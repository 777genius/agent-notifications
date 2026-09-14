package installruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestPolicySnapshotFencesAcrossRequests(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.PolicyEnabled = &enabled
	r.ExpectedGeneration = &l.Generation
	l, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Policy.Enabled || !first.Installation.Enabled || first.Installation.Ledger.ID != l.ID || first.Installation.Ledger.Generation != l.Generation {
		t.Fatalf("incorrect request snapshot: %+v", first)
	}
	enabled = false
	r.ExpectedGeneration = &l.Generation
	if _, err = Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, release, err := AcquireInstalledLease(ctx, r.ControlRoot, first.Installation); err == nil {
		release()
		t.Fatal("old request admitted after disable")
	}
	second, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if second.Policy.Enabled || second.Installation.Enabled || second.Installation.Ledger.Generation <= first.Installation.Ledger.Generation {
		t.Fatal("new request reused old policy")
	}
	if !first.Policy.Enabled {
		t.Fatal("earlier request mutated")
	}
}

func TestRequestPolicySnapshotUsesConfigLockAndDoesNotCreate(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json.lock")
	release, err := LockExisting(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = ReadPolicySnapshot(short, r.ControlRoot)
	cancel()
	release()
	if err == nil {
		t.Fatal("read did not honor policy config lock")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPolicySnapshot(ctx, r.ControlRoot); err == nil {
		t.Fatal("missing lock accepted")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("read created lock")
	}
	root := filepath.Join(t.TempDir(), "missing")
	s, err := ReadPolicySnapshot(ctx, root)
	if err != nil || s.Policy.Enabled || s.Installation.Enabled {
		t.Fatalf("missing: %+v %v", s, err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("read created root")
	}
}

// Every successful setup/repair provisions the policy lock even without an
// explicit policy mutation. Readers remain strictly no-create when it is lost.
func TestSetupRepairsMissingPolicySnapshotLock(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPolicySnapshot(ctx, r.ControlRoot); err != nil {
		t.Fatal("fresh setup snapshot:", err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json.lock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPolicySnapshot(ctx, r.ControlRoot); err == nil {
		t.Fatal("reader accepted missing lock")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reader created lock")
	}
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal("repair:", err)
	}
	if _, err := ReadPolicySnapshot(ctx, r.ControlRoot); err != nil {
		t.Fatal("repaired setup snapshot:", err)
	}
}

func TestPolicySnapshotDoesNotFollowPolicyLink(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(foreign, []byte(`{"schemaVersion":1,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	if err := os.Symlink(foreign, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ReadPolicySnapshot(ctx, r.ControlRoot); err == nil {
		t.Fatal("linked policy accepted")
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("linked policy repaired")
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("policy link replaced", err)
	}
}

func TestReadPolicySnapshotRejectsOversizedDocument(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxControlDocument+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPolicySnapshot(ctx, r.ControlRoot); err == nil {
		t.Fatal("oversized policy accepted")
	}
}
