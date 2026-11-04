//go:build linux || darwin

package setupwizard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
)

func TestGroupUninstallPreservesResultsWhenProfileCleanupFails(t *testing.T) {
	testUninstallProfileCleanupRetry(t, true)
}

func TestSingleUninstallPreservesResultsWhenProfileCleanupFails(t *testing.T) {
	testUninstallProfileCleanupRetry(t, false)
}

func testUninstallProfileCleanupRetry(t *testing.T, group bool) {
	t.Helper()
	req := installedClaudeSelectionRequest(t)
	req.Action, req.ExternalUninstalled = ActionUninstall, true
	if group {
		req.Agents = []string{"claude", "codex"}
	}
	req.CodexHome = t.TempDir()
	snapshot, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := materializer(req, snapshot, req.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	data := liveDataRoot(mat, req.InstallationID, string(portable.Claude))
	if data == "" {
		t.Fatal("installed Claude data root is missing")
	}
	sentinel := filepath.Join(data, "TEST-retained-user-data")
	if err := os.WriteFile(sentinel, []byte("preserve user data"), 0600); err != nil {
		t.Fatal(err)
	}
	injected := false
	req.Progress = func(phase string) {
		if phase == "agent-notify" {
			// Admission has checked the original profile. Damage only its host
			// sidecar so SDK removal commits before host cleanup observes it.
			if err := os.WriteFile(filepath.Join(data, liveProfileFile), []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
			injected = true
		}
	}
	removed, err := Run(testCtx(t), req)
	var malformed *json.SyntaxError
	if !injected || !errors.As(err, &malformed) || removed.Outcome != "incomplete" {
		t.Fatalf("expected post-removal sidecar failure: %+v %v", removed, err)
	}
	if retained, err := mat.RetainedEmpty(req.InstallationID); err != nil || !retained {
		t.Fatalf("fixture did not commit group removal: retained=%v err=%v", retained, err)
	}
	want := map[string]string{"claude": "completed"}
	if group {
		want["codex"] = "unchanged"
	}
	for _, target := range removed.Targets {
		if target.Unit == "agent-notify" && target.Outcome == want[target.Client] {
			delete(want, target.Client)
		}
	}
	if len(want) != 0 {
		t.Errorf("committed removal results hidden by cleanup failure: missing=%v result=%+v", want, removed)
	}
	req.Progress = nil
	retried, err := Run(testCtx(t), req)
	if !errors.As(err, &malformed) || retried.Outcome != "incomplete" {
		t.Errorf("retained retry reported success with malformed sidecar: %+v %v", retried, err)
	}
	profiles := map[string]string{"claude": req.ClaudeConfig, "codex": req.CodexHome, "cursor": "TEST-unselected-profile"}
	if err := writeLiveProfiles(data, profiles); err != nil {
		t.Fatal(err)
	}
	retried, err = Run(testCtx(t), req)
	if err != nil || retried.Outcome != "unchanged" || !retried.DataRetained {
		t.Fatalf("repaired retained cleanup did not finish: %+v %v", retried, err)
	}
	profiles, err = readLiveProfiles(data)
	if err != nil || profiles["claude"] != "" || group && profiles["codex"] != "" || !group && profiles["codex"] != req.CodexHome || profiles["cursor"] != "TEST-unselected-profile" {
		t.Fatalf("retained cleanup selected the wrong profile facts: %v %v", profiles, err)
	}
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "preserve user data" {
		t.Fatalf("cleanup changed retained user data: %q %v", body, err)
	}
}

func TestUninstallAbsentPortablePreservesHookRemoval(t *testing.T) {
	ctx := testCtx(t)
	home := t.TempDir()
	testenv.Set(t, home)
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	canonical := filepath.Join(home, "config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	req := Request{Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: boolPtr(true), AgentNotify: boolPtr(false), PluginRoot: writePluginBundle(t),
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, CodexHome: filepath.Join(home, "codex"),
	}
	if err := os.MkdirAll(req.CodexHome, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" || !codexsetup.HasManagedHooks(req.CodexHome) {
		t.Fatalf("install hook fixture: %+v %v", installed, err)
	}
	if live := LiveNotifyClients(control, req.Agents); len(live) != 0 {
		t.Fatalf("fixture unexpectedly installed portable client: %v", live)
	}
	req.Action, req.AgentNotify = ActionUninstall, boolPtr(true)
	removed, err := Run(ctx, req)
	if err != nil || codexsetup.HasManagedHooks(req.CodexHome) {
		t.Fatalf("hooks were not removed: %+v %v", removed, err)
	}
	if removed.Outcome != "completed" || removed.Reason != "" {
		t.Fatalf("completed hook removal relabeled as absent portable: %+v", removed)
	}
	// Claude's plugin-owned hooks are unchanged by this wizard. An absent
	// portable client alone must not manufacture a completed removal.
	req.Agents = []string{"claude"}
	unchanged, err := Run(ctx, req)
	if err != nil || unchanged.Outcome != "unchanged" || unchanged.Reason != "portable_absent" {
		t.Fatalf("absent unchanged units reported removal: %+v %v", unchanged, err)
	}
}

func TestUninstallRetainedEmptyPreservesHookRemoval(t *testing.T) {
	req := installedCodexHooksOnlyRequest(t)
	probe := buildProbe(t)
	req.PackageRoot = filepath.Join(filepath.Dir(req.ControlRoot), "package")
	writePackage(t, req.PackageRoot, probe)
	req.ScopeRoot = filepath.Join(filepath.Dir(req.ControlRoot), "scope")
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	req.Hooks, req.AgentNotify = boolPtr(false), boolPtr(true)
	req.ClientExecutable, req.Helper = probe, probe
	req.Agents = []string{"claude"}
	req.ClaudeConfig = filepath.Join(filepath.Dir(req.ControlRoot), "claude")
	if err := os.MkdirAll(req.ClaudeConfig, 0700); err != nil {
		t.Fatal(err)
	}
	req.ClaudeRunner = listingRunner{configRoot: req.ClaudeConfig}
	installed, err := Run(testCtx(t), req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install portable fixture: %+v %v", installed, err)
	}
	req.InstallationID = installed.InstallationID
	req.Action, req.ExternalUninstalled = ActionUninstall, true
	removed, err := Run(testCtx(t), req)
	if err != nil || removed.Outcome != "completed" || !removed.DataRetained || !codexsetup.HasManagedHooks(req.CodexHome) {
		t.Fatalf("retire portable fixture while retaining hooks: %+v %v", removed, err)
	}
	req.Hooks = boolPtr(true)
	req.Agents = []string{"claude", "codex"}
	removed, err = Run(testCtx(t), req)
	if err != nil || codexsetup.HasManagedHooks(req.CodexHome) || !removed.DataRetained {
		t.Fatalf("remove hooks beside retained data: %+v %v", removed, err)
	}
	if removed.Outcome != "completed" || removed.Reason != "" {
		t.Fatalf("completed hooks were relabeled already absent: %+v", removed)
	}
	unchanged, err := Run(testCtx(t), req)
	if err != nil || unchanged.Outcome != "unchanged" || unchanged.Reason != "already_absent" || !unchanged.DataRetained {
		t.Fatalf("empty retry invented a completed removal: %+v %v", unchanged, err)
	}
}

func TestSequentialUninstallWithAbsentSiblingsFinishesReservation(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Action, req.Agents, req.ExternalUninstalled = ActionUninstall, []string{"claude", "codex", "pi"}, true
	req.CodexHome, req.PiAgentDir = t.TempDir(), t.TempDir()
	removed, err := Run(testCtx(t), req)
	if err != nil || removed.Outcome != "completed" || !removed.DataRetained {
		t.Fatalf("sequential removal with absent siblings: %+v %v", removed, err)
	}
	if live := LiveNotifyClients(req.ControlRoot, req.Agents); len(live) != 0 {
		t.Fatalf("sequential uninstall left live clients: %v", live)
	}
	view := req
	view.Action = ActionInspect
	inspected, err := Run(testCtx(t), view)
	if err != nil || inspected.Outcome != "completed" {
		t.Fatalf("sequential uninstall left pending work: %+v %v", inspected, err)
	}
	for _, next := range inspected.NextActions {
		if next.Kind == "recover" || next.Kind == "resume" {
			t.Fatalf("sequential uninstall did not finish its reservation: %+v", inspected.NextActions)
		}
	}
}
