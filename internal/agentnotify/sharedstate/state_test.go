package sharedstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var keyA = strings.Repeat("a", 64)
var keyB = strings.Repeat("b", 64)
var runtimeA = strings.Repeat("c", 64)
var runtimeB = strings.Repeat("d", 64)

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(t.TempDir(), "TEST-shared-data-")
	if err != nil {
		t.Fatal(err)
	}
	if err = makePrivateFixture(root); err != nil {
		t.Fatal(err)
	}
	return root
}
func openStore(t *testing.T, root, key, runtime string) *Store {
	t.Helper()
	s, err := Open(root, key, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func endpoint() Record {
	return Record{Version: 1, Address: "127.0.0.1:48219", Token: strings.Repeat("e", 64), PID: 12345}
}
func locked(t *testing.T, s *Store) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := s.LockServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return release
}

// Isolation red condition: discovery from another profile, binding or installed
// runtime returns this daemon's credentials, or opening state touches policy.
func TestIdentityIsolationAndEphemeralScope(t *testing.T) {
	root := testRoot(t)
	s := openStore(t, root, keyA, runtimeA)
	release := locked(t, s)
	defer release()
	r := endpoint()
	if err := s.Publish(r); err != nil {
		t.Fatal(err)
	}
	same := openStore(t, root, keyA, runtimeA)
	if got, err := same.Read(); err != nil || got != r {
		t.Fatalf("same identity read: %v", err)
	}
	for _, other := range []*Store{openStore(t, testRoot(t), keyA, runtimeA), openStore(t, root, keyB, runtimeA), openStore(t, root, keyA, runtimeB)} {
		if _, err := other.Read(); !os.IsNotExist(err) {
			t.Fatalf("other identity read = %v", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".shared-mcp" {
		t.Fatal("Open initialized non-ephemeral data")
	}
	if s.Identity() != keyA+":"+runtimeA {
		t.Fatal("unexpected transport identity")
	}
}

// A stale owner's deferred shutdown must never clear a replacement daemon.
func TestRemovePreservesReplacementAndCorruption(t *testing.T) {
	s := openStore(t, testRoot(t), keyA, runtimeA)
	release := locked(t, s)
	defer release()
	first := endpoint()
	if err := s.Publish(first); err != nil {
		t.Fatal(err)
	}
	replacement := first
	replacement.Token = strings.Repeat("f", 64)
	replacement.PID++
	if err := s.Publish(replacement); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(first); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Read(); err != nil || got != replacement {
		t.Fatal("stale cleanup removed replacement")
	}
	if err := s.Remove(replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(); !os.IsNotExist(err) {
		t.Fatalf("removed read: %v", err)
	}
	corrupt := []byte(`{"token":"SECRET-DO-NOT-LOG","version":1}`)
	path := filepath.Join(s.dir, recordName)
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := makePrivateFixture(path); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{s.Publish(first), s.Remove(first)} {
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("corrupt record error: %v", err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(corrupt) {
		t.Fatal("corrupt state changed")
	}
}

// Exclusive permanent lock inodes must coordinate separately opened stores and
// cancellation must not unlink the inode or release another process's claim.
func TestConcurrentElectionAndServerLifetime(t *testing.T) {
	root := testRoot(t)
	s := openStore(t, root, keyA, runtimeA)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var in, peak atomic.Int32
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := Open(root, keyA, runtimeA)
			if err != nil {
				failures.Add(1)
				return
			}
			release, err := other.LockStart(ctx)
			if err != nil {
				failures.Add(1)
				return
			}
			defer release()
			n := in.Add(1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			in.Add(-1)
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || peak.Load() != 1 {
		t.Fatalf("election failures=%d max holders=%d", failures.Load(), peak.Load())
	}
	release := locked(t, s)
	before, err := os.Stat(filepath.Join(s.dir, "server.lock"))
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancelWait := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelWait()
	if _, err := openStore(t, root, keyA, runtimeA).LockServer(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second daemon lock=%v", err)
	}
	release()
	release()
	after, err := os.Stat(filepath.Join(s.dir, "server.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("lock inode replaced")
	}
	unlocked := locked(t, s)
	unlocked()
}

func TestInvalidEndpointPreservedAndNotPublished(t *testing.T) {
	s := openStore(t, testRoot(t), keyA, runtimeA)
	release := locked(t, s)
	defer release()
	for _, address := range []string{"localhost:80", "[::1]:80", "127.0.0.2:80", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:080", "127.0.0.1:http", "SECRET:80"} {
		r := endpoint()
		r.Address = address
		if err := s.Publish(r); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("address %q accepted or leaked", address)
		}
	}
	path := filepath.Join(s.dir, recordName)
	for _, data := range [][]byte{[]byte(strings.Repeat("X", maxRecord+1)), []byte(`null`), []byte(`{"identity":"wrong"}`)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := makePrivateFixture(path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Read(); err == nil {
			t.Fatal("invalid document accepted")
		}
		if err := s.Publish(endpoint()); err == nil {
			t.Fatal("invalid document overwritten")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Fatal("invalid document changed")
		}
	}
}

func TestIdentityAndExistingRootRequired(t *testing.T) {
	root := testRoot(t)
	for _, key := range []string{"", strings.Repeat("A", 64), strings.Repeat("x", 64), "../" + keyA} {
		if _, err := Open(root, key, runtimeA); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	missing := filepath.Join(root, "missing")
	if _, err := Open(missing, keyA, runtimeA); err == nil {
		t.Fatal("missing root created")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing root mutated")
	}
}

// Case aliases and duplicate fields are ambiguous documents even when their
// effective values match. They must remain untouched for explicit recovery.
func TestStrictDocumentSchemaPreservesAmbiguousRecords(t *testing.T) {
	s := openStore(t, testRoot(t), keyA, runtimeA)
	release := locked(t, s)
	defer release()
	r := endpoint()
	raw, err := json.Marshal(document{Identity: s.Identity(), Record: r})
	if err != nil {
		t.Fatal(err)
	}
	original := string(raw)
	cases := map[string]string{
		"duplicate version":    strings.Replace(original, `"version":1`, `"version":1,"version":1`, 1),
		"escaped duplicate":    strings.Replace(original, `"version":1`, `"version":1,"\u0076ersion":1`, 1),
		"capitalized identity": strings.Replace(original, `"identity":`, `"Identity":`, 1),
		"capitalized version":  strings.Replace(original, `"version":`, `"Version":`, 1),
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(s.dir, recordName)
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if err := makePrivateFixture(path); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Read(); err == nil {
				t.Fatal("ambiguous document accepted")
			}
			if err := s.Publish(r); err == nil {
				t.Fatal("ambiguous document replaced")
			}
			if err := s.Remove(r); err == nil {
				t.Fatal("ambiguous document removed")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != contents {
				t.Fatal("ambiguous document changed")
			}
		})
	}
}
