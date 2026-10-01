package geminievent

import (
	"context"
	"fmt"
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

// Red condition: separate hook processes both claim a marker/channel while
// unit-level consumer concurrency appears safe with a shared in-memory lock.
func TestCacheInterprocessClaim(t *testing.T) {
	if root := os.Getenv("AN_GEMINI_TEST_CACHE_ROOT"); root != "" {
		c, facts, _ := consumerFixture(t)
		c.Cache.Root = root
		claimed, err := c.Cache.claim(context.Background(), c.Binding, facts, WebhookChannel)
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
	c, _, _ := consumerFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCacheInterprocessClaim$", "-test.timeout=2s")
			cmd.Dir = c.Cache.Root // Only a fresh TEST directory is a child process cwd.
			cmd.Env = []string{"AN_GEMINI_TEST_CACHE_ROOT=" + c.Cache.Root, "TMPDIR=" + c.Cache.Root, "TMP=" + c.Cache.Root, "TEMP=" + c.Cache.Root}
			if value := os.Getenv("SystemRoot"); value != "" {
				cmd.Env = append(cmd.Env, "SystemRoot="+value)
			}
			data, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("cache child: %v / %s", err, data)
				return
			}
			switch string(data) {
			case "claimed":
				claimed.Add(1)
			case "duplicate":
			default:
				t.Errorf("unexpected cache child classification: %s", data)
			}
		}()
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("interprocess attempts = %d", claimed.Load())
	}
}

// Red condition: cache expiry/eviction grows a durable journal, or changing an
// installed generation or a distinct native marker keeps an old suppression.
func TestCacheWindowBoundAndBindingIsolation(t *testing.T) {
	c, facts, _ := consumerFixture(t)
	ctx := context.Background()
	claim := func(want bool) {
		t.Helper()
		got, err := c.Cache.claim(ctx, c.Binding, facts, DesktopChannel)
		if err != nil || got != want {
			t.Fatalf("claim = %v/%v, want %v", got, err, want)
		}
	}
	claim(true)
	claim(false)
	c.Binding.Generation++
	claim(true)
	c.Binding.InstallationID = "other-installation"
	claim(true)
	c.Clock.(*testClock).seconds.Store(70)
	claim(true) // Window expires at sixty seconds; no replay of prior observations.
	for i := range cacheLimit + 1 {
		facts.Timestamp = "marker-" + strconv.Itoa(i)
		claim(true)
	}
	data, err := readCache(c.Cache.Root)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect the persisted public privacy contract, not a mocked repository.
	if strings.Count(string(data), `"key"`) != cacheLimit {
		t.Fatalf("cache grew past bound: %d bytes", len(data))
	}
	facts.Timestamp = "marker-0"
	claim(true) // Eviction makes no exact-once promise.
}

// Red condition: a held interprocess lock waits beyond its 250ms allowance,
// or cache corruption creates a fresh claim and permits another effect.
func TestUnavailableCacheAndLockBudgetAreNeutral(t *testing.T) {
	c, facts, _ := consumerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := installruntime.Lock(ctx, filepath.Join(c.Cache.Root, ".observations.lock"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := c.Cache.claim(ctx, c.Binding, facts, DesktopChannel)
	release()
	if got || err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("lock was unbounded or admitted effect: %v/%v after %s", got, err, time.Since(start))
	}
	if err = os.WriteFile(filepath.Join(c.Cache.Root, "observations.json"), []byte("PRIVATE_CORRUPT_CACHE"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = c.Cache.claim(ctx, c.Binding, facts, DesktopChannel)
	if got || err == nil || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatalf("corrupt cache admitted/leaked: %v/%v", got, err)
	}
}
