//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// End-to-end configure admission reproduces #585's reported "preflight ready"
// followed by "enable initialization_recovery_required" mismatch. No registration
// or user-state mutation is allowed after read-only preflight refusal.
func TestNotificationConfigureRefusesUninitializedEnabledPolicyBeforeRegister(t *testing.T) {
	f, req, deps := configureFixture(t)
	req.Provider = "claude"
	req.PolicyOnly = true
	snap, err := installruntime.ReadPolicySnapshot(setupCommandContext(t), f.control)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	generation := snap.Installation.Ledger.Generation
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{
		ControlRoot: f.control, RuntimeRoot: f.runtime,
		Owner: "existing-installer", ConsumerID: "hooks",
		PolicyOnly: true, RefreshOnly: true,
		ExpectedGeneration: &generation, PolicyEnabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	before := setupCommandTree(t, f.root)
	result, err := configureNotifications(setupCommandContext(t), req, deps)
	if err == nil || result.Reason != "initialization_recovery_required" {
		t.Fatalf("invalid preflight accepted or wrong reason: %+v %v", result, err)
	}
	if len(result.Stages) == 0 || result.Stages[len(result.Stages)-1].Name != "preflight" ||
		result.Stages[len(result.Stages)-1].Outcome != "failed" {
		t.Fatalf("enable failure escaped preflight: %+v", result.Stages)
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("failed preflight modified owned config or private state")
	}
}

func TestNotificationConfigureRefusesMissingRetainedJournalBeforeMutation(t *testing.T) {
	f, req, deps := configureFixture(t)
	req.Provider = "claude"
	req.PolicyOnly = true
	if result, err := configureNotifications(setupCommandContext(t), req, deps); err != nil {
		t.Fatalf("first configure failed: %+v %v", result, err)
	}
	if err := os.RemoveAll(filepath.Join(f.control, "state", "journal")); err != nil {
		t.Fatal(err)
	}
	req.PreserveEnabled = true
	before := setupCommandTree(t, f.root)
	result, err := configureNotifications(setupCommandContext(t), req, deps)
	if err == nil || result.Reason != "initialization_recovery_required" {
		t.Fatalf("lost journal reported ready: %+v %v", result, err)
	}
	if len(result.Stages) == 0 || result.Stages[len(result.Stages)-1].Name != "preflight" {
		t.Fatalf("lost journal admitted to mutating stage: %+v", result.Stages)
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("configure recreated or modified a lost journal")
	}
}

func TestNotificationConfigureRefusesCorruptJournalBeforeEnable(t *testing.T) {
	f, req, deps := configureFixture(t)
	req.Provider = "claude"
	req.PolicyOnly = true
	if result, err := configureNotifications(setupCommandContext(t), req, deps); err != nil {
		t.Fatalf("healthy first setup failed: %+v %v", result, err)
	}
	file := filepath.Join(f.control, "state", "journal", "journal.json")
	if err := os.WriteFile(file, []byte("{not valid journal\\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req.PreserveEnabled = true
	before := setupCommandTree(t, f.root)
	result, err := configureNotifications(setupCommandContext(t), req, deps)
	if err == nil || result.Reason != "initialization_recovery_required" {
		t.Fatalf("corrupt but existing journal was accepted: %+v %v", result, err)
	}
	if len(result.Stages) == 0 || result.Stages[len(result.Stages)-1].Name != "preflight" ||
		result.Stages[len(result.Stages)-1].Outcome != "failed" {
		t.Fatalf("corrupt journal reached mutating stage: %+v", result.Stages)
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("preflight altered corrupt user journal or registration")
	}
}
