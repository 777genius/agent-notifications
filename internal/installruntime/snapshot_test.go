package installruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotMissingAndCorruptAreReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	s, err := ReadInstalledSnapshot(root)
	if err != nil || s.Ledger.Generation != 0 || s.Enabled {
		t.Fatalf("missing: %+v %v", s, err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("read created root")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ownership.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInstalledSnapshot(root); err == nil {
		t.Fatal("corrupt accepted")
	}
	entries, _ := os.ReadDir(root)
	data, _ := os.ReadFile(path)
	if len(entries) != 1 || string(data) != "broken" {
		t.Fatal("read modified corrupt state")
	}
}

func TestLeaseFencesPolicyGenerationAndRecovery(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handoff := func(InstalledSnapshot) error { calls++; return nil }
	if err := WithInstalledLease(ctx, r.ControlRoot, disabled, handoff); err == nil {
		t.Fatal("disabled admitted")
	}
	enable := true
	r.PolicyEnabled = &enable
	r.ExpectedGeneration = &l.Generation
	l, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || enabled.Ledger.Generation <= disabled.Ledger.Generation {
		t.Fatal("policy did not advance generation")
	}
	if err := WithInstalledLease(ctx, r.ControlRoot, enabled, handoff); err != nil {
		t.Fatal(err)
	}
	enable = false
	r.ExpectedGeneration = &l.Generation
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := WithInstalledLease(ctx, r.ControlRoot, enabled, handoff); err == nil {
		t.Fatal("stale generation admitted")
	}
	if err := os.WriteFile(filepath.Join(r.ControlRoot, "transaction.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	recovery, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil || !recovery.Recovery {
		t.Fatalf("recovery observation: %v", err)
	}
	if err := WithInstalledLease(ctx, r.ControlRoot, recovery, handoff); err == nil {
		t.Fatal("recovery admitted")
	}
	if calls != 1 {
		t.Fatalf("handoffs=%d", calls)
	}
}

func TestAcquireLeaseLifetimeAndMissingLock(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.PolicyEnabled = &enabled
	r.ExpectedGeneration = &l.Generation
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	expected, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	current, release, err := AcquireInstalledLease(ctx, r.ControlRoot, expected)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if current.Ledger.Generation != expected.Ledger.Generation {
		t.Fatal("wrong snapshot")
	}
	path := filepath.Join(r.ControlRoot, ".component-install.lock")
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if unlock, err := LockExisting(short, path); err == nil {
		unlock()
		t.Fatal("lease released before handoff")
	}
	release()
	unlock, err := LockExisting(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, unlock, err := AcquireInstalledLease(ctx, r.ControlRoot, expected); err == nil {
		unlock()
		t.Fatal("missing lock accepted")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("lease recreated missing inode")
	}
	missing := filepath.Join(t.TempDir(), "absent", "lock")
	if unlock, err := LockExisting(ctx, missing); err == nil {
		unlock()
		t.Fatal("missing lock accepted")
	}
	if _, err := os.Lstat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("created missing parent")
	}
}
