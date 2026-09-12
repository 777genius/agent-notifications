//go:build darwin || linux

package notifier

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestReadinessManagedNoMutation(t *testing.T) {
	m := pr3ManagedFixture(t)
	h := newPR3Harness(t)
	h.delivery.Installation = m
	h.delivery.Spool = nil
	before, err := pr3ManagedFiles(t, m.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if out := h.delivery.CheckReadiness(context.Background(), pr3Request()); out.Status != "ready" {
			t.Fatal(out)
		}
	}
	after, err := pr3ManagedFiles(t, m.ControlRoot)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("owned tree mutated", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	lease, err := m.Acquire(ctx)
	if err != nil {
		t.Fatal("lease retained", err)
	}
	lease.Release()
	lock := filepath.Join(m.ControlRoot, ".component-install.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	h.probes = 0
	if out := h.delivery.CheckReadiness(context.Background(), pr3Request()); out.Status == "ready" || h.probes != 0 {
		t.Fatal(out)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("created missing lock")
	}
}

func TestReadinessDirectProbeReapedAndLegacyReject(t *testing.T) {
	// Inert shell models the exact old verified capability grammar. No native/OS
	// notification API is invoked by this process lifecycle test.
	script := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --capabilities-json ] && exit 0\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := nativePermissionCommand(context.Background(), script, pr3Correlation, pr3Nonce)
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 1 {
		t.Fatal("legacy grammar accepted")
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cmd = nativePermissionCommand(ctx, script, pr3Correlation, pr3Nonce)
	start := time.Now()
	if err := cmd.Run(); err == nil || cmd.ProcessState == nil || time.Since(start) > time.Second {
		t.Fatal("probe not bounded/reaped")
	}
}

// Compare entries with the shared file fingerprint, not a second tree hash.
func pr3ManagedFiles(t *testing.T, root string) (map[string]installruntime.Identity, error) {
	t.Helper()
	entries := map[string]installruntime.Identity{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		id, err := installruntime.Fingerprint(path)
		entries[path] = id
		return err
	})
	return entries, err
}
