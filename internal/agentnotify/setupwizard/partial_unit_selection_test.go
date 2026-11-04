//go:build linux || darwin

package setupwizard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/testenv"
)

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
	req.AgentNotify, req.CodexAgentNotify = nil, boolPtr(true)
	plan, err := Plan(testCtx(t), req)
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
