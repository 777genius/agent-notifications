//go:build linux || darwin

package setupwizard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/testenv"
)

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
