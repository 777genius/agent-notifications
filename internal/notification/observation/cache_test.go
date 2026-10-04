package observation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

type testClock struct {
	seconds atomic.Int64
	boot    string
}

func (c *testClock) Now() (string, float64, error) {
	return c.boot, float64(c.seconds.Load()), nil
}

func hashedKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cacheFixture(t *testing.T) (*RecentCache, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{boot: "test-boot"}
	clock.seconds.Store(10)
	return &RecentCache{Root: root, Clock: clock}, hashedKey("test-marker")
}

// Red condition: separate hook processes both claim a marker/channel while
// unit-level consumer concurrency appears safe with a shared in-memory lock.
func TestCacheInterprocessClaim(t *testing.T) {
	if root := os.Getenv("AN_OBSERVATION_TEST_CACHE_ROOT"); root != "" {
		c, key := cacheFixture(t)
		c.Root = root
		fmt.Println("ready")
		var signal [1]byte
		if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != '\n' {
			os.Exit(3)
		}
		claimed, err := c.Claim(context.Background(), key, 2)
		if err != nil {
			fmt.Print("unavailable")
			os.Exit(2)
		}
		if claimed {
			fmt.Print("claimed")
		} else {
			fmt.Print("duplicate")
		}
		os.Exit(0)
	}
	c, key := cacheFixture(t)
	// Prepare durable state before racing hook processes. Desktop is already
	// attempted, but Webhook remains unclaimed: all children race its first bit.
	// This removes cold publication/lock creation from concurrent process startup
	// without relaxing the production 250ms allowance or accepting unavailable.
	boot, now, err := c.Clock.Now()
	if !validTime(boot, now, err) {
		t.Fatal("invalid cache fixture clock")
	}
	bootHash := sha256.Sum256([]byte(boot))
	seed, err := json.Marshal(cacheState{Boot: hex.EncodeToString(bootHash[:]),
		Entries: []cacheEntry{{key, now + cacheWindow, 1}}})
	if err != nil {
		t.Fatal(err)
	}
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 3*time.Second)
	release, err := installruntime.Lock(seedCtx, filepath.Join(c.Root, ".observations.lock"))
	if err != nil {
		seedCancel()
		t.Fatal(err)
	}
	err = writeCache(c.Root, seed)
	release()
	seedCancel()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The fixture watchdog covers startup and READY/GO, while every production
	// claim still creates its own unchanged ClaimBudget context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type childProcess struct {
		cmd         *exec.Cmd
		input       io.WriteCloser
		output      io.ReadCloser
		diagnostics bytes.Buffer
	}
	children := make([]*childProcess, 0, 4)
	for range 4 {
		child := &childProcess{cmd: exec.CommandContext(ctx, executable, "-test.run=^TestCacheInterprocessClaim$", "-test.timeout=5s")}
		cmd := child.cmd
		cmd.WaitDelay = time.Second
		cmd.Dir = c.Root // Only a fresh TEST directory is a child process cwd.
		cmd.Env = []string{"AN_OBSERVATION_TEST_CACHE_ROOT=" + c.Root, "TMPDIR=" + c.Root, "TMP=" + c.Root, "TEMP=" + c.Root, "HOME=" + c.Root, "USERPROFILE=" + c.Root}
		if value := os.Getenv("SystemRoot"); value != "" {
			cmd.Env = append(cmd.Env, "SystemRoot="+value)
		}
		// Do not mix coverage warnings with the exact stdout protocol.
		cmd.Stderr = &child.diagnostics
		t.Cleanup(func() {
			if cmd.Process != nil && cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
			if child.input != nil {
				_ = child.input.Close()
			}
			if child.output != nil {
				_ = child.output.Close()
			}
		})
		if child.input, err = cmd.StdinPipe(); err != nil {
			t.Fatal(err)
		}
		if child.output, err = cmd.StdoutPipe(); err != nil {
			t.Fatal(err)
		}
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
	}
	// All children have loaded their instrumented binary and constructed their
	// fixture before any Webhook claim begins; process startup cannot consume it.
	for _, child := range children {
		var signal [6]byte
		if _, err = io.ReadFull(child.output, signal[:]); err != nil || string(signal[:]) != "ready\n" {
			t.Fatalf("cache child readiness: %q / %v", signal, err)
		}
	}
	startClaims := make(chan struct{})
	var claimed, duplicates atomic.Int32
	var wg sync.WaitGroup
	for _, child := range children {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startClaims
			if _, err := child.input.Write([]byte{'\n'}); err != nil {
				t.Errorf("cache child GO: %v", err)
				return
			}
			_ = child.input.Close()
			data, readErr := io.ReadAll(io.LimitReader(child.output, 32))
			err := child.cmd.Wait()
			if readErr != nil {
				t.Errorf("cache child output: %v", readErr)
				return
			}
			if err != nil {
				t.Errorf("cache child: %v / stdout %q / stderr %q", err, data, child.diagnostics.String())
				return
			}
			switch string(data) {
			case "claimed":
				claimed.Add(1)
			case "duplicate":
				duplicates.Add(1)
			default:
				t.Errorf("unexpected cache child classification: %q / stderr %q", data, child.diagnostics.String())
			}
		}()
	}
	close(startClaims)
	wg.Wait()
	if claimed.Load() != 1 || duplicates.Load() != 3 {
		t.Fatalf("interprocess attempts = %d, duplicates = %d", claimed.Load(), duplicates.Load())
	}
}

// Red condition: cache expiry/eviction grows a durable journal or a distinct
// hashed key keeps an old suppression. Binding isolation remains in Gemini.
func TestCacheWindowAndBound(t *testing.T) {
	c, key := cacheFixture(t)
	ctx := context.Background()
	claim := func(want bool) {
		t.Helper()
		start := time.Now()
		got, err := c.Claim(ctx, key, 1)
		if err != nil || got != want {
			t.Fatalf("claim marker %q = %v/%v, want %v, after %s", key, got, err, want, time.Since(start))
		}
	}
	claim(true)
	claim(false)
	c.Clock.(*testClock).seconds.Store(70)
	claim(true) // Window expires at sixty seconds; no replay of prior observations.
	// Seed a full on-disk fixture instead of requiring 257 synchronous durable
	// writes to each meet the production claim budget on a busy CI host. Real
	// claims below still validate, read, evict and publish through the OS adapter;
	// cache_unavailable is never retried or accepted as success.
	data, err := readCache(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	var state cacheState
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.Entries = nil
	for i := range cacheLimit {
		key = hashedKey("marker-" + strconv.Itoa(i))
		state.Entries = append(state.Entries, cacheEntry{key, 130, 1})
	}
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeCache(c.Root, data); err != nil {
		t.Fatal(err)
	}
	claim(false) // A retained marker is still a duplicate at capacity.
	key = hashedKey("marker-0")
	claim(false)
	key = hashedKey("marker-" + strconv.Itoa(cacheLimit))
	claim(true) // A distinct marker displaces the first equally old entry.
	data, err = readCache(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect the persisted public privacy contract, not a mocked repository.
	if strings.Count(string(data), `"key"`) != cacheLimit {
		t.Fatalf("cache grew past bound: %d bytes", len(data))
	}
	key = hashedKey("marker-0")
	claim(true) // Eviction makes no exact-once promise.
	claim(false)
}

// Red condition: a held interprocess lock waits beyond its 250ms allowance,
// or cache corruption creates a fresh claim and permits another effect.
func TestUnavailableCacheAndLockBudgetAreNeutral(t *testing.T) {
	c, key := cacheFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := installruntime.Lock(ctx, filepath.Join(c.Root, ".observations.lock"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := c.Claim(ctx, key, 1)
	release()
	if got || err == nil {
		t.Fatalf("lock was unbounded or admitted effect: %v/%v after %s", got, err, time.Since(start))
	}
	var failure *ClaimFailure
	if !errors.As(err, &failure) || failure.Phase != "lock" || failure.Class != "deadline" || failure.BudgetState != "deadline" || failure.MayHavePublished || failure.BudgetElapsed > 500*time.Millisecond {
		t.Fatalf("held lock diagnostics lost the admission boundary: %#v", err)
	}
	if err = os.WriteFile(filepath.Join(c.Root, "observations.json"), []byte("PRIVATE_CORRUPT_CACHE"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = c.Claim(ctx, key, 1)
	if got || err == nil || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatalf("corrupt cache admitted/leaked: %v/%v", got, err)
	}
}

// Red condition: generic keys merge, raw identifiers/unsupported bits persist,
// or adding the second channel extends the first channel's suppression window.
func TestCacheHashedKeysAndFixedWindow(t *testing.T) {
	c, key := cacheFixture(t)
	ctx := context.Background()
	claim := func(key string, bit uint8, want bool) {
		t.Helper()
		got, err := c.Claim(ctx, key, bit)
		if err != nil || got != want {
			t.Fatalf("claim %s bit %d = %v/%v, want %v", key, bit, got, err, want)
		}
	}
	claim(key, 1, true)
	claim(key, 1, false)
	c.Clock.(*testClock).seconds.Store(50)
	claim(key, 2, true)
	claim(key, 2, false)
	other := hashedKey("caller-independent-other-key")
	claim(other, 1, true)
	data, err := readCache(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	var state cacheState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Entries) != 2 || state.Entries[0] != (cacheEntry{key, 70, 3}) || state.Entries[1] != (cacheEntry{other, 110, 1}) {
		t.Fatalf("keys, bits or original window changed: %+v", state.Entries)
	}
	// Invalid generic inputs must leave the exact valid cache bytes intact.
	for _, input := range []struct {
		key string
		bit uint8
	}{
		{"PRIVATE_RAW_IDENTIFIER", 1}, {strings.Repeat("g", 64), 1}, {key[:62], 1},
		{key, 0}, {key, 3}, {key, 4}, {key, 255},
	} {
		got, err := c.Claim(ctx, input.key, input.bit)
		if got || err == nil || err.Error() != "cache_unavailable" {
			t.Fatalf("invalid input admitted/leaked: %v/%v", got, err)
		}
	}
	after, err := readCache(c.Root)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatalf("invalid input mutated persisted cache: %v", err)
	}
	c.Clock.(*testClock).seconds.Store(70)
	claim(key, 1, true) // Exact first expiry; the second bit did not renew it.
	claim(key, 2, true)
	claim(other, 1, false) // Another hashed key retains its own window.
	c.Clock.(*testClock).seconds.Store(0)
	claim(key, 1, true) // Rollback invalidates the old future window.
	c.Clock.(*testClock).boot = "next-test-boot"
	claim(key, 1, true) // A new boot must not retain old attempted bits.
}
