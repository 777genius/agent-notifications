//go:build linux

package setupwizard

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// This test is opt-in in the generic suite, but the dedicated native CI lane
// installs exactly the reporter's Claude Code 2.1.317 and REQUIRES a PASS.
// It invokes the real UAP materializer, real Claude plugin listing and real
// user-scope skills-dir projection inside an isolated, throwaway HOME.
func TestWizardClaudeNativeSkillsDirLifecycleE2E(t *testing.T) {
	executable := os.Getenv("AGENT_NOTIFY_CLAUDE_NATIVE_EXE")
	if executable == "" {
		t.Skip("native Claude executable supplied by dedicated E2E lane")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("native Claude executable must be an absolute path")
	}
	home := filepath.Join(t.TempDir(), "e2e-home")
	claudeRoot := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("DISABLE_AUTOUPDATER", "1")
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	scope := filepath.Join(filepath.Dir(control), "scope")
	if err := os.MkdirAll(scope, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude"}, Yes: true,
		Hooks: &off, PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtimeRoot,
		GlobalConfig: global, ClaudeConfig: claudeRoot, ClientExecutable: executable,
		Helper: probe, ScopeRoot: scope,
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("native Claude activation was not verified: outcome=%q reason=%q err=%v", installed.Outcome, installed.Reason, err)
	}
	if installed.ExitCode() != 0 || installed.InstallationID == "" {
		t.Fatalf("native activation missing committed identity: %+v", installed)
	}

	// Do not trust a successful installer result without an independent
	// live native CLI observation and durable UAP state.
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := materializer(req, snapshot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	managedPath := ""
	for _, installation := range state.Installations {
		if installation.InstallationID != installed.InstallationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == string(domain.ClientClaude) {
				if binding.Activation != domain.ActivationActive || binding.Verification != domain.VerificationInstalled {
					t.Fatalf("native client not verified: activation=%q verification=%q", binding.Activation, binding.Verification)
				}
				managedPath = binding.TargetLocator
			}
		}
	}
	if managedPath == "" {
		t.Fatal("no live managed Claude binding")
	}
	out, err := exec.CommandContext(ctx, executable, "plugin", "list", "--json").Output()
	if err != nil {
		t.Fatalf("read-only native Claude listing failed: %v", err)
	}
	if claude.PluginStatusFromList(out, "agent-notify", managedPath) != claude.StatusInstalled {
		t.Fatal("native Claude CLI did not recognize the exact enabled @skills-dir projection")
	}
	var listing []map[string]any
	if err := json.Unmarshal(out, &listing); err != nil {
		t.Fatal("native plugin list is not an array")
	}
	entryFound := false
	for _, entry := range listing {
		if entry["id"] == "agent-notify@skills-dir" && entry["enabled"] == true &&
			entry["scope"] == "user" && entry["installPath"] == managedPath {
			entryFound = true
		}
	}
	if !entryFound {
		t.Fatal("native listing omitted enabled user-scope managed @skills-dir identity")
	}
	req.Action, req.Yes = ActionInspect, false
	view, err := Run(ctx, req)
	if err != nil || view.ExitCode() != 0 || len(view.Readiness) != 1 || view.Readiness[0].Delivery != "not_tracked" {
		t.Fatalf("inspected native installation incorrectly: outcome=%q reason=%q delivery=%+v err=%v", view.Outcome, view.Reason, view.Readiness, err)
	}
	for _, action := range view.NextActions {
		if action.Kind == "test-notification" {
			t.Fatalf("inspect repeated a test it cannot verify: %+v", view.NextActions)
		}
	}
	req.Action, req.Yes = ActionUninstall, true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("native Claude removal failed: outcome=%q reason=%q err=%v", removed.Outcome, removed.Reason, err)
	}
	if len(LiveNotifyClients(control, []string{"claude"})) != 0 {
		t.Fatal("native removal retained a live UAP Claude binding")
	}
	after, err := exec.CommandContext(ctx, executable, "plugin", "list", "--json").Output()
	if err != nil {
		t.Fatalf("native Claude listing after removal failed: %v", err)
	}
	if got := claude.PluginStatusFromList(after, "agent-notify", managedPath); got != claude.StatusAbsent {
		t.Fatalf("native Claude still sees removed plugin or returned an unrecognized listing: status=%d", got)
	}
}
