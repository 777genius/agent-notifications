//go:build linux || darwin

package setupwizard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func buildProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(src, []byte(`package main
import ("encoding/json"; "os")
func main() { json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true}) }
`), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "probe")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %s %v", body, err)
	}
	return out
}

func writePackage(t *testing.T, root, probe string) {
	t.Helper()
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"plugin.json":                  []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.0"}`),
		"mcp.json":                     []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"agent-notify":{"type":"stdio","command":"./bin/probe","args":[],"env":{}}}}`),
		"skills/agent-notify/SKILL.md": []byte("---\nname: agent-notify\ndescription: Wizard fixture\n---\n"),
		"bin/probe":                    body,
	}
	for rel, data := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if rel == "bin/probe" {
			mode = 0700
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func managedRuntime(t *testing.T) (control, runtime, global, primary string, generation uint64) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control = filepath.Join(root, "control")
	runtime = filepath.Join(root, "runtime")
	global = filepath.Join(root, "global", "config.json")
	primary = filepath.Join(runtime, "primary")
	if err := os.MkdirAll(filepath.Dir(global), 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: primary, Data: []byte("inert primary"), Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return control, runtime, global, primary, ledger.Generation
}

func TestWizardRejectsMutationWithoutYes(t *testing.T) {
	control, _, _, _, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{Action: ActionInstall, Agents: []string{"codex"}, ControlRoot: control})
	if err == nil || got.Outcome != "invalid" || got.Reason != "noninteractive_requires_yes" || got.ExitCode() != 2 {
		t.Fatalf("yes: %+v %v", got, err)
	}
}

func TestWizardEmptyAgentsCancels(t *testing.T) {
	control, _, _, _, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{Action: ActionInstall, Yes: true, ControlRoot: control})
	if err != nil || got.Outcome != "cancelled" || got.ExitCode() != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
}

func TestWizardInstallInspectUninstall(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks:       boolPtr(false),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe, ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.Outcome != "completed" {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inspect missed portable binding: %+v", view.Targets)
	}
	if live := LiveNotifyClients(control, []string{"codex", "claude"}); strings.Join(live, ",") != "codex" {
		t.Fatalf("live clients: %v", live)
	}
	req.Action = ActionUninstall
	req.Yes = true
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err = Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("portable binding survived uninstall: %+v", view.Targets)
		}
	}
}

func TestWizardInstallFromReleaseZip(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	base := filepath.Dir(control)
	pkg := filepath.Join(base, "release-pkg")
	archive := filepath.Join(base, portableasset.AssetName(runtime.GOOS, runtime.GOARCH))
	built, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: probe, OutputRoot: pkg, Archive: archive,
	})
	if err != nil {
		t.Fatal(err)
	}
	codexConfig := filepath.Join(base, "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: boolPtr(false), PackageRoot: archive, PackageSHA256: built.ArchiveSHA256,
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe, ScopeRoot: filepath.Join(base, "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("zip install: %+v %v", installed, err)
	}
	req.PackageSHA256 = strings.Repeat("0", 64)
	blocked, err := Run(ctx, req)
	if err == nil || blocked.Reason != "package_acquisition_failed" {
		t.Fatalf("checksum: %+v %v", blocked, err)
	}
}

func TestWizardOmittedPackageRequiresHostAcquisition(t *testing.T) {
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: boolPtr(false),
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global,
		CodexHome: filepath.Join(filepath.Dir(control), "codex"), ClientExecutable: "/bin/true",
		Helper: filepath.Join(runtimeRoot, "primary"), ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	})
	if err == nil || got.Reason != "package_required" {
		t.Fatalf("omitted: %+v %v", got, err)
	}
}

func TestWizardInstallFromHostAcquisition(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	base := filepath.Dir(control)
	pkg := filepath.Join(base, "release-pkg")
	archive := filepath.Join(base, portableasset.AssetName(runtime.GOOS, runtime.GOARCH))
	built, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: probe, OutputRoot: pkg, Archive: archive,
	})
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	asset := portableasset.AssetName(runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.43.0/checksums.txt":
			_, _ = io.WriteString(w, built.ArchiveSHA256+"  "+asset+"\n")
		case "/v1.43.0/" + asset:
			_, _ = w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	codexConfig := filepath.Join(base, "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: boolPtr(false),
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe, ScopeRoot: filepath.Join(base, "scope"),
		ReleaseVersion: "1.43.0", ReleaseDownloadRoot: srv.URL,
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("acquired install: %+v %v", installed, err)
	}
}

func TestWizardUnsupportedUpdate(t *testing.T) {
	control, _, _, _, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{Action: ActionUpdate, Agents: []string{"codex"}, Yes: true, ControlRoot: control})
	if err == nil || got.Outcome != "incomplete" || got.Reason != "action_not_published" {
		t.Fatalf("update: %+v %v", got, err)
	}
}

func writePluginBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"bin/codex-hook-wrapper.sh":  "#!/bin/sh\nexit 0\n",
		"bin/codex-hook-wrapper.cmd": "@echo off\r\nexit /b 0\r\n",
		"bin/hook-wrapper.sh":        "#!/bin/sh\nexit 0\n",
		"sounds/task-complete.mp3":   "not-really-audio",
		"config/config.json":         `{"notifications":{}}`,
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestWizardCodexHooksWithoutConfigure(t *testing.T) {
	ctx := testCtx(t)
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	control, runtime, global, _, _ := managedRuntime(t)
	bundle := writePluginBundle(t)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	home := filepath.Join(envHome, "codex-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: boolPtr(true), AgentNotify: &off,
		PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: home,
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("hooks install: %+v %v", installed, err)
	}
	hooks := filepath.Join(home, "hooks.json")
	data, err := os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks.json: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("hooks-only install opened UAP state")
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var hooksInstalled, notifyInstalled bool
	for _, target := range view.Targets {
		if target.Unit == "hooks" && target.Outcome == "installed" {
			hooksInstalled = true
		}
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			notifyInstalled = true
		}
	}
	if !hooksInstalled || notifyInstalled {
		t.Fatalf("inspect after hooks-only: %+v", view.Targets)
	}
	req.Action = ActionUninstall
	req.Yes = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("hooks uninstall: %+v %v", removed, err)
	}
	data, err = os.ReadFile(hooks)
	if err == nil && strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks survived uninstall: %s", data)
	}
}

type listingRunner struct {
	configRoot string
}

func (r listingRunner) Run(_ context.Context, _ ports.Command) (ports.CommandResult, error) {
	root := filepath.Join(r.configRoot, "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return ports.CommandResult{Stdout: []byte("[]")}, nil
	}
	if err != nil {
		return ports.CommandResult{}, err
	}
	listed := make([]map[string]any, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		path := filepath.Join(root, entry.Name())
		body, readErr := os.ReadFile(filepath.Join(path, ".claude-plugin", "plugin.json"))
		if readErr != nil {
			continue
		}
		var manifest map[string]any
		if json.Unmarshal(body, &manifest) != nil {
			continue
		}
		name, _ := manifest["name"].(string)
		listed = append(listed, map[string]any{
			"id": name + "@skills-dir", "version": manifest["version"], "scope": "user",
			"enabled": true, "installPath": path,
		})
	}
	body, err := json.Marshal(listed)
	return ports.CommandResult{Stdout: body}, err
}

func TestWizardMixedPerClientOptOuts(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off, on := false, true
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true,
		Hooks: &off, ClaudeAgentNotify: &off, CodexAgentNotify: &on,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("mixed install: %+v %v", installed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var claudeMCP, codexMCP string
	for _, target := range view.Targets {
		if target.Unit != "agent-notify" {
			continue
		}
		switch target.Client {
		case "claude":
			claudeMCP = target.Outcome
		case "codex":
			codexMCP = target.Outcome
		}
	}
	if claudeMCP == "installed" || codexMCP != "installed" {
		t.Fatalf("mixed opt-outs: %+v", view.Targets)
	}
	if len(view.Readiness) == 0 {
		t.Fatal("inspect omitted readiness")
	}
	for _, fact := range view.Readiness {
		if fact.Permission != "unsupported" || fact.Delivery != "not_verified" {
			t.Fatalf("readiness mixed download with delivery: %+v", fact)
		}
	}
}

func TestWizardSecondClientAddDoesNotReviseExisting(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	base := Request{
		Action: ActionInstall, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(base.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	claudeReq := base
	claudeReq.Agents = []string{"claude"}
	installed, err := Run(ctx, claudeReq)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("claude: %+v %v", installed, err)
	}
	if installed.Readiness[0].Restart != "pending" || installed.Readiness[0].Delivery != "not_verified" {
		t.Fatalf("install readiness: %+v", installed.Readiness)
	}
	claudeBinding := ""
	for _, target := range installed.Targets {
		if target.Unit == "agent-notify" {
			claudeBinding = target.Reason
		}
	}
	otherPkg := filepath.Join(filepath.Dir(control), "other-package")
	writePackage(t, otherPkg, probe)
	if err := os.WriteFile(filepath.Join(otherPkg, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: Revised\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mismatch := base
	mismatch.Agents = []string{"codex"}
	mismatch.PackageRoot = otherPkg
	blocked, err := Run(ctx, mismatch)
	if err == nil || blocked.Outcome != "incomplete" || blocked.Reason != "update_required" {
		t.Fatalf("mismatch: %+v %v", blocked, err)
	}
	if len(blocked.NextActions) != 2 || blocked.NextActions[0].Kind != "update" || blocked.NextActions[1].Kind != "install" {
		t.Fatalf("next: %+v", blocked.NextActions)
	}
	codexReq := base
	codexReq.Agents = []string{"codex"}
	added, err := Run(ctx, codexReq)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("codex add: %+v %v", added, err)
	}
	inspectReq := base
	inspectReq.Action = ActionInspect
	inspectReq.Yes = false
	inspectReq.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatal(err)
	}
	var claudeOK, codexOK bool
	for _, target := range view.Targets {
		if target.Unit != "agent-notify" || target.Outcome != "installed" {
			continue
		}
		if target.Client == "claude" {
			if target.Reason != claudeBinding {
				t.Fatalf("claude binding revised: %s vs %s", claudeBinding, target.Reason)
			}
			claudeOK = true
		}
		if target.Client == "codex" {
			codexOK = true
		}
	}
	if !claudeOK || !codexOK {
		t.Fatalf("second client inspect: %+v", view.Targets)
	}
}

func TestWizardReinstallRetainsInstallation(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	statePath := filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")
	firstID := installationIDFromState(t, statePath)
	req.Action = ActionUninstall
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	req.Action = ActionInstall
	req.InstallationID = firstID
	reinstalled, err := Run(ctx, req)
	if err != nil || reinstalled.Outcome != "completed" {
		t.Fatalf("reinstall: %+v %v", reinstalled, err)
	}
	if got := installationIDFromState(t, statePath); got != firstID {
		t.Fatalf("retained installation lost: %s vs %s", firstID, got)
	}
}

func TestWizardUninstallExplicitFalsePreservesNotifyWithoutPackage(t *testing.T) {
	ctx := testCtx(t)
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	bundle := writePluginBundle(t)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	home := filepath.Join(envHome, "codex-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	on := true
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: &on, AgentNotify: &on,
		PackageRoot: pkg, PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: home, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	off := false
	req.Action = ActionUninstall
	req.Hooks = nil
	req.AgentNotify = &off
	req.PackageRoot = ""
	req.PackageFetcher = func(context.Context, string) ([]byte, error) {
		t.Fatal("uninstall fetched a package")
		return nil, nil
	}
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	if len(removed.Readiness) == 0 {
		t.Fatal("uninstall omitted readiness")
	}
	for _, fact := range removed.Readiness {
		if fact.Permission != "unsupported" || fact.Delivery != "not_verified" {
			t.Fatalf("uninstall required permission/delivery: %+v", fact)
		}
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.Outcome != "completed" || view.ExitCode() != 0 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	var hooksInstalled, notifyInstalled bool
	for _, target := range view.Targets {
		if target.Unit == "hooks" && target.Outcome == "installed" {
			hooksInstalled = true
		}
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			notifyInstalled = true
		}
	}
	if hooksInstalled || !notifyInstalled {
		t.Fatalf("explicit false did not keep notify: %+v", view.Targets)
	}
}

func TestWizardUninstallConflictsWithPendingInstallIntent(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	plantPendingInstallIntent(t, ctx, control, runtime, installed.Generation)
	req.Action = ActionUninstall
	req.PackageRoot = ""
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" || got.ExitCode() != 1 {
		t.Fatalf("conflict: %+v %v", got, err)
	}
	if len(got.Command) < 4 || got.Command[2] != "--action" || got.Command[3] != "install" {
		t.Fatalf("retry must keep pending install: %v", got.Command)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflict uninstalled portable binding: %+v", view.Targets)
	}
}

func plantPendingInstallIntent(t *testing.T, ctx context.Context, control, runtime string, generation uint64) {
	t.Helper()
	plantPendingIntent(t, ctx, control, runtime, generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: generation,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
}

func plantPendingIntent(t *testing.T, ctx context.Context, control, runtime string, generation uint64, intent portablesetup.Intent) {
	t.Helper()
	if intent.SetupIntentID == "" {
		intent.SetupIntentID = "pending-install-intent"
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	path := portablesetup.IntentPath(control)
	res := installruntime.PendingMutation{ID: intent.SetupIntentID, Owner: "existing-installer", IntentRef: path}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, Owner: "existing-installer", RuntimeRoot: runtime, ConsumerID: "existing",
		RefreshOnly: true, ExpectedGeneration: &generation, Reservation: &res,
		Files: []installruntime.File{{Path: path, Data: append(payload, '\n'), Mode: 0600}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWizardResumeRestoresOmittedParamsFromPendingIntent(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, gen := managedRuntime(t)
	probe := buildProbe(t)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	scope := filepath.Join(filepath.Dir(control), "scope")
	if err := os.MkdirAll(scope, 0700); err != nil {
		t.Fatal(err)
	}
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen, SourceRevision: "1.43.0", SourceDigest: strings.Repeat("a", 64),
		Targets: []portablesetup.IntentTarget{{
			Client: "codex", InstallationID: "inst-codex", Profile: codexConfig, Units: []string{"direct-mcp"},
		}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		ClientExecutable: probe, Helper: probe, ScopeRoot: scope,
	})
	if got.Outcome == "cancelled" || got.Reason == "empty_selection" {
		t.Fatalf("did not restore pending agents: %+v %v", got, err)
	}
	if got.Reason == "noninteractive_requires_yes" {
		t.Fatalf("matching pending intent still required --yes: %+v %v", got, err)
	}
	if got.Reason == "client_config_required" {
		t.Fatalf("did not restore pending profile: %+v %v", got, err)
	}
	if got.Reason != "package_required" {
		t.Fatalf("resume: %+v %v", got, err)
	}
	joined := strings.Join(got.Command, " ")
	for _, want := range []string{"--agents codex", "--codex-home " + codexConfig, "--hooks false", "--agent-notify true", "--installation-id inst-codex"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("retry omitted %q: %v", want, got.Command)
		}
	}
}

func TestWizardEmptyUninstallConflictsWithPendingInstall(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingInstallIntent(t, ctx, control, runtime, gen)
	got, err := Run(ctx, Request{Action: ActionUninstall, Yes: true, ControlRoot: control, RuntimeRoot: runtime})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("empty uninstall: %+v %v", got, err)
	}
	joined := strings.Join(got.Command, " ")
	if !strings.Contains(joined, "--action install") || !strings.Contains(joined, "--agents codex") {
		t.Fatalf("retry must keep pending install: %v", got.Command)
	}
}

func TestWizardResumeRejectsDifferentAgents(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingInstallIntent(t, ctx, control, runtime, gen)
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"claude"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different agents: %+v %v", got, err)
	}
}

func TestWizardResumeRejectsDifferentDigest(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen, SourceDigest: strings.Repeat("a", 64),
		Targets: []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, PackageSHA256: strings.Repeat("b", 64),
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different digest: %+v %v", got, err)
	}
}

func TestWizardResumeRestoresOmittedUninstallFromPendingIntent(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-uninstall-intent", Action: "uninstall", Stage: "revoke-locator",
		ExpectedGeneration: gen,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Profile: codexConfig, Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{Action: ActionUninstall, ControlRoot: control, RuntimeRoot: runtime})
	if got.Outcome == "cancelled" || got.Reason == "empty_selection" {
		t.Fatalf("did not restore pending uninstall: %+v %v", got, err)
	}
	if got.Reason == "noninteractive_requires_yes" {
		t.Fatalf("matching pending uninstall still required --yes: %+v %v", got, err)
	}
	if got.Outcome != "unchanged" || got.Reason != "portable_absent" {
		t.Fatalf("resume uninstall: %+v %v", got, err)
	}
}

func TestWizardCodexUninstallDoesNotInventExternalAttestation(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	req.Action = ActionUninstall
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "external_uninstall_required" {
		t.Fatalf("yes invented attestation: %+v %v", got, err)
	}
	joined := strings.Join(got.Command, " ")
	if !strings.Contains(joined, "--external-uninstalled") {
		t.Fatalf("retry omitted attestation flag: %v", got.Command)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refused uninstall removed portable binding: %+v", view.Targets)
	}
	req.Action = ActionUninstall
	req.Yes = true
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("attested uninstall: %+v %v", removed, err)
	}
}

func TestWizardUninstallOmittedUnitsRemovesManagedWithoutPackage(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	req.Action = ActionUninstall
	req.Hooks = nil
	req.AgentNotify = nil
	req.PackageRoot = ""
	req.ExternalUninstalled = true
	req.PackageFetcher = func(context.Context, string) ([]byte, error) {
		t.Fatal("uninstall fetched a package")
		return nil, nil
	}
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.ExitCode() != 0 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("omitted uninstall left notify: %+v", view.Targets)
		}
	}
}

func installationIDFromState(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Installations []struct {
			InstallationID string `json:"installation_id"`
		} `json:"installations"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Installations) != 1 || state.Installations[0].InstallationID == "" {
		t.Fatalf("installations: %s", body)
	}
	return state.Installations[0].InstallationID
}

func TestWizardUninstallDoesNotRestoreDirectMCP(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, primary, gen := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	mcpConfig := filepath.Join(filepath.Dir(control), "client", "config")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mcpConfig), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := clientsetup.Apply(ctx, clientsetup.Request{
		ControlRoot: control, RuntimeRoot: runtime, Command: primary, ConfigPath: mcpConfig,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: gen,
	}); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
		MCPConfig: map[string]string{"codex": mcpConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var notify, direct string
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" {
			notify = target.Outcome
		}
		if target.Unit == "direct-mcp" {
			direct = target.Outcome
		}
	}
	if notify != "installed" || direct != "absent" {
		t.Fatalf("handoff inspect: notify=%s direct=%s targets=%+v", notify, direct, view.Targets)
	}
	req.Action = ActionUninstall
	req.Yes = true
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err = Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	notify, direct = "", ""
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" {
			notify = target.Outcome
		}
		if target.Unit == "direct-mcp" {
			direct = target.Outcome
		}
	}
	if notify == "installed" {
		t.Fatalf("portable survived uninstall: %+v", view.Targets)
	}
	if direct == "installed" {
		t.Fatalf("uninstall restored direct MCP: %+v", view.Targets)
	}
}
