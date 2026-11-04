//go:build linux || darwin

package setupwizard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/testenv"
)

func TestExplicitNotifyPreservesOmittedCodexHooks(t *testing.T) {
	for _, action := range []Action{ActionUpdate, ActionRepair} {
		for _, perClient := range []bool{false, true} {
			name := string(action) + "/global"
			if perClient {
				name = string(action) + "/per-client"
			}
			t.Run(name, func(t *testing.T) {
				req, _, _, _ := uninstallProfileFixture(t)
				req.Action, req.Hooks = action, nil
				if perClient {
					req.AgentNotify, req.CodexAgentNotify = nil, boolPtr(true)
					// A different client's explicit hook choice cannot select Codex hooks.
					req.ClaudeHooks = boolPtr(true)
				}
				plan, err := Plan(testCtx(t), req)
				if err != nil || !plan.Ready || plan.Request.CodexHooks == nil || *plan.Request.CodexHooks {
					t.Fatalf("explicit notify enabled omitted Codex hooks: %+v %v", plan, err)
				}
				explicit := req
				explicit.CodexHooks = boolPtr(true)
				explicitPlan, _ := Plan(testCtx(t), explicit)
				if explicitPlan.Request.CodexHooks == nil || !*explicitPlan.Request.CodexHooks {
					t.Fatal("explicit Codex hook choice was rewritten")
				}
				// Stop at the existing public progress boundary after hook effects,
				// before SDK native activation. This checks selection, not activation.
				ctx, cancel := context.WithCancel(testCtx(t))
				defer cancel()
				admitted := false
				req.Progress = func(phase string) {
					if phase == "agent-notify" {
						admitted = true
						cancel()
					}
				}
				before := policyConflictFiles(t, req.CodexHome)
				got, err := Run(ctx, req)
				if !admitted || !errors.Is(err, context.Canceled) {
					t.Fatalf("notify-only update did not reach native admission: %+v %v", got, err)
				}
				if codexsetup.HasManagedHooks(req.CodexHome) || !reflect.DeepEqual(before, policyConflictFiles(t, req.CodexHome)) {
					t.Fatal("omitted Codex hooks changed the client profile")
				}
			})
		}
	}
}

func installedCodexHooksOnlyRequest(t *testing.T) Request {
	t.Helper()
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
	installed, err := Run(testCtx(t), req)
	if err != nil || installed.Outcome != "completed" || !codexsetup.HasManagedHooks(req.CodexHome) {
		t.Fatalf("install hooks fixture: %+v %v", installed, err)
	}
	return req
}

func TestExplicitHooksPreserveOmittedNotifyOnUpdateAndRepair(t *testing.T) {
	for _, action := range []Action{ActionUpdate, ActionRepair} {
		for _, perClient := range []bool{false, true} {
			name := string(action) + "/global"
			if perClient {
				name = string(action) + "/per-client"
			}
			t.Run(name, func(t *testing.T) {
				req := installedCodexHooksOnlyRequest(t)
				req.Action, req.AgentNotify = action, nil
				if perClient {
					req.Hooks, req.CodexHooks = nil, boolPtr(true)
				}
				plan, err := Plan(testCtx(t), req)
				if err != nil || !plan.Ready || plan.Request.CodexAgentNotify == nil || *plan.Request.CodexAgentNotify {
					t.Fatalf("explicit hooks enabled omitted notify: %+v %v", plan, err)
				}
				if plan.Request.Hooks != req.Hooks || plan.Request.CodexHooks != req.CodexHooks {
					t.Fatal("explicit hooks selection changed")
				}
				got, err := Run(testCtx(t), req)
				if err != nil || got.Outcome != "completed" || !codexsetup.HasManagedHooks(req.CodexHome) {
					t.Fatalf("hooks lifecycle failed: %+v %v", got, err)
				}
				if live := LiveNotifyClients(req.ControlRoot, req.Agents); len(live) != 0 {
					t.Fatalf("omitted notification unit was installed: %v", live)
				}
			})
		}
	}
}

func TestOmittedNotifyPreservesExplicitClientOverrides(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Action, req.Agents = ActionRepair, []string{"claude", "codex"}
	req.CodexHome = filepath.Join(filepath.Dir(req.ControlRoot), "codex")
	if err := os.MkdirAll(req.CodexHome, 0700); err != nil {
		t.Fatal(err)
	}
	req.AgentNotify = nil
	before := policyConflictFiles(t, filepath.Dir(req.ControlRoot))
	plan, err := Plan(testCtx(t), req)
	if err == nil || plan.Ready || plan.Result.Reason != "not_installed" || plan.Request.CodexAgentNotify != nil {
		t.Fatalf("omitted notification-only request lost missing sibling: %+v %v", plan, err)
	}
	if got, err := Run(testCtx(t), req); err == nil || got.Reason != "not_installed" {
		t.Fatalf("missing sibling mutated live client: %+v %v", got, err)
	}
	if after := policyConflictFiles(t, filepath.Dir(req.ControlRoot)); !reflect.DeepEqual(before, after) {
		t.Fatal("missing sibling preflight changed installed files")
	}
	req.AgentNotify, req.CodexAgentNotify = nil, boolPtr(true)
	plan, err = Plan(testCtx(t), req)
	if err == nil || plan.Ready || plan.Result.Reason != "not_installed" ||
		plan.Request.ClaudeAgentNotify == nil || !*plan.Request.ClaudeAgentNotify ||
		plan.Request.CodexAgentNotify == nil || !*plan.Request.CodexAgentNotify {
		t.Fatalf("explicit missing notify request was lost: %+v %v", plan, err)
	}
	req.CodexAgentNotify = boolPtr(false)
	plan, err = Plan(testCtx(t), req)
	if err != nil || !plan.Ready || plan.Request.ClaudeAgentNotify == nil || !*plan.Request.ClaudeAgentNotify ||
		plan.Request.CodexAgentNotify == nil || *plan.Request.CodexAgentNotify {
		t.Fatalf("omitted/live and explicit/off selections differ: %+v %v", plan, err)
	}
	req.AgentNotify = boolPtr(true)
	plan, err = Plan(testCtx(t), req)
	if err != nil || !plan.Ready || plan.Request.ClaudeAgentNotify != nil ||
		plan.Request.CodexAgentNotify == nil || *plan.Request.CodexAgentNotify {
		t.Fatalf("explicit global/client selection was rewritten: %+v %v", plan, err)
	}
}

func TestOmittedNotifyRetainedUpdatePreservesDataWithoutInstallingClient(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Action, req.AgentNotify, req.ExternalUninstalled = ActionUninstall, nil, true
	removed, err := Run(testCtx(t), req)
	if err != nil || removed.Outcome != "completed" || !removed.DataRetained {
		t.Fatalf("retain installed data: %+v %v", removed, err)
	}
	statePath := filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state", "state-v2.json")
	sentinel := filepath.Join(retainedDataRoot(t, statePath), "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = filepath.Join(filepath.Dir(req.ControlRoot), "updated-package")
	writePackage(t, req.PackageRoot, req.Helper)
	if err := os.WriteFile(filepath.Join(req.PackageRoot, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action, req.ExternalUninstalled = ActionUpdate, false
	plan, err := Plan(testCtx(t), req)
	if err != nil || !plan.Ready || !strings.Contains(plan.Text, "metadata-only") {
		t.Fatalf("retained metadata plan: %+v %v", plan, err)
	}
	updated, err := Run(testCtx(t), plan.Request)
	if err != nil || updated.Outcome != "completed" || updated.Reason != "retained_source_updated" {
		t.Fatalf("retained metadata update: %+v %v", updated, err)
	}
	if live := LiveNotifyClients(req.ControlRoot, req.Agents); len(live) != 0 {
		t.Fatalf("metadata update installed a client: %v", live)
	}
	req.Action = ActionInstall
	added, err := Run(testCtx(t), req)
	if err != nil || added.Outcome != "completed" || added.InstallationID != req.InstallationID {
		t.Fatalf("reinstall retained installation: %+v %v", added, err)
	}
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "retain\n" {
		t.Fatalf("retained data changed: %q %v", body, err)
	}
}
