//go:build linux || darwin

package portable

import (
	"os"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestDiscoveryDoesNotGrantExecutionAuthority(t *testing.T) {
	b, name, _ := fixture(t)
	before, err := Discover(testContext(t), b.DataRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	// Metadata is deliberately cheap. It can select a daemon epoch but cannot
	// bless changed executable bytes; actual startup must still acquire a lease.
	if err := os.WriteFile(before.Executable, []byte("modified executable"), 0700); err != nil {
		t.Fatal(err)
	}
	observed, err := Discover(testContext(t), b.DataRoot, name)
	if err != nil || observed.SHA256 != before.SHA256 {
		t.Fatalf("metadata lookup unexpectedly grants or changes authority: %+v %v", observed, err)
	}
	if lease, err := Acquire(testContext(t), b.DataRoot, name); err == nil {
		lease.Release()
		t.Fatal("modified executable admitted for execution")
	}
}

func TestRuntimeEpochCheckedAgainstPolicySnapshot(t *testing.T) {
	b, name, request := fixture(t)
	old, err := Discover(testContext(t), b.DataRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	before, err := installruntime.Fingerprint(old.Executable)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedGeneration = nil
	request.Files = []installruntime.File{{Path: old.Executable, Before: before, Data: []byte("new owned runtime epoch"), Mode: 0700}}
	if _, err := installruntime.Commit(testContext(t), request); err != nil {
		t.Fatal(err)
	}
	current, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CheckRuntimeSnapshot(current, old.SHA256); err == nil {
		t.Fatal("old daemon accepted a new policy snapshot after primary refresh")
	}
	newEpoch, err := Discover(testContext(t), b.DataRoot, name)
	if err != nil || newEpoch.SHA256 == old.SHA256 {
		t.Fatal("refresh did not select a new endpoint epoch", err)
	}
	if err := b.CheckRuntimeSnapshot(current, newEpoch.SHA256); err != nil {
		t.Fatal("current daemon rejected its verified snapshot", err)
	}
	request.Files = nil
	request.RemoveConsumer = true
	if _, err := installruntime.Commit(testContext(t), request); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(testContext(t), b.DataRoot, name); err == nil {
		t.Fatal("revoked consumer remained discoverable")
	}
}
