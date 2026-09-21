//go:build linux || darwin

package setupwizard

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
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

func copyPackage(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
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
	primary = filepath.Join(runtime, "bin", "claude-notifications")
	if err := os.MkdirAll(filepath.Dir(global), 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: primary, Data: []byte(installruntime.WriterProtocolMarker), Mode: 0700}},
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

func TestPlanDoesNotRequireYesOrMutate(t *testing.T) {
	control, runtime, global, _, gen := managedRuntime(t)
	off := false
	plan, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: boolPtr(true), AgentNotify: &off,
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
	})
	if err != nil || !plan.Ready || plan.Result.Outcome != "ready" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "Plan: action=install") || !strings.Contains(plan.Text, "permission-dialog=explicit") {
		t.Fatalf("text: %s", plan.Text)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != gen {
		t.Fatalf("preflight mutated ledger: gen=%d pending=%v err=%v", snap.Ledger.Generation, snap.Ledger.PendingMutation, err)
	}
}

func TestPlanNotifyRequiresClientExecutable(t *testing.T) {
	control, runtime, global, _, _ := managedRuntime(t)
	off := false
	plan, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: false,
		Hooks: &off, AgentNotify: boolPtr(true),
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: filepath.Join(filepath.Dir(control), "codex-profile"),
	})
	if plan.Ready || plan.Result.Reason != "client_executable_required" {
		t.Fatalf("executable: %+v %v", plan, err)
	}
}

func TestPlanDoesNotRereadEnvDefaults(t *testing.T) {
	control, runtime, global, _, _ := managedRuntime(t)
	explicit := filepath.Join(filepath.Dir(control), "explicit-codex")
	envHome := filepath.Join(filepath.Dir(control), "env-home")
	envCodex := filepath.Join(filepath.Dir(control), "env-codex")
	for _, dir := range []string{explicit, envHome, envCodex} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", envHome)
	t.Setenv("CODEX_HOME", envCodex)
	off := false
	plan, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: false,
		Hooks: &off, AgentNotify: boolPtr(true),
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: explicit,
	})
	if plan.Ready || plan.Result.Reason != "client_executable_required" {
		t.Fatalf("executable: %+v %v", plan, err)
	}
	if plan.Request.CodexHome != explicit {
		t.Fatalf("plan reread env: %+v", plan.Request)
	}
	omitted, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: false,
		Hooks: &off, AgentNotify: boolPtr(true),
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
	})
	if err == nil && omitted.Request.CodexHome == envCodex {
		t.Fatal("plan filled CodexHome from env")
	}
}

func TestPlanShowsSourceDigestWithoutMutating(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, gen := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	off := false
	plan, err := Plan(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	})
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "source-digest=") || !strings.Contains(plan.Text, "helper-digest=") || !strings.Contains(plan.Text, "helper-version=") {
		t.Fatalf("missing helper identity: %s", plan.Text)
	}
	if plan.Request.InstallationID == "" || !strings.Contains(plan.Text, "installation-id="+plan.Request.InstallationID) || plan.Result.InstallationID != plan.Request.InstallationID {
		t.Fatalf("plan omitted reserved installation id: text=%s req=%s result=%s", plan.Text, plan.Request.InstallationID, plan.Result.InstallationID)
	}
	if !strings.Contains(plan.Text, "binding-id=") {
		t.Fatalf("plan omitted binding id: %s", plan.Text)
	}
	if plan.Request.TreeDigest == "" || plan.Request.HelperDigest == "" || plan.Request.HelperVersion == "" {
		t.Fatalf("plan omitted source/helper identity: %+v", plan.Request)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != gen {
		t.Fatalf("preflight mutated ledger: %+v %v", snap.Ledger, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("plan wrote UAP state")
	}
}

func TestPlanReservedIDIsReusedOnRun(t *testing.T) {
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
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready || plan.Request.InstallationID == "" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	reserved := plan.Request.InstallationID
	reservedBinding := plan.Request.BindingIDs["codex"]
	if reservedBinding == "" {
		t.Fatal("plan omitted reserved binding id")
	}
	if plan.Request.TreeDigest == "" || plan.Request.HelperDigest == "" {
		t.Fatalf("plan omitted source/helper identity: %+v", plan.Request)
	}
	runReq := plan.Request
	runReq.Yes = true
	got, err := Run(ctx, runReq)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("run: %+v %v", got, err)
	}
	if got.InstallationID != reserved {
		t.Fatalf("run allocated a different installation id: plan=%s run=%s", reserved, got.InstallationID)
	}
	mat, err := materializer(runReq, installruntime.InstalledSnapshot{}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil || len(state.Installations) != 1 || state.Installations[0].InstallationID != reserved {
		t.Fatalf("uap installation: %+v err=%v", state.Installations, err)
	}
	bindingID := ""
	for _, binding := range state.Installations[0].Clients {
		bindingID = binding.ClientBindingID
	}
	if bindingID != reservedBinding || !strings.Contains(plan.Text, "binding-id="+bindingID) {
		t.Fatalf("run used a different binding id: plan=%s installed=%s text=%s", reservedBinding, bindingID, plan.Text)
	}
}

func TestPlanShowsEveryClientBindingID(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig, filepath.Join(filepath.Dir(control), "scope")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	plan, err := Plan(ctx, Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"},
		Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	})
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	claudeID := plan.Request.BindingIDs["claude"]
	codexID := plan.Request.BindingIDs["codex"]
	if claudeID == "" || codexID == "" || claudeID == codexID {
		t.Fatalf("reserved bindings: %+v", plan.Request.BindingIDs)
	}
	if !strings.Contains(plan.Text, "claude-binding-id="+claudeID) || !strings.Contains(plan.Text, "codex-binding-id="+codexID) {
		t.Fatalf("plan omitted a client binding: %s", plan.Text)
	}
}

func TestPlanShowsMixedRepairPerBindingDigests(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(filepath.Dir(control), "package-r1")
	copyPackage(t, pkg, r1)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: r1, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = pkg
	req.Action = ActionUpdate
	req.Agents = []string{"codex"}
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := os.ReadFile(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.Agents = []string{"claude", "codex"}
	req.Yes = false
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("mixed repair plan: %+v %v", plan, err)
	}
	claude := inspectField(plan.Text, "claude-source-digest=")
	codex := inspectField(plan.Text, "codex-source-digest=")
	if claude == "" || claude == codex {
		t.Fatalf("plan collapsed mixed repair digests: %s", plan.Text)
	}
	req.Action = ActionUpdate
	updatePlan, err := Plan(ctx, req)
	if err != nil {
		t.Fatalf("mixed update plan: %+v %v", updatePlan, err)
	}
	if inspectField(updatePlan.Text, "claude-source-digest=") != claude || inspectField(updatePlan.Text, "codex-source-digest=") != codex {
		t.Fatalf("update plan collapsed mixed digests: %s", updatePlan.Text)
	}
	after, err := os.ReadFile(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("mixed repair plan mutated UAP state")
	}
}

func inspectField(text, key string) string {
	idx := strings.Index(text, key)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(key):]
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		return rest[:i]
	}
	return rest
}

func TestWizardRunRefusesPlanDigestDrift(t *testing.T) {
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
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready || plan.Request.TreeDigest == "" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runReq := plan.Request
	runReq.Yes = true
	got, err := Run(ctx, runReq)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "source_identity_drift" {
		t.Fatalf("mutated package after plan: %+v %v", got, err)
	}
}

func TestWizardRunRejectsReservedBindingIDDrift(t *testing.T) {
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
	scope := filepath.Join(filepath.Dir(control), "scope")
	if err := os.MkdirAll(scope, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:  scope,
		BindingIDs: map[string]string{"codex": "not-the-reserved-binding"},
	})
	if err == nil || got.Outcome != "incomplete" || got.Reason != "binding_id_drift" {
		t.Fatalf("reserved binding drift: %+v %v", got, err)
	}
}

func TestWizardRunRejectsSecondClientBindingIDDrift(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig, filepath.Join(filepath.Dir(control), "scope")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex", "claude"}, Yes: true,
		Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
		BindingIDs:   map[string]string{"claude": "not-the-reserved-binding"},
	}
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "binding_id_drift" {
		t.Fatalf("second client binding drift: %+v %v", got, err)
	}
	plan, err := Plan(ctx, req)
	if plan.Ready || plan.Result.Reason != "binding_id_drift" {
		t.Fatalf("plan second client binding drift: %+v %v", plan, err)
	}
}

func TestPlanOmittedPackageRequiresAcquisition(t *testing.T) {
	control, runtime, global, primary, _ := managedRuntime(t)
	off := false
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: &off, AgentNotify: boolPtr(true),
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: primary, Helper: primary,
	})
	if plan.Ready || plan.Result.Reason != "package_required" {
		t.Fatalf("omitted package: %+v %v", plan, err)
	}
}

func TestPlanAcquiresHostPackageForDigest(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, gen := managedRuntime(t)
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
	off := false
	plan, err := Plan(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: &off, AgentNotify: boolPtr(true),
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:      filepath.Join(base, "scope"),
		ReleaseVersion: "1.43.0", ReleaseDownloadRoot: srv.URL,
	})
	if err != nil || !plan.Ready {
		t.Fatalf("acquired plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "source-digest=") || !strings.Contains(plan.Text, "helper-digest=") {
		t.Fatalf("missing acquired digest: %s", plan.Text)
	}
	if explicitAbs(plan.Request.PackageRoot) {
		t.Fatalf("plan leaked acquired path: %s", plan.Request.PackageRoot)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != gen {
		t.Fatalf("acquired plan mutated ledger: %+v %v", snap.Ledger, err)
	}
}

func TestWizardInspectInvalidStillExitsTwo(t *testing.T) {
	got, err := Run(testCtx(t), Request{Action: ActionInspect, Agents: []string{"codex"}})
	if err == nil || got.Outcome != "invalid" || got.Reason != "control_root_required" || got.ExitCode() != 2 {
		t.Fatalf("invalid inspect: %+v %v", got, err)
	}
}

func TestWizardInspectReportsPendingJournal(t *testing.T) {
	control, runtime, global, primary, _ := managedRuntime(t)
	plantWizardJournal(t, control)
	got, err := Run(testCtx(t), Request{
		Action: ActionInspect, Agents: []string{"codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "incomplete" || got.Reason != "recovery_required" || got.ExitCode() != 0 {
		t.Fatalf("inspect recovery: %+v", got)
	}
	found := false
	for _, next := range got.NextActions {
		if next.Kind == "recover" && strings.Contains(next.Reason, "wizard-pending-op") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing recover action: %+v", got.NextActions)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); err != nil {
		t.Fatalf("inspect recovered journal: %v", err)
	}
}

func TestWizardInspectReportsBothPendingJournals(t *testing.T) {
	control, runtime, global, primary, _ := managedRuntime(t)
	plantWizardJournalNamed(t, control, "wizard-pending-op")
	plantWizardJournalNamed(t, control, "wizard-pending-op-2")
	got, err := Run(testCtx(t), Request{
		Action: ActionInspect, Agents: []string{"codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "incomplete" || got.Reason != "recovery_required" || got.ExitCode() != 0 {
		t.Fatalf("inspect recovery: %+v", got)
	}
	reason := ""
	for _, next := range got.NextActions {
		if next.Kind == "recover" {
			reason = next.Reason
		}
	}
	if !strings.Contains(reason, "wizard-pending-op") || !strings.Contains(reason, "wizard-pending-op-2") {
		t.Fatalf("inspect hid a pending journal: %+v", got.NextActions)
	}
	for _, opID := range []string{"wizard-pending-op", "wizard-pending-op-2"} {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(control), "uap", "state", "operations", opID+".json")); err != nil {
			t.Fatalf("inspect recovered %s: %v", opID, err)
		}
	}
}

func TestWizardInspectReportsPendingKernelJournal(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, primary, _ := managedRuntime(t)
	plantWizardKernelJournal(t, ctx, control, runtime)
	got, err := Run(ctx, Request{
		Action: ActionInspect, Agents: []string{"codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "incomplete" || got.Reason != "recovery_required" || got.ExitCode() != 0 {
		t.Fatalf("inspect kernel recovery: %+v", got)
	}
	found := false
	for _, next := range got.NextActions {
		if next.Kind == "recover" && next.Reason == "kernel-journal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing kernel recover action: %+v", got.NextActions)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); err != nil {
		t.Fatalf("inspect recovered kernel journal: %v", err)
	}
}

func TestWizardInspectDoesNotAcquirePackage(t *testing.T) {
	control, runtime, global, primary, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{
		Action: ActionInspect, Agents: []string{"codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary,
		ReleaseVersion: "1.43.0", ReleaseDownloadRoot: "https://example.invalid/missing",
		PackageFetcher: func(context.Context, string) ([]byte, error) {
			t.Fatal("inspect fetched a package")
			return nil, nil
		},
	})
	if err != nil || got.Outcome != "completed" || got.ExitCode() != 0 {
		t.Fatalf("inspect: %+v %v", got, err)
	}
}

func TestWizardInspectOmittedAgentsReportsBothClients(t *testing.T) {
	control, runtime, global, primary, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{
		Action: ActionInspect, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary,
	})
	if err != nil || got.Outcome != "completed" || got.ExitCode() != 0 || got.Reason == "empty_selection" {
		t.Fatalf("omitted inspect: %+v %v", got, err)
	}
	saw := map[string]string{}
	for _, target := range got.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = target.Outcome
		}
	}
	if saw["claude"] != "absent" || saw["codex"] != "absent" {
		t.Fatalf("omitted inspect clients: %+v", got.Targets)
	}
}

func TestPlanShowsPendingRecoveryWithoutMutating(t *testing.T) {
	control, runtime, global, _, gen := managedRuntime(t)
	plantWizardJournal(t, control)
	off := false
	plan, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: boolPtr(true), AgentNotify: &off,
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
	})
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "recovery-pending=wizard-pending-op") {
		t.Fatalf("missing recovery text: %s", plan.Text)
	}
	found := false
	for _, next := range plan.Result.NextActions {
		if next.Kind == "recover" && strings.Contains(next.Reason, "wizard-pending-op") {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan recover action: %+v", plan.Result.NextActions)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); err != nil {
		t.Fatalf("hooks plan recovered journal: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != gen {
		t.Fatalf("plan recovered journal: %+v %v", snap.Ledger, err)
	}
}

func TestPlanNotifyShowsPendingRecoveryWithoutMutating(t *testing.T) {
	control, runtime, global, _, gen := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	plantWizardJournal(t, control)
	off := false
	plan, err := Plan(testCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"}, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
		PackageFetcher: func(context.Context, string) ([]byte, error) {
			t.Fatal("notify plan fetched a package during pending recovery")
			return nil, nil
		},
	})
	if err != nil || !plan.Ready {
		t.Fatalf("notify plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "recovery-pending=wizard-pending-op") {
		t.Fatalf("missing notify recovery text: %s", plan.Text)
	}
	found := false
	for _, next := range plan.Result.NextActions {
		if next.Kind == "recover" && strings.Contains(next.Reason, "wizard-pending-op") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notify plan recover action: %+v", plan.Result.NextActions)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); err != nil {
		t.Fatalf("notify plan recovered journal: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != gen {
		t.Fatalf("notify plan mutated ledger: %+v %v", snap.Ledger, err)
	}
}

func TestPlanNotifyShowsPendingKernelRecoveryWithoutMutating(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	plantWizardKernelJournal(t, ctx, control, runtime)
	off := false
	plan, err := Plan(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
		PackageFetcher: func(context.Context, string) ([]byte, error) {
			t.Fatal("notify plan fetched a package during pending kernel recovery")
			return nil, nil
		},
	})
	if err != nil || !plan.Ready {
		t.Fatalf("notify kernel plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "recovery-pending=kernel-journal") {
		t.Fatalf("missing notify kernel recovery text: %s", plan.Text)
	}
	if strings.Contains(plan.Text, "source-digest=") {
		t.Fatalf("kernel plan still previewed package: %s", plan.Text)
	}
	found := false
	for _, next := range plan.Result.NextActions {
		if next.Kind == "recover" && next.Reason == "kernel-journal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("notify kernel plan recover action: %+v", plan.Result.NextActions)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); err != nil {
		t.Fatalf("notify kernel plan recovered journal: %v", err)
	}
}

func TestPlanShowsPendingKernelRecoveryWithoutMutating(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	plantWizardKernelJournal(t, ctx, control, runtime)
	off := false
	plan, err := Plan(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"},
		Hooks: boolPtr(true), AgentNotify: &off,
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
	})
	if err != nil || !plan.Ready {
		t.Fatalf("kernel plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "recovery-pending=kernel-journal") {
		t.Fatalf("missing kernel recovery text: %s", plan.Text)
	}
	found := false
	for _, next := range plan.Result.NextActions {
		if next.Kind == "recover" && next.Reason == "kernel-journal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("kernel plan recover action: %+v", plan.Result.NextActions)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); err != nil {
		t.Fatalf("kernel plan recovered journal: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || !snap.Recovery || snap.Ledger.PendingMutation != nil {
		t.Fatalf("kernel plan mutated ledger: %+v %v", snap, err)
	}
}

func TestWizardInstallRecoversPendingJournal(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	plantWizardJournal(t, control)
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
		t.Fatalf("install recover: %+v %v", installed, err)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); !os.IsNotExist(err) {
		t.Fatalf("install left pending journal: %v", err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.Outcome == "recovery_required" || view.Reason == "recovery_required" {
		t.Fatalf("inspect after recover: %+v %v", view, err)
	}
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("install after recover missed binding: %+v", view.Targets)
	}
}

func TestWizardInstallRecoversBothPendingJournals(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	plantWizardJournalNamed(t, control, "wizard-pending-op")
	plantWizardJournalNamed(t, control, "wizard-pending-op-2")
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
		t.Fatalf("install recover both: %+v %v", installed, err)
	}
	ops := filepath.Join(filepath.Dir(control), "uap", "state", "operations")
	for _, opID := range []string{"wizard-pending-op", "wizard-pending-op-2"} {
		if _, err := os.Lstat(filepath.Join(ops, opID+".json")); !os.IsNotExist(err) {
			t.Fatalf("install left pending journal %s: %v", opID, err)
		}
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.Outcome == "recovery_required" || view.Reason == "recovery_required" {
		t.Fatalf("inspect after recover both: %+v %v", view, err)
	}
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("install after recover both missed binding: %+v", view.Targets)
	}
}

func TestWizardInstallRecoversKernelThenUAP(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	hook := plantWizardKernelJournal(t, ctx, control, runtime)
	plantWizardJournal(t, control)
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
		t.Fatalf("both journals install: %+v %v", installed, err)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("kernel journal survived recover")
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); !os.IsNotExist(err) {
		t.Fatalf("UAP journal survived recover: %v", err)
	}
	got, err := os.ReadFile(hook)
	if err != nil || string(got) != "new" {
		t.Fatalf("kernel recover did not finish: %s %v", got, err)
	}
}

func TestWizardUninstallRecoversPendingJournal(t *testing.T) {
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
	plantWizardJournal(t, control)
	req.Action = ActionUninstall
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall recover: %+v %v", removed, err)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); !os.IsNotExist(err) {
		t.Fatalf("uninstall left pending journal: %v", err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.Outcome == "recovery_required" || view.Reason == "recovery_required" {
		t.Fatalf("inspect after uninstall recover: %+v %v", view, err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("portable binding survived uninstall recover: %+v", view.Targets)
		}
	}
}

func TestWizardUninstallRecoversKernelThenUAP(t *testing.T) {
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
	hook := plantWizardKernelJournal(t, ctx, control, runtime)
	plantWizardJournal(t, control)
	req.Action = ActionUninstall
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("both journals uninstall: %+v %v", removed, err)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("kernel journal survived uninstall recover")
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); !os.IsNotExist(err) {
		t.Fatalf("UAP journal survived uninstall recover: %v", err)
	}
	got, err := os.ReadFile(hook)
	if err != nil || string(got) != "new" {
		t.Fatalf("kernel recover did not finish: %s %v", got, err)
	}
}

func TestWizardHooksOnlyInstallRecoversKernelJournal(t *testing.T) {
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
	hook := plantWizardKernelJournal(t, ctx, control, runtime)
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: boolPtr(true), AgentNotify: &off,
		PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: home,
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("hooks recover: %+v %v", installed, err)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("hooks-only left kernel journal")
	}
	got, err := os.ReadFile(hook)
	if err != nil || string(got) != "new" {
		t.Fatalf("hooks-only kernel recover did not finish: %s %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("hooks-only recover opened UAP state")
	}
}

func TestWizardHooksOnlyInstallLeavesUAPJournal(t *testing.T) {
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
	plantWizardJournal(t, control)
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: boolPtr(true), AgentNotify: &off,
		PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: home,
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("hooks with UAP journal: %+v %v", installed, err)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); err != nil {
		t.Fatalf("hooks-only recovered UAP journal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("hooks-only install opened UAP state")
	}
}

func TestWizardHooksOnlyUninstallRecoversKernelJournal(t *testing.T) {
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
	hook := plantWizardKernelJournal(t, ctx, control, runtime)
	req.Action = ActionUninstall
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("hooks uninstall recover: %+v %v", removed, err)
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("hooks uninstall left kernel journal")
	}
	got, err := os.ReadFile(hook)
	if err != nil || string(got) != "new" {
		t.Fatalf("hooks uninstall kernel recover did not finish: %s %v", got, err)
	}
	hooks := filepath.Join(home, "hooks.json")
	data, err := os.ReadFile(hooks)
	if err == nil && strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks survived uninstall: %s", data)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("hooks uninstall opened UAP state")
	}
}

func TestPlanUninstallListsCodexExternalPrerequisite(t *testing.T) {
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
	before, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUninstall
	req.Yes = false
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("uninstall plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "required=external-uninstall") || !strings.Contains(plan.Text, "required-external-uninstall=codex") {
		t.Fatalf("missing Codex prerequisite: %s", plan.Text)
	}
	found := false
	for _, next := range plan.Result.NextActions {
		if next.Kind == "external-uninstall" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan omitted external-uninstall action: %+v", plan.Result.NextActions)
	}
	after, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || after.Ledger.Generation != before.Ledger.Generation || after.Ledger.PendingMutation != nil {
		t.Fatalf("uninstall plan mutated ledger: %+v %v", after.Ledger, err)
	}
	req.ExternalUninstalled = true
	attested, err := Plan(ctx, req)
	if err != nil || !attested.Ready {
		t.Fatalf("attested plan: %+v %v", attested, err)
	}
	if strings.Contains(attested.Text, "required=external-uninstall") || strings.Contains(attested.Text, "required-external-uninstall=") {
		t.Fatalf("attested plan still required external uninstall: %s", attested.Text)
	}
}

func TestPlanUninstallShowsPendingRecoveryWithoutMutating(t *testing.T) {
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
	before, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	plantWizardJournal(t, control)
	req.Action = ActionUninstall
	req.Yes = false
	req.PackageFetcher = func(context.Context, string) ([]byte, error) {
		t.Fatal("uninstall plan fetched a package during pending recovery")
		return nil, nil
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("uninstall plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "recovery-pending=wizard-pending-op") {
		t.Fatalf("missing uninstall recovery text: %s", plan.Text)
	}
	found := false
	for _, next := range plan.Result.NextActions {
		if next.Kind == "recover" && strings.Contains(next.Reason, "wizard-pending-op") {
			found = true
		}
	}
	if !found {
		t.Fatalf("uninstall plan recover action: %+v", plan.Result.NextActions)
	}
	if _, err := os.Lstat(wizardPendingJournalPath(control)); err != nil {
		t.Fatalf("uninstall plan recovered journal: %v", err)
	}
	after, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || after.Ledger.Generation != before.Ledger.Generation || after.Ledger.PendingMutation != nil {
		t.Fatalf("uninstall plan mutated ledger: %+v %v", after.Ledger, err)
	}
}

func plantWizardJournal(t *testing.T, controlRoot string) {
	t.Helper()
	plantWizardJournalNamed(t, controlRoot, "wizard-pending-op")
}

func plantWizardJournalNamed(t *testing.T, controlRoot, opID string) {
	t.Helper()
	owned := filepath.Join(filepath.Dir(controlRoot), "uap", "managed")
	staging := filepath.Join(owned, ".agentplugins-staging-"+opID)
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(opID))
	receipt := dirswap.Receipt{
		SchemaVersion: 3, Operation: dirswap.OperationSwap, OperationID: opID,
		ClientBindingID: "client-binding-1", Sequence: 1, OwnedBase: owned,
		ActivePath: filepath.Join(owned, "plugin"), StagingPath: staging,
		BackupPath: filepath.Join(owned, ".agentplugins-backup-"+hex.EncodeToString(sum[:8])),
		Phase:      dirswap.PhaseIntent,
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	ops := filepath.Join(filepath.Dir(controlRoot), "uap", "state", "operations")
	if err := os.MkdirAll(ops, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ops, opID+".json"), append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func wizardPendingJournalPath(controlRoot string) string {
	return filepath.Join(filepath.Dir(controlRoot), "uap", "state", "operations", "wizard-pending-op.json")
}

func plantWizardKernelJournal(t *testing.T, ctx context.Context, control, runtime string) string {
	t.Helper()
	hook := filepath.Join(runtime, "kernel-pending-hook")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime,
		Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: hook, Data: []byte("new"), Mode: 0700}},
		Fault: func(phase string) error {
			if phase == "transaction" {
				return errors.New("crash")
			}
			return nil
		},
	}); err == nil {
		t.Fatal("kernel fault not reached")
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); err != nil {
		t.Fatal("missing kernel journal")
	}
	return hook
}

func TestWizardEmptyAgentsRequiresSelection(t *testing.T) {
	control, _, _, _, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{Action: ActionInstall, Yes: true, ControlRoot: control})
	if err == nil || got.Outcome != "invalid" || got.Reason != "agents_required" || got.ExitCode() != 2 {
		t.Fatalf("empty: %+v %v", got, err)
	}
}

func TestWizardEmptyUnitsCancelsWithoutIntent(t *testing.T) {
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
	empty := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off, AgentNotify: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(empty.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Run(ctx, empty)
	if err != nil || got.Outcome != "cancelled" || got.Reason != "empty_units" || got.ExitCode() != 0 {
		t.Fatalf("empty install units: %+v %v", got, err)
	}
	if _, err := os.Lstat(portablesetup.IntentPath(control)); !os.IsNotExist(err) {
		t.Fatal("empty install units created intent")
	}
	empty.AgentNotify = nil
	installed, err := Run(ctx, empty)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	empty.Action = ActionUninstall
	empty.Hooks = &off
	empty.AgentNotify = &off
	empty.PackageRoot = ""
	held, err := Run(ctx, empty)
	if err != nil || held.Outcome != "cancelled" || held.Reason != "empty_units" {
		t.Fatalf("empty uninstall units: %+v %v", held, err)
	}
	if live := LiveNotifyClients(control, []string{"codex"}); strings.Join(live, ",") != "codex" {
		t.Fatalf("empty uninstall units mutated bindings: %v", live)
	}
	if _, err := os.Lstat(portablesetup.IntentPath(control)); !os.IsNotExist(err) {
		t.Fatal("empty uninstall units created intent")
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
	var phases []string
	req.Progress = func(phase string) {
		phases = append(phases, phase)
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if strings.Join(phases, ",") != "prepare,preflight,agent-notify,complete" {
		t.Fatalf("install phases: %v", phases)
	}
	if len(installed.Readiness) == 0 || installed.Readiness[0].Delivery != "not_verified" {
		t.Fatalf("delivery treated as proven: %+v", installed.Readiness)
	}
	kinds := map[string]bool{}
	for _, next := range installed.NextActions {
		kinds[next.Kind] = true
		if next.Kind == "test-notification" && (len(next.Command) == 0 || next.Command[0] != "notify" || next.Reason != "delivery_not_verified") {
			t.Fatalf("test action: %+v", next)
		}
		if next.Kind == "request-permission" && (len(next.Command) < 2 || next.Command[1] != "request-permission") {
			t.Fatalf("permission action: %+v", next)
		}
	}
	if !kinds["test-notification"] || !kinds["request-permission"] || !kinds["restart-client"] {
		t.Fatalf("install next actions: %+v", installed.NextActions)
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
			if target.Profile != codexConfig {
				t.Fatalf("inspect omitted live profile: %+v", target)
			}
			if target.TreeDigest == "" {
				t.Fatalf("inspect omitted tree digest: %+v", target)
			}
		}
	}
	if !found {
		t.Fatalf("inspect missed portable binding: %+v", view.Targets)
	}
	if view.InstallationID == "" || view.InstallationID != installed.InstallationID {
		t.Fatalf("live inspect omitted installation id: inspect=%s install=%s", view.InstallationID, installed.InstallationID)
	}
	if view.DataRetained {
		t.Fatalf("live inspect reported retained data: %+v", view)
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
	if !removed.DataRetained {
		t.Fatalf("last uninstall omitted data_retained: %+v", removed)
	}
	if removed.InstallationID == "" || removed.InstallationID != installed.InstallationID {
		t.Fatalf("last uninstall omitted installation id: uninstall=%s install=%s", removed.InstallationID, installed.InstallationID)
	}
	for _, next := range removed.NextActions {
		if next.Kind == "test-notification" || next.Kind == "request-permission" {
			t.Fatalf("uninstall offered setup action: %+v", removed.NextActions)
		}
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err = Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !view.DataRetained {
		t.Fatalf("inspect omitted data_retained: %+v", view)
	}
	if view.InstallationID == "" || view.InstallationID != installed.InstallationID {
		t.Fatalf("retained inspect omitted installation id: inspect=%s install=%s", view.InstallationID, installed.InstallationID)
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
	srv.Close()
	req.ReleaseDownloadRoot = ""
	statePath := filepath.Join(base, "uap", "state", "state-v2.json")
	target := liveTargetPath(t, statePath, "codex")
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("omitted host-acquired repair: %+v %v", got, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not use durable fetch source: %v", err)
	}
}

func TestWizardUpdateChangesLiveRevision(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := req
	blocked.Action = ActionInstall
	if got, err := Run(ctx, blocked); err == nil || got.Reason != "update_required" {
		t.Fatalf("install still upserts: %+v %v", got, err)
	}
	req.Action = ActionUpdate
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("update: %+v %v", got, err)
	}
	if got.InstallationID == "" || got.InstallationID != installed.InstallationID {
		t.Fatalf("update changed installation: install=%s update=%s", installed.InstallationID, got.InstallationID)
	}
	view, err := inspectUAPState(ctx, req)
	if err != nil || len(view.Installations) != 1 || view.Installations[0].TreeDigest == "" {
		t.Fatalf("inspect after update: %+v %v", view, err)
	}
}

func TestWizardUpdateSameDigestIsUnchanged(t *testing.T) {
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
	req.Action = ActionUpdate
	got, err := Run(ctx, req)
	if err != nil {
		t.Fatalf("same-digest update: %+v %v", got, err)
	}
	if got.Outcome != "completed" && got.Outcome != "unchanged" {
		t.Fatalf("same-digest update: %+v", got)
	}
	if got.InstallationID != installed.InstallationID {
		t.Fatalf("update changed installation: install=%s update=%s", installed.InstallationID, got.InstallationID)
	}
}

func TestWizardRepairRematerializesMissingTarget(t *testing.T) {
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
	target := liveTargetPath(t, statePath, "codex")
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("repair: %+v %v", got, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore target: %v", err)
	}
}

func TestWizardUpdateWithoutBindingIsNotInstalled(t *testing.T) {
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
		Action: ActionUpdate, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{ActionUpdate, ActionRepair} {
		req.Action = action
		got, err := Run(ctx, req)
		if err == nil || got.Reason != "not_installed" {
			t.Fatalf("%s without binding: %+v %v", action, got, err)
		}
		plan, err := Plan(ctx, req)
		if plan.Ready || plan.Result.Reason != "not_installed" {
			t.Fatalf("%s plan without binding: %+v %v", action, plan, err)
		}
	}
}

func TestWizardRepairDifferentDigestRequiresUpdate(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	got, err := Run(ctx, req)
	if err == nil || got.Reason != "update_required" {
		t.Fatalf("repair rewrote revision: %+v %v", got, err)
	}
}

func TestWizardRepairOmittedUnitsPreservesNotifyOnly(t *testing.T) {
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
	target := liveTargetPath(t, statePath, "codex")
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.Hooks = nil
	req.AgentNotify = nil
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("omitted-unit repair: %+v %v", got, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore target: %v", err)
	}
	for _, item := range got.Targets {
		if item.Unit == "hooks" && item.Outcome != "absent" && item.Outcome != "" {
			t.Fatalf("omitted repair added hooks: %+v", got.Targets)
		}
	}
}

func TestWizardRepairOmittedPackageUsesRecordedSource(t *testing.T) {
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
	target := liveTargetPath(t, statePath, "codex")
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.PackageRoot = ""
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("omitted package repair: %+v %v", got, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore target from recorded source: %v", err)
	}
}

func TestWizardRefusesSymlinkPackage(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	link := filepath.Join(filepath.Dir(control), "package-link")
	if err := os.Symlink(pkg, link); err != nil {
		t.Skip("symlink not permitted")
	}
	off := false
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: link, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: filepath.Join(filepath.Dir(control), "codex-profile"), ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	})
	if err == nil || got.Reason != "package_acquisition_failed" {
		t.Fatalf("symlink package: %+v %v", got, err)
	}
}

func TestWizardRefusesUnsafeZip(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	base := filepath.Dir(control)
	archive := filepath.Join(base, "escape.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	off := false
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: archive, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: filepath.Join(base, "codex-profile"), ClientExecutable: "/bin/true", Helper: "/bin/true",
		ScopeRoot: filepath.Join(base, "scope"),
	})
	if err == nil || got.Reason != "package_acquisition_failed" {
		t.Fatalf("unsafe zip: %+v %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(base, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("zip slip wrote outside dest")
	}
}

func TestWizardRepairOmittedZipUsesDurableSource(t *testing.T) {
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
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: archive, PackageSHA256: built.ArchiveSHA256,
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
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(base, "uap", "state", "state-v2.json")
	target := liveTargetPath(t, statePath, "codex")
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.PackageRoot = ""
	req.PackageSHA256 = ""
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("omitted zip repair: %+v %v", got, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not use durable acquired source: %v", err)
	}
}

func TestWizardRepairMissingDurableZipIsUnavailable(t *testing.T) {
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
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: archive, PackageSHA256: built.ArchiveSHA256,
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
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(base, "uap", "acquired-source")); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.PackageRoot = ""
	req.PackageSHA256 = ""
	got, err := Run(ctx, req)
	if err == nil || got.Reason != "recorded_package_unavailable" {
		t.Fatalf("missing durable zip: %+v %v", got, err)
	}
}

func TestWizardUpdateAndRepairRejectWithoutYes(t *testing.T) {
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
	before, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{ActionUpdate, ActionRepair, ActionUninstall} {
		blocked := req
		blocked.Action = action
		blocked.Yes = false
		got, err := Run(ctx, blocked)
		if err == nil || got.Outcome != "invalid" || got.Reason != "noninteractive_requires_yes" {
			t.Fatalf("%s without yes: %+v %v", action, got, err)
		}
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != before.Ledger.Generation {
		t.Fatalf("denied mutation changed ledger: gen=%d pending=%v err=%v", snap.Ledger.Generation, snap.Ledger.PendingMutation, err)
	}
}

func TestWizardRepairMissingRecordedPackageIsUnavailable(t *testing.T) {
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
	if err := os.RemoveAll(pkg); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.PackageRoot = ""
	got, err := Run(ctx, req)
	if err == nil || got.Reason != "recorded_package_unavailable" {
		t.Fatalf("missing recorded package: %+v %v", got, err)
	}
}

func TestWizardRepairExplicitNewHooksRequiresInstall(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	off, on := false, true
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
	req.Action = ActionRepair
	req.Hooks = &on
	got, err := Run(ctx, req)
	if err == nil || got.Reason != "install_required" {
		t.Fatalf("repair added hooks: %+v %v", got, err)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "install" {
		t.Fatalf("repair missing install next action: %+v", got.NextActions)
	}
	plan, err := Plan(ctx, req)
	if plan.Ready || plan.Result.Reason != "install_required" {
		t.Fatalf("repair plan added hooks: %+v %v", plan, err)
	}
}

func TestWizardRepairBothClientsMissingOneDoesNotMutateLive(t *testing.T) {
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
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	removed, err := Run(ctx, Request{
		Action: ActionUninstall, Agents: []string{"claude"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: req.ScopeRoot, ClaudeRunner: listingRunner{configRoot: claudeConfig},
	})
	if err != nil || (removed.Outcome != "completed" && removed.Outcome != "unchanged") {
		t.Fatalf("uninstall claude: %+v %v", removed, err)
	}
	req.Action = ActionRepair
	got, err := Run(ctx, req)
	if err == nil || got.Reason != "not_installed" {
		t.Fatalf("repair missing sibling: %+v %v", got, err)
	}
	if len(got.NextActions) != 2 || got.NextActions[0].Kind != "install" || got.NextActions[1].Kind != "repair" {
		t.Fatalf("repair missing sibling next actions: %+v", got.NextActions)
	}
	live := LiveNotifyClients(control, []string{"claude", "codex"})
	if len(live) != 1 || live[0] != "codex" {
		t.Fatalf("repair mutated live sibling: %v", live)
	}
}

func TestWizardUpdateOmittedUnitsPreservesNotifyOnly(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUpdate
	req.Hooks = nil
	req.AgentNotify = nil
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("omitted update plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "hooks=unchanged") || !strings.Contains(plan.Text, "codex-agent-notify=true") || strings.Contains(plan.Text, "hooks=on") {
		t.Fatalf("omitted update plan hid live units: %s", plan.Text)
	}
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("omitted-unit update: %+v %v", got, err)
	}
	for _, target := range got.Targets {
		if target.Unit == "hooks" && target.Outcome != "absent" && target.Outcome != "" {
			t.Fatalf("omitted update added hooks: %+v", got.Targets)
		}
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Unit == "hooks" && target.Outcome == "installed" {
			t.Fatalf("omitted update installed hooks: %+v", view.Targets)
		}
	}
}

func TestWizardUpdateOneClientKeepsSibling(t *testing.T) {
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
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUpdate
	req.Agents = []string{"codex"}
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	if got.InstallationID != installed.InstallationID {
		t.Fatalf("update changed installation: install=%s update=%s", installed.InstallationID, got.InstallationID)
	}
	live := LiveNotifyClients(control, []string{"claude", "codex"})
	if len(live) != 2 {
		t.Fatalf("sibling lost: %v", live)
	}
}

func TestWizardRepairMixedRevisionsRepairsMatchingPackage(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(filepath.Dir(control), "package-r1")
	copyPackage(t, pkg, r1)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: r1, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = pkg
	req.Action = ActionUpdate
	req.Agents = []string{"codex"}
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before := inspectedWizardBinding(t, ctx, control, "claude")
	codexBefore := inspectedWizardBinding(t, ctx, control, "codex")
	inspectReq := req
	inspectReq.Action = ActionInspect
	inspectReq.Agents = []string{"claude", "codex"}
	inspectReq.Yes = false
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect mixed: %+v %v", view, err)
	}
	claudeDigest := notifyTreeDigest(view, "claude")
	codexDigest := notifyTreeDigest(view, "codex")
	if claudeDigest == "" || claudeDigest == codexDigest {
		t.Fatalf("inspect collapsed mixed revisions: claude=%s codex=%s targets=%+v", claudeDigest, codexDigest, view.Targets)
	}
	req.Action = ActionRepair
	req.Agents = []string{"claude", "codex"}
	got, err = Run(ctx, req)
	if err != nil || (got.Outcome != "completed" && got.Outcome != "unchanged") {
		t.Fatalf("mixed repair with recorded r1: %+v %v", got, err)
	}
	saw := map[string]TargetResult{}
	for _, target := range got.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = target
		}
	}
	if saw["codex"].Outcome != "completed" || saw["claude"].Outcome != "completed" {
		t.Fatalf("mixed group repair: %+v", got.Targets)
	}
	if saw["claude"].TreeDigest == "" || saw["claude"].TreeDigest == saw["codex"].TreeDigest {
		t.Fatalf("mixed repair result collapsed digests: %+v", got.Targets)
	}
	if saw["claude"].TreeDigest != claudeDigest || saw["codex"].TreeDigest != codexDigest {
		t.Fatalf("mixed repair result drifted from inspect: result=%+v inspect claude=%s codex=%s", got.Targets, claudeDigest, codexDigest)
	}
	if live := LiveNotifyClients(control, []string{"claude", "codex"}); len(live) != 2 {
		t.Fatalf("mixed repair lost sibling: %v", live)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil {
		t.Fatalf("mixed repair left intent: %+v %v", snap.Ledger.PendingMutation, err)
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeAfter.BindingID != before.BindingID || claudeAfter.TargetPath != before.TargetPath {
		t.Fatalf("mixed repair rewrote claude: before=%+v after=%+v", before, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TargetPath != codexBefore.TargetPath {
		t.Fatalf("mixed repair rewrote codex: before=%+v after=%+v", codexBefore, codexAfter)
	}
	view, err = Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect after mixed repair: %+v %v", view, err)
	}
	if notifyTreeDigest(view, "claude") != claudeDigest {
		t.Fatalf("mixed repair rewrote claude digest: before=%s after=%s", claudeDigest, notifyTreeDigest(view, "claude"))
	}
	if notifyTreeDigest(view, "codex") != codexDigest {
		t.Fatalf("mixed repair rewrote codex digest: before=%s after=%s", codexDigest, notifyTreeDigest(view, "codex"))
	}
}

func TestWizardRepairMixedRevisionsMissingOlderPackage(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(filepath.Dir(control), "package-r1")
	copyPackage(t, pkg, r1)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: r1, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = pkg
	req.Action = ActionUpdate
	req.Agents = []string{"codex"}
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	if err := os.RemoveAll(r1); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.Agents = []string{"claude", "codex"}
	got, err = Run(ctx, req)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "exact_revision_required" {
		t.Fatalf("mixed repair without r1: %+v %v", got, err)
	}
	saw := map[string]TargetResult{}
	for _, target := range got.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = target
		}
	}
	if saw["codex"].Outcome != "completed" {
		t.Fatalf("codex r2 repair: %+v", saw["codex"])
	}
	if saw["claude"].Outcome != "incomplete" || saw["claude"].Reason != "exact_revision_required" {
		t.Fatalf("claude r1 mismatch: %+v", saw["claude"])
	}
	if saw["codex"].TreeDigest == "" || saw["claude"].TreeDigest == "" || saw["codex"].TreeDigest == saw["claude"].TreeDigest {
		t.Fatalf("mixed missing-package result collapsed digests: %+v", got.Targets)
	}
	if live := LiveNotifyClients(control, []string{"claude", "codex"}); len(live) != 2 {
		t.Fatalf("mixed repair lost sibling: %v", live)
	}
}

func TestWizardRepairMixedRevisionsRematerializesDeletedSibling(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(filepath.Dir(control), "package-r1")
	copyPackage(t, pkg, r1)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: r1, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = pkg
	req.Action = ActionUpdate
	req.Agents = []string{"codex"}
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	claudeBefore := inspectedWizardBinding(t, ctx, control, "claude")
	codexBefore := inspectedWizardBinding(t, ctx, control, "codex")
	inspectReq := req
	inspectReq.Action = ActionInspect
	inspectReq.Agents = []string{"claude", "codex"}
	inspectReq.Yes = false
	beforeView, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect mixed: %+v %v", beforeView, err)
	}
	claudeDigest := notifyTreeDigest(beforeView, "claude")
	codexDigest := notifyTreeDigest(beforeView, "codex")
	if claudeDigest == "" || claudeDigest == codexDigest {
		t.Fatalf("inspect collapsed mixed revisions: claude=%s codex=%s targets=%+v", claudeDigest, codexDigest, beforeView.Targets)
	}
	if err := os.RemoveAll(claudeBefore.TargetPath); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	req.Agents = []string{"claude", "codex"}
	got, err = Run(ctx, req)
	if err != nil || (got.Outcome != "completed" && got.Outcome != "unchanged") {
		t.Fatalf("mixed rematerialize: %+v %v", got, err)
	}
	if notifyTreeDigest(got, "claude") != claudeDigest || notifyTreeDigest(got, "codex") != codexDigest {
		t.Fatalf("mixed rematerialize result collapsed digests: %+v inspect claude=%s codex=%s", got.Targets, claudeDigest, codexDigest)
	}
	if _, err := os.Stat(claudeBefore.TargetPath); err != nil {
		t.Fatalf("mixed rematerialize did not restore claude: %v", err)
	}
	if live := LiveNotifyClients(control, []string{"claude", "codex"}); len(live) != 2 {
		t.Fatalf("mixed rematerialize lost sibling: %v", live)
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath {
		t.Fatalf("mixed rematerialize rewrote claude: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TargetPath != codexBefore.TargetPath {
		t.Fatalf("mixed rematerialize rewrote codex sibling: before=%+v after=%+v", codexBefore, codexAfter)
	}
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect after mixed rematerialize: %+v %v", view, err)
	}
	if notifyTreeDigest(view, "claude") != claudeDigest {
		t.Fatalf("claude rematerialized from newer package: before=%s after=%s", claudeDigest, notifyTreeDigest(view, "claude"))
	}
	if notifyTreeDigest(view, "codex") != codexDigest {
		t.Fatalf("codex sibling revision lost: before=%s after=%s", codexDigest, notifyTreeDigest(view, "codex"))
	}
}

func TestCanGroupNotify(t *testing.T) {
	mat := portablesetup.Materializer{}
	id := portablesetup.Identity{InstallationID: "00000000-0000-4000-8000-000000000001"}
	both := []portable.Integration{portable.Claude, portable.Codex}
	if !canGroupNotify(mat, id, Request{Action: ActionInstall}, both) {
		t.Fatal("unbound install should group")
	}
	if canGroupNotify(mat, id, Request{Action: ActionUpdate}, both) {
		t.Fatal("unbound update should not group")
	}
	if canGroupNotify(mat, id, Request{Action: ActionRepair}, both) {
		t.Fatal("unbound repair should not group")
	}
	if canGroupNotify(mat, id, Request{Action: ActionUninstall}, both) {
		t.Fatal("uninstall should not group")
	}
	if canGroupNotify(mat, id, Request{Action: ActionInstall}, []portable.Integration{portable.Codex}) {
		t.Fatal("single client should not group")
	}
	if !canGroupRemove(mat, id, Request{Action: ActionUninstall, ExternalUninstalled: true}, both) {
		t.Fatal("attested uninstall should group")
	}
	if !canGroupRemove(mat, id, Request{Action: ActionUninstall}, both) {
		t.Fatal("absent Codex should still group with Claude")
	}
	if canGroupRemove(mat, id, Request{Action: ActionInstall}, both) {
		t.Fatal("install should not group-remove")
	}
}

func TestWizardUpdateBothLiveClients(t *testing.T) {
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
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install both: %+v %v", installed, err)
	}
	var sawClaude, sawCodex bool
	for _, target := range installed.Targets {
		if target.Unit != "agent-notify" {
			continue
		}
		if target.Outcome != "completed" || target.Reason == "" {
			t.Fatalf("install target: %+v", target)
		}
		switch target.Client {
		case "claude":
			sawClaude = true
		case "codex":
			sawCodex = true
		}
	}
	if !sawClaude || !sawCodex {
		t.Fatalf("install omitted a client: %+v", installed.Targets)
	}
	repeat := req
	again, err := Run(ctx, repeat)
	if err != nil || again.Outcome != "completed" {
		t.Fatalf("repeat install both: %+v %v", again, err)
	}
	if again.InstallationID != installed.InstallationID {
		t.Fatalf("repeat install changed installation: install=%s repeat=%s", installed.InstallationID, again.InstallationID)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUpdate
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("update both: %+v %v", got, err)
	}
	if got.InstallationID != installed.InstallationID {
		t.Fatalf("update changed installation: install=%s update=%s", installed.InstallationID, got.InstallationID)
	}
	live := LiveNotifyClients(control, []string{"claude", "codex"})
	if len(live) != 2 {
		t.Fatalf("live after group update: %v", live)
	}
	req.Action = ActionRepair
	repaired, err := Run(ctx, req)
	if err != nil || repaired.Outcome != "completed" {
		t.Fatalf("repair both: %+v %v", repaired, err)
	}
	if repaired.InstallationID != installed.InstallationID {
		t.Fatalf("repair changed installation: install=%s repair=%s", installed.InstallationID, repaired.InstallationID)
	}
}

func TestWizardInspectAfterGroupInstallReportsBothWithoutMutating(t *testing.T) {
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
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install both: %+v %v", installed, err)
	}
	statePath := filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.Outcome != "completed" {
		t.Fatalf("inspect both: %+v %v", view, err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inspect mutated UAP state")
	}
	again, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if again.Ledger.Generation != snap.Ledger.Generation {
		t.Fatalf("inspect mutated ledger: before=%d after=%d", snap.Ledger.Generation, again.Ledger.Generation)
	}
	saw := map[string]TargetResult{}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = target
		}
	}
	if saw["claude"].Outcome != "installed" || saw["claude"].Profile != claudeConfig || saw["claude"].TreeDigest == "" {
		t.Fatalf("claude inspect: %+v", saw["claude"])
	}
	if saw["codex"].Outcome != "installed" || saw["codex"].Profile != codexConfig || saw["codex"].TreeDigest == "" {
		t.Fatalf("codex inspect: %+v", saw["codex"])
	}
	if live := LiveNotifyClients(control, []string{"claude", "codex"}); len(live) != 2 {
		t.Fatalf("live after inspect: %v", live)
	}
}

func TestWizardUpdateOneClientMissingSiblingProfile(t *testing.T) {
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
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install both: %+v %v", installed, err)
	}
	removeLiveProfiles(t, control)
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUpdate
	req.Agents = []string{"codex"}
	got, err := Run(ctx, req)
	if err == nil || got.Reason != "sibling_compatibility_unavailable" {
		t.Fatalf("missing sibling profile: %+v %v", got, err)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "claude,codex" {
		t.Fatalf("retry: %+v", got.NextActions)
	}
	if live := LiveNotifyClients(control, []string{"claude", "codex"}); len(live) != 2 {
		t.Fatalf("refused update dropped a client: %v", live)
	}
}

func removeLiveProfiles(t *testing.T, controlRoot string) {
	t.Helper()
	pluginData := filepath.Join(filepath.Dir(controlRoot), "uap", "plugin-data")
	removed := 0
	if err := filepath.Walk(pluginData, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Name() != uapinstaller.LiveProfilesFile {
			return err
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return rmErr
		}
		removed++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("live-profiles.json was not written")
	}
}

func liveTargetPath(t *testing.T, statePath, clientID string) string {
	t.Helper()
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Dir(statePath)})
	if err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, installation := range view.Installations {
		for _, binding := range installation.Bindings {
			if binding.ClientID == clientID && binding.TargetPath != "" {
				return binding.TargetPath
			}
		}
	}
	t.Fatalf("no live target for %s in %s", clientID, statePath)
	return ""
}

func inspectedWizardBinding(t *testing.T, ctx context.Context, control, clientID string) uapinstaller.InspectedBinding {
	t.Helper()
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(filepath.Dir(control), "uap", "state")})
	if err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, installation := range view.Installations {
		for _, binding := range installation.Bindings {
			if binding.ClientID == clientID && binding.TargetPath != "" {
				return binding
			}
		}
	}
	t.Fatalf("no live binding for %s under %s", clientID, control)
	return uapinstaller.InspectedBinding{}
}

func notifyTreeDigest(got Result, client string) string {
	for _, target := range got.Targets {
		if target.Unit == "agent-notify" && target.Client == client {
			return target.TreeDigest
		}
	}
	return ""
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
	var phases []string
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		Hooks: boolPtr(true), AgentNotify: &off,
		PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: home,
		Progress: func(phase string) {
			phases = append(phases, phase)
		},
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("hooks install: %+v %v", installed, err)
	}
	if strings.Join(phases, ",") != "preflight,hooks,complete" {
		t.Fatalf("install phases: %v", phases)
	}
	hooks := filepath.Join(home, "hooks.json")
	data, err := os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks.json: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("hooks-only install opened UAP state")
	}
	if live := LiveSetupClients(req, []string{"codex", "claude"}); strings.Join(live, ",") != "codex" {
		t.Fatalf("hooks-only live clients: %v", live)
	}
	if live := LiveNotifyClients(control, []string{"codex"}); len(live) != 0 {
		t.Fatalf("hooks-only reported notify live: %v", live)
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
	phases = nil
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("hooks uninstall: %+v %v", removed, err)
	}
	if strings.Join(phases, ",") != "preflight,hooks,complete" {
		t.Fatalf("uninstall phases: %v", phases)
	}
	data, err = os.ReadFile(hooks)
	if err == nil && strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks survived uninstall: %s", data)
	}
}

func TestWizardNotifyInstallReportsProgress(t *testing.T) {
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
	var phases []string
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
		Progress:  func(phase string) { phases = append(phases, phase) },
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if strings.Join(phases, ",") != "prepare,preflight,agent-notify,complete" {
		t.Fatalf("install phases: %v", phases)
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
	units := LiveClientUnits(req, []string{"claude", "codex"})
	if len(units) != 2 || units[0].Client != "claude" || units[0].Notify || units[0].Hooks || units[1].Client != "codex" || !units[1].Notify || units[1].Hooks {
		t.Fatalf("live units: %+v", units)
	}
	if len(view.Readiness) == 0 {
		t.Fatal("inspect omitted readiness")
	}
	for _, fact := range view.Readiness {
		if fact.Permission != "unsupported" || fact.Delivery != "not_verified" {
			t.Fatalf("readiness mixed download with delivery: %+v", fact)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUpdate
	req.Yes = true
	req.Hooks = nil
	req.AgentNotify = nil
	req.ClaudeAgentNotify = nil
	req.CodexAgentNotify = nil
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("omitted mixed update: %+v %v", got, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err = Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	claudeMCP, codexMCP = "", ""
	var hooksInstalled bool
	for _, target := range view.Targets {
		switch {
		case target.Unit == "agent-notify" && target.Client == "claude":
			claudeMCP = target.Outcome
		case target.Unit == "agent-notify" && target.Client == "codex":
			codexMCP = target.Outcome
		case target.Unit == "hooks" && target.Outcome == "installed":
			hooksInstalled = true
		}
	}
	if claudeMCP == "installed" || codexMCP != "installed" || hooksInstalled {
		t.Fatalf("omitted mixed update changed units: %+v", view.Targets)
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
	mismatch.Yes = false
	blockedPlan, err := Plan(ctx, mismatch)
	if blockedPlan.Ready || blockedPlan.Result.Reason != "update_required" {
		t.Fatalf("mismatch plan: %+v %v", blockedPlan, err)
	}
	if !strings.Contains(blockedPlan.Text, "required-update=claude") {
		t.Fatalf("mismatch plan omitted required-update: %s", blockedPlan.Text)
	}
	if !strings.Contains(blockedPlan.Text, "phases=1-update:claude;2-add:codex") {
		t.Fatalf("mismatch plan omitted two phases: %s", blockedPlan.Text)
	}
	if strings.Contains(blockedPlan.Text, "data_retained=true") || strings.Contains(blockedPlan.Text, "metadata-only") {
		t.Fatalf("live mismatch plan looked retained: %s", blockedPlan.Text)
	}
	samePlan, err := Plan(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Hooks: &off, AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    base.ScopeRoot,
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	})
	if err != nil || !samePlan.Ready {
		t.Fatalf("same-revision add plan: %+v %v", samePlan, err)
	}
	if strings.Contains(samePlan.Text, "required-update=") {
		t.Fatalf("same-revision add showed required-update: %s", samePlan.Text)
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

func TestWizardTwoPhaseUpdateThenAdd(t *testing.T) {
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
	updateReq := base
	updateReq.Action = ActionUpdate
	updateReq.Agents = blocked.NextActions[0].Agents
	updateReq.PackageRoot = otherPkg
	updated, err := Run(ctx, updateReq)
	if err != nil || updated.Outcome != "completed" {
		t.Fatalf("phase 1 update: %+v %v", updated, err)
	}
	addReq := base
	addReq.Agents = blocked.NextActions[1].Agents
	addReq.PackageRoot = otherPkg
	added, err := Run(ctx, addReq)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("phase 2 add: %+v %v", added, err)
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
		t.Fatalf("two-phase inspect: %+v", view.Targets)
	}
}

func TestWizardInstallBothMismatchedShowsTwoPhase(t *testing.T) {
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
	claudeBefore := inspectedWizardBinding(t, ctx, control, "claude")
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	both := base
	both.Agents = []string{"claude", "codex"}
	blocked, err := Run(ctx, both)
	if err == nil || blocked.Outcome != "incomplete" || blocked.Reason != "update_required" {
		t.Fatalf("install both mismatched: %+v %v", blocked, err)
	}
	if len(blocked.NextActions) != 2 || blocked.NextActions[0].Kind != "update" || blocked.NextActions[1].Kind != "install" {
		t.Fatalf("both mismatched next: %+v", blocked.NextActions)
	}
	if strings.Join(blocked.NextActions[0].Agents, ",") != "claude" || strings.Join(blocked.NextActions[1].Agents, ",") != "codex" {
		t.Fatalf("both mismatched agents: %+v", blocked.NextActions)
	}
	if cmd := strings.Join(blocked.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents claude") {
		t.Fatalf("both mismatched update argv: %v", blocked.NextActions[0].Command)
	}
	if cmd := strings.Join(blocked.NextActions[1].Command, " "); !strings.Contains(cmd, "--action install") || !strings.Contains(cmd, "--agents codex") {
		t.Fatalf("both mismatched add argv: %v", blocked.NextActions[1].Command)
	}
	both.Yes = false
	plan, err := Plan(ctx, both)
	if plan.Ready || plan.Result.Reason != "update_required" {
		t.Fatalf("both mismatched plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "required-update=claude") || !strings.Contains(plan.Text, "2-add:codex") {
		t.Fatalf("both mismatched plan omitted two phases: %s", plan.Text)
	}
	var promptOut strings.Builder
	ok, err := (&LinePrompt{In: strings.NewReader("n\n"), Out: &promptOut}).Confirm(ctx, plan.Text)
	if err != nil || ok {
		t.Fatalf("both mismatched confirm: ok=%t err=%v text=%s", ok, err, promptOut.String())
	}
	if !strings.Contains(promptOut.String(), "required-update=claude") || !strings.Contains(promptOut.String(), "2-add:codex") {
		t.Fatalf("tty omitted both mismatched phases: %s", promptOut.String())
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TreeDigest != claudeBefore.TreeDigest {
		t.Fatalf("mismatched both rewrote claude: %+v/%+v", claudeBefore, claudeAfter)
	}
	updateReq := base
	updateReq.Action = ActionUpdate
	updateReq.Agents = blocked.NextActions[0].Agents
	updated, err := Run(ctx, updateReq)
	if err != nil || updated.Outcome != "completed" {
		t.Fatalf("both mismatched update: %+v %v", updated, err)
	}
	addReq := base
	addReq.Agents = blocked.NextActions[1].Agents
	added, err := Run(ctx, addReq)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("both mismatched add: %+v %v", added, err)
	}
	inspectReq := base
	inspectReq.Action = ActionInspect
	inspectReq.Yes = false
	inspectReq.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatal(err)
	}
	claudeDone := inspectedWizardBinding(t, ctx, control, "claude")
	codexDone := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeDone.BindingID != claudeBefore.BindingID {
		t.Fatalf("both mismatched phases rewrote claude: %+v/%+v", claudeBefore, claudeDone)
	}
	if claudeDone.TreeDigest == "" || claudeDone.TreeDigest == claudeBefore.TreeDigest || claudeDone.TreeDigest != codexDone.TreeDigest {
		t.Fatalf("both mismatched did not converge: before=%s claude=%s codex=%s view=%+v", claudeBefore.TreeDigest, claudeDone.TreeDigest, codexDone.TreeDigest, view.Targets)
	}
}

func TestWizardInstallBothMismatchedCodexLiveShowsTwoPhase(t *testing.T) {
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
	codexReq := base
	codexReq.Agents = []string{"codex"}
	installed, err := Run(ctx, codexReq)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("codex: %+v %v", installed, err)
	}
	codexBefore := inspectedWizardBinding(t, ctx, control, "codex")
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	both := base
	both.Agents = []string{"claude", "codex"}
	blocked, err := Run(ctx, both)
	if err == nil || blocked.Outcome != "incomplete" || blocked.Reason != "update_required" {
		t.Fatalf("codex-live both mismatched: %+v %v", blocked, err)
	}
	if len(blocked.NextActions) != 2 || blocked.NextActions[0].Kind != "update" || blocked.NextActions[1].Kind != "install" {
		t.Fatalf("codex-live both next: %+v", blocked.NextActions)
	}
	if strings.Join(blocked.NextActions[0].Agents, ",") != "codex" || strings.Join(blocked.NextActions[1].Agents, ",") != "claude" {
		t.Fatalf("codex-live both agents: %+v", blocked.NextActions)
	}
	if cmd := strings.Join(blocked.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents codex") {
		t.Fatalf("codex-live both update argv: %v", blocked.NextActions[0].Command)
	}
	if cmd := strings.Join(blocked.NextActions[1].Command, " "); !strings.Contains(cmd, "--action install") || !strings.Contains(cmd, "--agents claude") {
		t.Fatalf("codex-live both add argv: %v", blocked.NextActions[1].Command)
	}
	both.Yes = false
	plan, err := Plan(ctx, both)
	if plan.Ready || plan.Result.Reason != "update_required" {
		t.Fatalf("codex-live both plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "required-update=codex") || !strings.Contains(plan.Text, "2-add:claude") {
		t.Fatalf("codex-live both plan omitted two phases: %s", plan.Text)
	}
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TreeDigest != codexBefore.TreeDigest {
		t.Fatalf("codex-live both rewrote codex: %+v/%+v", codexBefore, codexAfter)
	}
	updateReq := base
	updateReq.Action = ActionUpdate
	updateReq.Agents = blocked.NextActions[0].Agents
	updated, err := Run(ctx, updateReq)
	if err != nil || updated.Outcome != "completed" {
		t.Fatalf("codex-live both update: %+v %v", updated, err)
	}
	addReq := base
	addReq.Agents = blocked.NextActions[1].Agents
	added, err := Run(ctx, addReq)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("codex-live both add: %+v %v", added, err)
	}
	claudeDone := inspectedWizardBinding(t, ctx, control, "claude")
	codexDone := inspectedWizardBinding(t, ctx, control, "codex")
	if codexDone.BindingID != codexBefore.BindingID {
		t.Fatalf("codex-live phases rewrote codex: %+v/%+v", codexBefore, codexDone)
	}
	if claudeDone.TreeDigest == "" || claudeDone.TreeDigest == codexBefore.TreeDigest || claudeDone.TreeDigest != codexDone.TreeDigest {
		t.Fatalf("codex-live both did not converge: before=%s claude=%s codex=%s", codexBefore.TreeDigest, claudeDone.TreeDigest, codexDone.TreeDigest)
	}
}

func TestWizardInstallBothWhenOneLiveMatchingAddsSibling(t *testing.T) {
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
	claudeBefore := inspectedWizardBinding(t, ctx, control, "claude")
	both := base
	both.Agents = []string{"claude", "codex"}
	both.Yes = false
	plan, err := Plan(ctx, both)
	if err != nil || !plan.Ready || strings.Contains(plan.Text, "required-update=") || strings.Contains(plan.Text, "2-add:") {
		t.Fatalf("matching both plan: %+v %v", plan, err)
	}
	both.Yes = true
	added, err := Run(ctx, both)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("matching both install: %+v %v", added, err)
	}
	inspectReq := base
	inspectReq.Action = ActionInspect
	inspectReq.Yes = false
	inspectReq.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatal(err)
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID {
		t.Fatalf("matching both rewrote claude: %+v/%+v", claudeBefore, claudeAfter)
	}
	if claudeAfter.TreeDigest == "" || claudeAfter.TreeDigest != codexAfter.TreeDigest {
		t.Fatalf("matching both digests: claude=%s codex=%s view=%+v", claudeAfter.TreeDigest, codexAfter.TreeDigest, view.Targets)
	}
}

func TestWizardInstallBothWhenCodexLiveMatchingAddsClaude(t *testing.T) {
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
	codexReq := base
	codexReq.Agents = []string{"codex"}
	installed, err := Run(ctx, codexReq)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("codex: %+v %v", installed, err)
	}
	codexBefore := inspectedWizardBinding(t, ctx, control, "codex")
	both := base
	both.Agents = []string{"claude", "codex"}
	both.Yes = false
	plan, err := Plan(ctx, both)
	if err != nil || !plan.Ready || strings.Contains(plan.Text, "required-update=") || strings.Contains(plan.Text, "2-add:") {
		t.Fatalf("codex-live matching both plan: %+v %v", plan, err)
	}
	both.Yes = true
	added, err := Run(ctx, both)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("codex-live matching both install: %+v %v", added, err)
	}
	inspectReq := base
	inspectReq.Action = ActionInspect
	inspectReq.Yes = false
	inspectReq.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatal(err)
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if codexAfter.BindingID != codexBefore.BindingID {
		t.Fatalf("codex-live matching both rewrote codex: %+v/%+v", codexBefore, codexAfter)
	}
	if claudeAfter.TreeDigest == "" || claudeAfter.TreeDigest != codexAfter.TreeDigest {
		t.Fatalf("codex-live matching both digests: claude=%s codex=%s view=%+v", claudeAfter.TreeDigest, codexAfter.TreeDigest, view.Targets)
	}
}

func TestWizardInstallMixedLiveDoesNotReplaceOlderSibling(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(filepath.Dir(control), "package-r1")
	copyPackage(t, pkg, r1)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: r1, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = pkg
	req.Action = ActionUpdate
	req.Agents = []string{"claude"}
	updated, err := Run(ctx, req)
	if err != nil || updated.Outcome != "completed" {
		t.Fatalf("claude update: %+v %v", updated, err)
	}
	inspectReq := req
	inspectReq.Action = ActionInspect
	inspectReq.Agents = []string{"claude", "codex"}
	inspectReq.Yes = false
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect mixed: %+v %v", view, err)
	}
	claudeDigest := notifyTreeDigest(view, "claude")
	codexDigest := notifyTreeDigest(view, "codex")
	if claudeDigest == "" || claudeDigest == codexDigest {
		t.Fatalf("inspect collapsed mixed revisions: claude=%s codex=%s", claudeDigest, codexDigest)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := materializer(req, snap, runtime)
	if err != nil {
		t.Fatal(err)
	}
	id := portablesetup.Identity{InstallationID: installed.InstallationID}
	if id.InstallationID == "" {
		id.InstallationID = installationIDFromState(t, filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json"))
	}
	groupAgents := []portable.Integration{portable.Claude, portable.Codex}
	if canGroupNotify(mat, id, Request{Action: ActionInstall}, groupAgents) {
		t.Fatal("mixed live install grouped")
	}
	claudeBefore := inspectedWizardBinding(t, ctx, control, "claude")
	codexBefore := inspectedWizardBinding(t, ctx, control, "codex")
	match := req
	match.Action = ActionInstall
	match.Agents = []string{"claude"}
	match.Yes = false
	matchPlan, err := Plan(ctx, match)
	if err != nil || !matchPlan.Ready || strings.Contains(matchPlan.Text, "required-update=") || strings.Contains(matchPlan.Text, "2-add:") {
		t.Fatalf("matching live sibling plan: %+v %v", matchPlan, err)
	}
	match.Yes = true
	matched, err := Run(ctx, match)
	if err != nil || (matched.Outcome != "completed" && matched.Outcome != "unchanged") {
		t.Fatalf("matching live sibling install: %+v %v", matched, err)
	}
	view, err = Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect after matching sibling: %+v %v", view, err)
	}
	if notifyTreeDigest(view, "claude") != claudeDigest || notifyTreeDigest(view, "codex") != codexDigest {
		t.Fatalf("matching sibling rewrote mixed digests: claude=%s/%s codex=%s/%s", claudeDigest, notifyTreeDigest(view, "claude"), codexDigest, notifyTreeDigest(view, "codex"))
	}
	claudeMatched := inspectedWizardBinding(t, ctx, control, "claude")
	codexMatched := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeMatched.BindingID != claudeBefore.BindingID || codexMatched.BindingID != codexBefore.BindingID {
		t.Fatalf("matching sibling rewrote bindings: claude=%+v/%+v codex=%+v/%+v", claudeBefore, claudeMatched, codexBefore, codexMatched)
	}
	if canGroupNotify(mat, id, Request{Action: ActionInstall}, groupAgents) {
		t.Fatal("matching sibling grouped mixed live install")
	}
	req.Action = ActionInstall
	req.Agents = []string{"codex"}
	req.Yes = true
	blocked, err := Run(ctx, req)
	if err == nil || blocked.Outcome != "incomplete" || blocked.Reason != "update_required" {
		t.Fatalf("install older sibling: %+v %v", blocked, err)
	}
	if len(blocked.NextActions) != 1 || blocked.NextActions[0].Kind != "update" || strings.Join(blocked.NextActions[0].Agents, ",") != "codex" {
		t.Fatalf("older sibling next: %+v", blocked.NextActions)
	}
	if cmd := strings.Join(blocked.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents codex") || strings.Contains(cmd, "2-add") {
		t.Fatalf("older sibling retry: %v", blocked.NextActions[0].Command)
	}
	both := req
	both.Agents = []string{"claude", "codex"}
	blockedBoth, err := Run(ctx, both)
	if err == nil || blockedBoth.Outcome != "incomplete" || blockedBoth.Reason != "update_required" {
		t.Fatalf("install both mixed: %+v %v", blockedBoth, err)
	}
	if len(blockedBoth.NextActions) != 1 || blockedBoth.NextActions[0].Kind != "update" || strings.Join(blockedBoth.NextActions[0].Agents, ",") != "codex" {
		t.Fatalf("mixed both next: %+v", blockedBoth.NextActions)
	}
	if cmd := strings.Join(blockedBoth.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents codex") || strings.Contains(cmd, "--agents claude,codex") {
		t.Fatalf("mixed both retry: %v", blockedBoth.NextActions[0].Command)
	}
	both.Yes = false
	plan, err := Plan(ctx, both)
	if plan.Ready || plan.Result.Reason != "update_required" {
		t.Fatalf("mixed install plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "required-update=codex") || strings.Contains(plan.Text, "2-add:") {
		t.Fatalf("mixed install plan omitted older sibling: %s", plan.Text)
	}
	if strings.Contains(plan.Text, "data_retained=true") || strings.Contains(plan.Text, "metadata-only") {
		t.Fatalf("live mixed install plan looked retained: %s", plan.Text)
	}
	var promptOut strings.Builder
	ok, err := (&LinePrompt{In: strings.NewReader("n\n"), Out: &promptOut}).Confirm(ctx, plan.Text)
	if err != nil || ok {
		t.Fatalf("mixed confirm: ok=%t err=%v text=%s", ok, err, promptOut.String())
	}
	if !strings.Contains(promptOut.String(), "required-update=codex") || strings.Contains(promptOut.String(), "2-add:") {
		t.Fatalf("tty omitted live older sibling: %s", promptOut.String())
	}
	view, err = Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect after blocked install: %+v %v", view, err)
	}
	if notifyTreeDigest(view, "claude") != claudeDigest || notifyTreeDigest(view, "codex") != codexDigest {
		t.Fatalf("blocked install rewrote mixed digests: claude=%s/%s codex=%s/%s", claudeDigest, notifyTreeDigest(view, "claude"), codexDigest, notifyTreeDigest(view, "codex"))
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || codexAfter.BindingID != codexBefore.BindingID {
		t.Fatalf("blocked install rewrote bindings: claude=%+v/%+v codex=%+v/%+v", claudeBefore, claudeAfter, codexBefore, codexAfter)
	}
	updateReq := req
	updateReq.Action = ActionUpdate
	updateReq.Agents = blocked.NextActions[0].Agents
	updateReq.Yes = true
	updatedCodex, err := Run(ctx, updateReq)
	if err != nil || updatedCodex.Outcome != "completed" {
		t.Fatalf("update behind sibling: %+v %v", updatedCodex, err)
	}
	view, err = Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect after behind update: %+v %v", view, err)
	}
	if notifyTreeDigest(view, "claude") != claudeDigest {
		t.Fatalf("behind update rewrote claude: before=%s after=%s", claudeDigest, notifyTreeDigest(view, "claude"))
	}
	if notifyTreeDigest(view, "codex") != claudeDigest {
		t.Fatalf("behind update did not bring codex to r2: claude=%s codex=%s", claudeDigest, notifyTreeDigest(view, "codex"))
	}
	claudeDone := inspectedWizardBinding(t, ctx, control, "claude")
	codexDone := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeDone.BindingID != claudeBefore.BindingID || codexDone.BindingID != codexBefore.BindingID {
		t.Fatalf("behind update rewrote bindings: claude=%+v/%+v codex=%+v/%+v", claudeBefore, claudeDone, codexBefore, codexDone)
	}
	snap, err = installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	mat, err = materializer(req, snap, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !canGroupNotify(mat, id, Request{Action: ActionInstall}, groupAgents) {
		t.Fatal("converged mixed live install did not group")
	}
	repeat := req
	repeat.Action = ActionInstall
	repeat.Agents = []string{"claude", "codex"}
	repeat.Yes = true
	again, err := Run(ctx, repeat)
	if err != nil || (again.Outcome != "completed" && again.Outcome != "unchanged") {
		t.Fatalf("converged mixed live install: %+v %v", again, err)
	}
	view, err = Run(ctx, inspectReq)
	if err != nil {
		t.Fatalf("inspect after converged install: %+v %v", view, err)
	}
	if notifyTreeDigest(view, "claude") != claudeDigest || notifyTreeDigest(view, "codex") != claudeDigest {
		t.Fatalf("converged install rewrote digests: claude=%s/%s codex=%s", claudeDigest, notifyTreeDigest(view, "claude"), notifyTreeDigest(view, "codex"))
	}
	claudeRepeat := inspectedWizardBinding(t, ctx, control, "claude")
	codexRepeat := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeRepeat.BindingID != claudeBefore.BindingID || codexRepeat.BindingID != codexBefore.BindingID {
		t.Fatalf("converged install rewrote bindings: claude=%+v/%+v codex=%+v/%+v", claudeBefore, claudeRepeat, codexBefore, codexRepeat)
	}
}

func TestWizardInstallBothLiveBehindRequiresUpdateBoth(t *testing.T) {
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
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	claudeBefore := inspectedWizardBinding(t, ctx, control, "claude")
	codexBefore := inspectedWizardBinding(t, ctx, control, "codex")
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	blocked, err := Run(ctx, req)
	if err == nil || blocked.Outcome != "incomplete" || blocked.Reason != "update_required" {
		t.Fatalf("install both behind: %+v %v", blocked, err)
	}
	if len(blocked.NextActions) != 1 || blocked.NextActions[0].Kind != "update" || strings.Join(blocked.NextActions[0].Agents, ",") != "claude,codex" {
		t.Fatalf("behind next: %+v", blocked.NextActions)
	}
	if cmd := strings.Join(blocked.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents claude,codex") {
		t.Fatalf("behind retry: %v", blocked.NextActions[0].Command)
	}
	req.Yes = false
	plan, err := Plan(ctx, req)
	if plan.Ready || plan.Result.Reason != "update_required" {
		t.Fatalf("behind plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "required-update=claude,codex") || strings.Contains(plan.Text, "2-add:") {
		t.Fatalf("behind plan looked like add: %s", plan.Text)
	}
	var promptOut strings.Builder
	ok, err := (&LinePrompt{In: strings.NewReader("n\n"), Out: &promptOut}).Confirm(ctx, plan.Text)
	if err != nil || ok {
		t.Fatalf("behind confirm: ok=%t err=%v text=%s", ok, err, promptOut.String())
	}
	if !strings.Contains(promptOut.String(), "required-update=claude,codex") || strings.Contains(promptOut.String(), "2-add:") {
		t.Fatalf("tty omitted behind siblings: %s", promptOut.String())
	}
	inspectReq := req
	inspectReq.Action = ActionInspect
	inspectReq.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, inspectReq)
	if err != nil {
		t.Fatal(err)
	}
	claudeAfter := inspectedWizardBinding(t, ctx, control, "claude")
	codexAfter := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || codexAfter.BindingID != codexBefore.BindingID {
		t.Fatalf("behind install rewrote bindings: claude=%+v/%+v codex=%+v/%+v", claudeBefore, claudeAfter, codexBefore, codexAfter)
	}
	if notifyTreeDigest(view, "claude") != claudeBefore.TreeDigest || notifyTreeDigest(view, "codex") != codexBefore.TreeDigest {
		t.Fatalf("behind install rewrote digests: view=%+v before claude=%s codex=%s", view.Targets, claudeBefore.TreeDigest, codexBefore.TreeDigest)
	}
	updateReq := req
	updateReq.Action = ActionUpdate
	updateReq.Agents = blocked.NextActions[0].Agents
	updateReq.Yes = true
	updated, err := Run(ctx, updateReq)
	if err != nil || updated.Outcome != "completed" {
		t.Fatalf("update both behind: %+v %v", updated, err)
	}
	view, err = Run(ctx, inspectReq)
	if err != nil {
		t.Fatal(err)
	}
	claudeDigest := notifyTreeDigest(view, "claude")
	codexDigest := notifyTreeDigest(view, "codex")
	if claudeDigest == "" || claudeDigest == claudeBefore.TreeDigest || claudeDigest != codexDigest {
		t.Fatalf("behind update did not converge: before=%s claude=%s codex=%s", claudeBefore.TreeDigest, claudeDigest, codexDigest)
	}
	claudeDone := inspectedWizardBinding(t, ctx, control, "claude")
	codexDone := inspectedWizardBinding(t, ctx, control, "codex")
	if claudeDone.BindingID != claudeBefore.BindingID || codexDone.BindingID != codexBefore.BindingID {
		t.Fatalf("behind update rewrote bindings: claude=%+v/%+v codex=%+v/%+v", claudeBefore, claudeDone, codexBefore, codexDone)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := materializer(req, snap, runtime)
	if err != nil {
		t.Fatal(err)
	}
	id := portablesetup.Identity{InstallationID: installed.InstallationID}
	if id.InstallationID == "" {
		id.InstallationID = installationIDFromState(t, filepath.Join(filepath.Dir(control), "uap", "state", "state-v2.json"))
	}
	if !canGroupNotify(mat, id, Request{Action: ActionInstall}, []portable.Integration{portable.Claude, portable.Codex}) {
		t.Fatal("converged behind install did not group")
	}
}

func TestWizardTTYAddSecondClientKeepProposesNewDefaults(t *testing.T) {
	ctx := testCtx(t)
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	bundle := writePluginBundle(t)
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
		PackageRoot: pkg, PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
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
	var promptOut strings.Builder
	filled, err := FillInteractive(ctx, Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"},
		PackageRoot: pkg, PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    base.ScopeRoot,
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}, &LinePrompt{In: strings.NewReader("1\n"), Out: &promptOut}, func(agents []string) []string {
		return LiveSetupClients(base, agents)
	})
	if err != nil {
		t.Fatal(err)
	}
	if filled.ClaudeAgentNotify == nil || !*filled.ClaudeAgentNotify || filled.ClaudeHooks == nil || *filled.ClaudeHooks {
		t.Fatalf("claude keep: hooks=%v notify=%v", filled.ClaudeHooks, filled.ClaudeAgentNotify)
	}
	if filled.CodexAgentNotify == nil || !*filled.CodexAgentNotify || filled.CodexHooks == nil || !*filled.CodexHooks {
		t.Fatalf("codex new defaults: hooks=%v notify=%v prompt=%s", filled.CodexHooks, filled.CodexAgentNotify, promptOut.String())
	}
	if !strings.Contains(promptOut.String(), "codex: hooks=true agent-notify=true") || !strings.Contains(promptOut.String(), "claude: hooks=false agent-notify=true") {
		t.Fatalf("unbound codex shown as live-off: %s", promptOut.String())
	}
	if strings.Join(filled.Agents, ",") != "codex" {
		t.Fatalf("keep reinstalls bound client: %v", filled.Agents)
	}
	add := filled
	add.Yes = true
	added, err := Run(ctx, add)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("add: %+v %v", added, err)
	}
	view, err := Run(ctx, Request{
		Action: ActionInspect, Agents: []string{"claude", "codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: probe,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ScopeRoot: base.ScopeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	saw := map[string]string{}
	for _, target := range view.Targets {
		saw[target.Client+"/"+target.Unit] = target.Outcome
	}
	if saw["claude/agent-notify"] != "installed" || saw["codex/agent-notify"] != "installed" {
		t.Fatalf("notify after add: %+v", view.Targets)
	}
	if saw["codex/hooks"] != "installed" {
		t.Fatalf("codex hooks after add: %+v", view.Targets)
	}
}

func TestWizardTTYKeepBoundMixedCancels(t *testing.T) {
	ctx := testCtx(t)
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	bundle := writePluginBundle(t)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off, on := false, true
	base := Request{
		Action: ActionInstall, Yes: true, Agents: []string{"claude", "codex"},
		ClaudeHooks: &off, CodexHooks: &on, ClaudeAgentNotify: &on, CodexAgentNotify: &on,
		PackageRoot: pkg, PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(base.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, base)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("bound mixed: %+v %v", installed, err)
	}
	var promptOut strings.Builder
	_, err = FillInteractive(ctx, Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"},
		PackageRoot: pkg, PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    base.ScopeRoot,
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}, &LinePrompt{In: strings.NewReader("1\n"), Out: &promptOut}, func(agents []string) []string {
		return LiveSetupClients(base, agents)
	})
	if err != ErrPromptCanceled {
		t.Fatalf("bound keep: %v prompt=%s", err, promptOut.String())
	}
	if !strings.Contains(promptOut.String(), "claude: hooks=false agent-notify=true") || !strings.Contains(promptOut.String(), "codex: hooks=true agent-notify=true") {
		t.Fatalf("bound keep hid live units: %s", promptOut.String())
	}
	view, err := Run(ctx, Request{
		Action: ActionInspect, Agents: []string{"claude", "codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: probe,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ScopeRoot: base.ScopeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	saw := map[string]string{}
	for _, target := range view.Targets {
		saw[target.Client+"/"+target.Unit] = target.Outcome
	}
	if saw["claude/agent-notify"] != "installed" || saw["codex/agent-notify"] != "installed" || saw["codex/hooks"] != "installed" {
		t.Fatalf("bound keep mutated: %+v", view.Targets)
	}
	if saw["claude/hooks"] == "installed" {
		t.Fatalf("claude hooks appeared: %+v", view.Targets)
	}
}

func TestWizardSecondClientAddFromCopiedPackage(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	acquired := filepath.Join(filepath.Dir(control), "acquired-copy")
	writePackage(t, pkg, probe)
	writePackage(t, acquired, probe)
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
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(base.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	claudeReq := base
	claudeReq.Agents = []string{"claude"}
	claudeReq.PackageRoot = pkg
	installed, err := Run(ctx, claudeReq)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("claude: %+v %v", installed, err)
	}
	codexReq := base
	codexReq.Agents = []string{"codex"}
	codexReq.PackageRoot = acquired
	added, err := Run(ctx, codexReq)
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("copied package add: %+v %v", added, err)
	}
	if added.Reason == "update_required" {
		t.Fatalf("same digest copied root forced update: %+v", added)
	}
}

func TestWizardAmbiguousInstallationsConflictWithoutID(t *testing.T) {
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
	first := base
	first.Agents = []string{"codex"}
	first.InstallationID = "00000000-0000-4000-8000-000000000080"
	if got, err := Run(ctx, first); err != nil || got.Outcome != "completed" {
		t.Fatalf("first: %+v %v", got, err)
	}
	otherPkg := filepath.Join(filepath.Dir(control), "other-package")
	writePackage(t, otherPkg, probe)
	if err := os.WriteFile(filepath.Join(otherPkg, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: Other installation\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second := base
	second.Agents = []string{"claude"}
	second.InstallationID = "00000000-0000-4000-8000-000000000081"
	second.PackageRoot = otherPkg
	if got, err := Run(ctx, second); err != nil || got.Outcome != "completed" {
		t.Fatalf("second: %+v %v", got, err)
	}
	omitted := base
	omitted.Agents = []string{"codex"}
	omitted.InstallationID = ""
	got, err := Run(ctx, omitted)
	if err == nil || got.Outcome != "conflict" || got.Reason != "ambiguous_installation" || got.ExitCode() != 1 {
		t.Fatalf("omitted install: %+v %v", got, err)
	}
	if len(got.NextActions) == 0 || got.NextActions[0].Kind != "inspect" {
		t.Fatalf("inspect action: %+v", got.NextActions)
	}
	plan, err := Plan(ctx, omitted)
	if err == nil || plan.Ready || plan.Result.Outcome != "conflict" || plan.Result.Reason != "ambiguous_installation" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	viewReq := omitted
	viewReq.Action = ActionInspect
	viewReq.Yes = false
	viewReq.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, viewReq)
	if err != nil || view.Outcome != "completed" {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	omitted.Action = ActionUninstall
	got, err = Run(ctx, omitted)
	if err == nil || got.Outcome != "conflict" || got.Reason != "ambiguous_installation" {
		t.Fatalf("omitted uninstall: %+v %v", got, err)
	}
	explicit := omitted
	explicit.InstallationID = first.InstallationID
	explicit.ExternalUninstalled = true
	removed, err := Run(ctx, explicit)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("explicit uninstall: %+v %v", removed, err)
	}
}

func TestWizardLiveProfileConflict(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	live := filepath.Join(filepath.Dir(control), "claude-live")
	other := filepath.Join(filepath.Dir(control), "claude-other")
	for _, dir := range []string{live, other} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		ClaudeConfig: live, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: live},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := Run(ctx, req); err != nil || got.Outcome != "completed" {
		t.Fatalf("install: %+v %v", got, err)
	}
	mismatch := req
	mismatch.ClaudeConfig = other
	mismatch.ClaudeRunner = listingRunner{configRoot: other}
	got, err := Run(ctx, mismatch)
	if err == nil || got.Outcome != "conflict" || got.Reason != "live_profile_conflict" || got.ExitCode() != 1 {
		t.Fatalf("install mismatch: %+v %v", got, err)
	}
	plan, err := Plan(ctx, mismatch)
	if err == nil || plan.Ready || plan.Result.Outcome != "conflict" || plan.Result.Reason != "live_profile_conflict" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	mismatch.Action = ActionUninstall
	got, err = Run(ctx, mismatch)
	if err == nil || got.Outcome != "conflict" || got.Reason != "live_profile_conflict" {
		t.Fatalf("uninstall mismatch: %+v %v", got, err)
	}
	viewReq := req
	viewReq.Action = ActionInspect
	viewReq.Yes = false
	view, err := Run(ctx, viewReq)
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
		t.Fatalf("live binding lost: %+v", view.Targets)
	}
	same := req
	same.Action = ActionUninstall
	removed, err := Run(ctx, same)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("matching uninstall: %+v %v", removed, err)
	}
	reinstall := mismatch
	reinstall.Action = ActionInstall
	if got, err := Run(ctx, reinstall); err != nil || got.Outcome != "completed" {
		t.Fatalf("reinstall other profile: %+v %v", got, err)
	}
}

func TestWizardUninstallBothWhenOneBindingMissing(t *testing.T) {
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
	on := true
	base := Request{
		Action: ActionInstall, Yes: true, Hooks: &off, AgentNotify: &on,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(base.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	both := base
	both.Agents = []string{"claude", "codex"}
	installed, err := Run(ctx, both)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install both: %+v %v", installed, err)
	}
	dropClaude := base
	dropClaude.Action = ActionUninstall
	dropClaude.Agents = []string{"claude"}
	removed, err := Run(ctx, dropClaude)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("remove claude: %+v %v", removed, err)
	}
	both.Action = ActionUninstall
	both.ExternalUninstalled = true
	got, err := Run(ctx, both)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("remove both after one missing: %+v %v", got, err)
	}
	var claudeAbsent, codexRemoved bool
	for _, target := range got.Targets {
		if target.Unit != "agent-notify" {
			continue
		}
		if target.Client == "claude" && target.Outcome == "unchanged" && target.Reason == "already_absent" {
			claudeAbsent = true
		}
		if target.Client == "codex" && target.Outcome == "completed" {
			codexRemoved = true
		}
	}
	if !claudeAbsent || !codexRemoved {
		t.Fatalf("mixed remove targets: %+v", got.Targets)
	}
}

func TestWizardMixedUninstallHoldsClaudeUntilCodexAttested(t *testing.T) {
	ctx := testCtx(t)
	for _, agents := range [][]string{{"claude", "codex"}, {"codex", "claude"}} {
		t.Run(strings.Join(agents, ","), func(t *testing.T) {
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
			req := Request{
				Action: ActionInstall, Agents: agents, Yes: true, Hooks: &off,
				PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
				CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
				ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
				ClaudeRunner: listingRunner{configRoot: claudeConfig},
			}
			if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
				t.Fatal(err)
			}
			installed, err := Run(ctx, req)
			if err != nil || installed.Outcome != "completed" {
				t.Fatalf("install: %+v %v", installed, err)
			}
			before, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil {
				t.Fatal(err)
			}
			req.Action = ActionUninstall
			req.Yes = false
			plan, err := Plan(ctx, req)
			if err != nil || !plan.Ready {
				t.Fatalf("uninstall plan: %+v %v", plan, err)
			}
			if !strings.Contains(plan.Text, "required=external-uninstall") || !strings.Contains(plan.Text, "required-external-uninstall=codex") {
				t.Fatalf("mixed uninstall plan omitted Codex prerequisite: %s", plan.Text)
			}
			afterPlan, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil || afterPlan.Ledger.Generation != before.Ledger.Generation || afterPlan.Ledger.PendingMutation != nil {
				t.Fatalf("mixed uninstall plan mutated ledger: %+v %v", afterPlan.Ledger, err)
			}
			req.Yes = true
			held, err := Run(ctx, req)
			if err == nil || held.Outcome != "incomplete" || held.Reason != "external_uninstall_required" {
				t.Fatalf("hold: %+v %v", held, err)
			}
			for _, target := range held.Targets {
				if target.Client == "claude" && target.Unit == "agent-notify" && target.Outcome == "completed" {
					t.Fatalf("Claude removed before Codex attestation: %+v", held.Targets)
				}
			}
			live := LiveNotifyClients(control, []string{"claude", "codex"})
			have := strings.Join(live, ",")
			if len(live) != 2 || !strings.Contains(have, "claude") || !strings.Contains(have, "codex") {
				t.Fatalf("sibling revoked before Codex attestation: %v", live)
			}
			intent, err := portablesetup.ReadIntent(control)
			if err != nil || intent.ExternalUninstalled {
				t.Fatalf("hold intent: %+v %v", intent, err)
			}
			var sawClaude, sawCodex bool
			for _, target := range intent.Targets {
				if target.Client == "claude" {
					sawClaude = true
				}
				if target.Client == "codex" {
					sawCodex = true
				}
			}
			if !sawClaude || !sawCodex {
				t.Fatalf("hold dropped a sibling from intent: %+v", intent.Targets)
			}
			joined := strings.Join(held.Command, " ")
			if !strings.Contains(joined, "claude") || !strings.Contains(joined, "codex") || !strings.Contains(joined, "--external-uninstalled") {
				t.Fatalf("retry omitted mixed uninstall identity: %v", held.Command)
			}
			beforeInspect, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil {
				t.Fatal(err)
			}
			view, err := Run(ctx, Request{
				Action:      ActionInspect,
				ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: probe,
			})
			if err != nil || view.Outcome != "completed" || view.ExitCode() != 0 {
				t.Fatalf("inspect pending hold: %+v %v", view, err)
			}
			foundResume := false
			for _, next := range view.NextActions {
				if next.Kind != "external-uninstall" {
					continue
				}
				foundResume = true
				cmd := strings.Join(next.Command, " ")
				if !strings.Contains(cmd, "claude") || !strings.Contains(cmd, "codex") || !strings.Contains(cmd, "--external-uninstalled") {
					t.Fatalf("inspect omitted mixed uninstall resume: %v", next.Command)
				}
			}
			if !foundResume {
				t.Fatalf("inspect omitted pending uninstall: %+v", view.NextActions)
			}
			for _, next := range view.NextActions {
				if next.Kind == "test-notification" {
					t.Fatalf("inspect offered delivery while uninstall is pending: %+v", view.NextActions)
				}
			}
			afterInspect, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil || afterInspect.Ledger.Generation != beforeInspect.Ledger.Generation || afterInspect.Ledger.PendingMutation == nil {
				t.Fatalf("inspect mutated pending uninstall: %+v %v", afterInspect.Ledger, err)
			}
			removed, err := Run(ctx, Request{
				Action: ActionUninstall, Yes: true, ExternalUninstalled: true,
				ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
				ClientExecutable: probe, Helper: probe,
				ClaudeRunner: listingRunner{configRoot: claudeConfig},
			})
			if err != nil || removed.Outcome != "completed" {
				t.Fatalf("omitted resume attested uninstall: %+v %v", removed, err)
			}
			if remaining := LiveNotifyClients(control, []string{"claude", "codex"}); len(remaining) != 0 {
				t.Fatalf("attested uninstall left bindings: %v", remaining)
			}
		})
	}
}

func TestWizardMixedUninstallHoldsCodexHooksUntilAttested(t *testing.T) {
	ctx := testCtx(t)
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	bundle := writePluginBundle(t)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true,
		PackageRoot: pkg, PluginRoot: bundle, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install: %+v %v", installed, err)
	}
	hooks := filepath.Join(codexConfig, "hooks.json")
	data, err := os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks.json after install: %s %v", data, err)
	}
	req.Action = ActionUninstall
	held, err := Run(ctx, req)
	if err == nil || held.Outcome != "incomplete" || held.Reason != "external_uninstall_required" {
		t.Fatalf("hold: %+v %v", held, err)
	}
	data, err = os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("Codex hooks removed before attestation: %s %v", data, err)
	}
	if live := LiveNotifyClients(control, []string{"claude", "codex"}); len(live) != 2 {
		t.Fatalf("notify revoked with hooks still pending Codex: %v", live)
	}
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("attested uninstall: %+v %v", removed, err)
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
	// TTY after last uninstall sees no live bindings, so it omits --installation-id.
	req.InstallationID = ""
	reinstalled, err := Run(ctx, req)
	if err != nil || reinstalled.Outcome != "completed" {
		t.Fatalf("reinstall: %+v %v", reinstalled, err)
	}
	if reinstalled.InstallationID != firstID {
		t.Fatalf("omitted id did not reuse retained installation: %s vs %s", reinstalled.InstallationID, firstID)
	}
	if got := installationIDFromState(t, statePath); got != firstID {
		t.Fatalf("retained installation lost: %s vs %s", firstID, got)
	}
}

func TestWizardReinstallPreservesNotificationOptOuts(t *testing.T) {
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
	optOut := `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`
	if err := os.WriteFile(global, []byte(optOut), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		RefreshOnly: true, PolicyEnabled: &disabled, ExpectedGeneration: &snap.Ledger.Generation,
	}); err != nil {
		t.Fatalf("disable policy: %v", err)
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
	policy, err := installruntime.ReadUserPolicy(control)
	if err != nil || policy.Enabled {
		t.Fatalf("reinstall re-enabled policy: %+v %v", policy, err)
	}
	body, err := os.ReadFile(global)
	if err != nil || !strings.Contains(string(body), `"enabled":false`) || !strings.Contains(string(body), `"sound":false`) || !strings.Contains(string(body), `"clickToFocus":false`) || !strings.Contains(string(body), `"keep":true`) {
		t.Fatalf("reinstall lost global opt-outs: %s %v", body, err)
	}
}

func TestWizardRetainedDifferentDigestRequiresUpdate(t *testing.T) {
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
	firstID := installed.InstallationID
	if firstID == "" {
		firstID = installationIDFromState(t, statePath)
	}
	dataRoot := retainedDataRoot(t, statePath)
	sentinel := filepath.Join(dataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUninstall
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	other := filepath.Join(filepath.Dir(control), "other-package")
	writePackage(t, other, probe)
	if err := os.WriteFile(filepath.Join(other, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionInstall
	req.PackageRoot = other
	req.ExternalUninstalled = false
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "update_required" {
		t.Fatalf("retained digest mismatch: %+v %v", got, err)
	}
	if len(got.NextActions) != 2 || got.NextActions[0].Kind != "update" || got.NextActions[1].Kind != "install" {
		t.Fatalf("retained update phases: %+v", got.NextActions)
	}
	if !strings.Contains(strings.Join(got.NextActions[0].Command, " "), "--agents codex") {
		t.Fatalf("retained update missed agents: %v", got.NextActions[0].Command)
	}
	updated, err := Run(ctx, Request{
		Action: ActionUpdate, Agents: got.NextActions[0].Agents, Yes: true, Hooks: &off,
		PackageRoot: other, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: req.ScopeRoot, InstallationID: firstID,
	})
	if err != nil || updated.Outcome != "completed" {
		t.Fatalf("phase 1 retained update: %+v %v", updated, err)
	}
	if updated.Reason != "retained_source_updated" {
		t.Fatalf("phase 1 installed a client: %+v", updated)
	}
	for _, next := range updated.NextActions {
		if next.Kind == "test-notification" || next.Kind == "request-permission" {
			t.Fatalf("metadata update offered delivery: %+v", updated.NextActions)
		}
	}
	view, err := Run(ctx, Request{Action: ActionInspect, Agents: []string{"codex"}, ControlRoot: control})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("metadata update installed a client: %+v", view.Targets)
		}
	}
	added, err := Run(ctx, Request{
		Action: ActionInstall, Agents: got.NextActions[1].Agents, Yes: true, Hooks: &off,
		PackageRoot: other, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: req.ScopeRoot, InstallationID: firstID,
	})
	if err != nil || added.Outcome != "completed" {
		t.Fatalf("phase 2 retained add: %+v %v", added, err)
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
	view, err = Run(ctx, Request{Action: ActionInspect, Agents: []string{"codex"}, ControlRoot: control, RuntimeRoot: runtime, Helper: probe})
	if err != nil {
		t.Fatal(err)
	}
	var installedNotify bool
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			installedNotify = true
		}
	}
	if !installedNotify {
		t.Fatalf("phase 2 did not install: %+v", view.Targets)
	}
	if got := installationIDFromState(t, statePath); got != firstID {
		t.Fatalf("retained installation lost: %s vs %s", firstID, got)
	}
}

func prepareRetainedCodexWizard(t *testing.T, ctx context.Context) (Request, string, string, string) {
	t.Helper()
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
	firstID := installed.InstallationID
	if firstID == "" {
		firstID = installationIDFromState(t, statePath)
	}
	dataRoot := retainedDataRoot(t, statePath)
	sentinel := filepath.Join(dataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUninstall
	req.ExternalUninstalled = true
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall: %+v %v", removed, err)
	}
	other := filepath.Join(filepath.Dir(control), "other-package")
	writePackage(t, other, probe)
	if err := os.WriteFile(filepath.Join(other, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionInstall
	req.PackageRoot = other
	req.ExternalUninstalled = false
	req.Yes = false
	req.InstallationID = firstID
	return req, statePath, sentinel, pkg
}

func TestWizardPlanRetainedDifferentDigestShowsTwoPhases(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if plan.Ready || plan.Result.Reason != "update_required" {
		t.Fatalf("retained digest plan: %+v %v", plan, err)
	}
	if len(plan.Result.NextActions) != 2 || plan.Result.NextActions[0].Kind != "update" || plan.Result.NextActions[1].Kind != "install" {
		t.Fatalf("retained plan phases: %+v", plan.Result.NextActions)
	}
	if !strings.Contains(plan.Text, "required-update=codex") || !strings.Contains(plan.Text, "phases=1-update:codex;2-add:codex") {
		t.Fatalf("retained plan omitted two phases: %s", plan.Text)
	}
	if !strings.Contains(plan.Text, "data_retained=true") || !strings.Contains(plan.Text, "data-compatibility-warning") {
		t.Fatalf("retained two-phase plan omitted data warning: %s", plan.Text)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("retained plan rewrote state")
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
}

func TestWizardPlanRetainedUpdateIsMetadataOnly(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	req.Action = ActionUpdate
	req.Yes = false
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("retained update plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "data_retained=true") || !strings.Contains(plan.Text, "metadata-only") || !strings.Contains(plan.Text, "data-compatibility-warning") {
		t.Fatalf("retained update plan omitted metadata-only: %s", plan.Text)
	}
	if len(plan.Result.NextActions) == 0 || plan.Result.NextActions[0].Kind != "data_compatibility" || plan.Result.NextActions[0].Reason == "" {
		t.Fatalf("retained update plan omitted compatibility warning: %+v", plan.Result.NextActions)
	}
	if !strings.Contains(plan.Text, "phases=1-update:codex") || strings.Contains(plan.Text, "2-add:") {
		t.Fatalf("retained update plan showed add: %s", plan.Text)
	}
	if !strings.Contains(plan.Text, "required=none") || !strings.Contains(plan.Text, "permission-dialog=skipped") {
		t.Fatalf("retained update plan required client install: %s", plan.Text)
	}
	if !strings.Contains(plan.Text, "source-digest=") || plan.Request.TreeDigest == "" {
		t.Fatalf("retained update plan omitted desired digest: %s req=%s", plan.Text, plan.Request.TreeDigest)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("retained update plan rewrote state")
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
}

func TestWizardTTYConfirmRetainedUpdateShowsWarningAndCancel(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	req.Action = ActionUpdate
	req.Yes = false
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("retained update plan: %+v %v", plan, err)
	}
	var out strings.Builder
	ok, err := (&LinePrompt{In: strings.NewReader("n\n"), Out: &out}).Confirm(ctx, plan.Text)
	if err != nil || ok {
		t.Fatalf("confirm: ok=%t err=%v text=%s", ok, err, out.String())
	}
	if !strings.Contains(out.String(), "metadata-only") || !strings.Contains(out.String(), "data-compatibility-warning") {
		t.Fatalf("tty omitted retained warning: %s", out.String())
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("tty cancel rewrote retained state")
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
}

func TestWizardPlanRetainedSameRevisionIsReady(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, origPkg := prepareRetainedCodexWizard(t, ctx)
	req.PackageRoot = origPkg
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("same-revision retained plan: %+v %v", plan, err)
	}
	if strings.Contains(plan.Text, "required-update=") || strings.Contains(plan.Text, "metadata-only") {
		t.Fatalf("same-revision reinstall showed update: %s", plan.Text)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("same-revision plan rewrote state")
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
}

func TestWizardRetainedSameRevisionReinstallPreservesData(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, origPkg := prepareRetainedCodexWizard(t, ctx)
	req.PackageRoot = origPkg
	req.Action = ActionInstall
	req.Yes = true
	firstID := req.InstallationID
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("same-revision reinstall: %+v %v", got, err)
	}
	if got.InstallationID != "" && got.InstallationID != firstID {
		t.Fatalf("reinstall lost installation: %s vs %s", got.InstallationID, firstID)
	}
	if got := installationIDFromState(t, statePath); got != firstID {
		t.Fatalf("state installation lost: %s vs %s", firstID, got)
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
	view, err := Run(ctx, Request{
		Action: ActionInspect, Agents: []string{"codex"}, ControlRoot: req.ControlRoot,
		RuntimeRoot: req.RuntimeRoot, Helper: req.Helper,
	})
	if err != nil {
		t.Fatal(err)
	}
	var installedNotify bool
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			installedNotify = true
		}
	}
	if !installedNotify {
		t.Fatalf("same-revision reinstall did not install: %+v", view.Targets)
	}
}

func TestWizardRetainedUpdateReportsProgress(t *testing.T) {
	ctx := testCtx(t)
	req, _, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	req.Action = ActionUpdate
	req.Yes = true
	var phases []string
	req.Progress = func(phase string) { phases = append(phases, phase) }
	got, err := Run(ctx, req)
	if err != nil || got.Outcome != "completed" || got.Reason != "retained_source_updated" {
		t.Fatalf("retained update: %+v %v", got, err)
	}
	if strings.Join(phases, ",") != "prepare,preflight,complete" {
		t.Fatalf("retained update phases: %v", phases)
	}
	if len(got.NextActions) == 0 || got.NextActions[0].Kind != "data_compatibility" {
		t.Fatalf("retained update omitted compatibility warning: %+v", got.NextActions)
	}
	for _, next := range got.NextActions {
		if next.Kind == "request-permission" || next.Kind == "test-notification" {
			t.Fatalf("retained metadata-only update offered delivery: %+v", got.NextActions)
		}
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
}

func TestWizardPlanAfterRetainedUpdateIsReady(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	req.Action = ActionUpdate
	req.Yes = true
	updated, err := Run(ctx, req)
	if err != nil || updated.Outcome != "completed" || updated.Reason != "retained_source_updated" {
		t.Fatalf("retained update: %+v %v", updated, err)
	}
	req.Action = ActionInstall
	req.Yes = false
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("add plan after retained update: %+v %v", plan, err)
	}
	if strings.Contains(plan.Text, "required-update=") || strings.Contains(plan.Text, "metadata-only") {
		t.Fatalf("add plan still required update: %s", plan.Text)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("add plan rewrote state")
	}
	view, err := Run(ctx, Request{Action: ActionInspect, Agents: []string{"codex"}, ControlRoot: req.ControlRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("plan after metadata update installed a client: %+v", view.Targets)
		}
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
}

func TestWizardRetainedAddFailureKeepsMetadata(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	req.Action = ActionUpdate
	req.Yes = true
	updated, err := Run(ctx, req)
	if err != nil || updated.Outcome != "completed" || updated.Reason != "retained_source_updated" {
		t.Fatalf("retained update: %+v %v", updated, err)
	}
	state, err := loadUAPState(req.ControlRoot)
	if err != nil || len(state.Installations) != 1 || state.Installations[0].Source.TreeDigest == "" {
		t.Fatalf("state after metadata: %+v %v", state, err)
	}
	recorded := state.Installations[0].Source.TreeDigest
	if err := os.RemoveAll(filepath.Join(req.PackageRoot, "skills")); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionInstall
	got, err := Run(ctx, req)
	if err == nil || got.Outcome == "completed" {
		t.Fatalf("incomplete add succeeded: %+v %v", got, err)
	}
	after, err := loadUAPState(req.ControlRoot)
	if err != nil || len(after.Installations) != 1 {
		t.Fatalf("state after failed add: %+v %v", after, err)
	}
	if after.Installations[0].Source.TreeDigest != recorded {
		t.Fatalf("failed add rewrote metadata: %s vs %s", after.Installations[0].Source.TreeDigest, recorded)
	}
	if !after.Installations[0].DataRetained || len(after.Installations[0].Clients) != 0 {
		t.Fatalf("failed add changed retained install: %+v", after.Installations[0])
	}
	view, err := Run(ctx, Request{Action: ActionInspect, Agents: []string{"codex"}, ControlRoot: req.ControlRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("failed add installed a client: %+v", view.Targets)
		}
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
	writePackage(t, req.PackageRoot, req.ClientExecutable)
	if err := os.WriteFile(filepath.Join(req.PackageRoot, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Yes = false
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("add plan after failed add: %+v %v", plan, err)
	}
	if strings.Contains(plan.Text, "required-update=") || strings.Contains(plan.Text, "metadata-only") {
		t.Fatalf("failed add plan repeated metadata update: %s", plan.Text)
	}
	afterPlan, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, afterPlan) {
		t.Fatal("add plan after failed add rewrote state")
	}
}

func TestWizardRetainedDifferentPackageNameIsConflict(t *testing.T) {
	ctx := testCtx(t)
	req, statePath, sentinel, _ := prepareRetainedCodexWizard(t, ctx)
	if err := os.WriteFile(filepath.Join(req.PackageRoot, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"other-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionUpdate
	req.Yes = false
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if plan.Ready || plan.Result.Reason != "package_identity" || plan.Result.Outcome != "conflict" {
		t.Fatalf("plan different name: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "package-identity-conflict") {
		t.Fatalf("plan omitted identity conflict: %s", plan.Text)
	}
	req.Yes = true
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "conflict" || got.Reason != "package_identity" {
		t.Fatalf("run different name: %+v %v", got, err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("different name rewrote retained state")
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
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

func TestWizardUninstallNotifyOnlyKeepsHooks(t *testing.T) {
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
	req.Hooks = &off
	req.AgentNotify = &on
	req.PackageRoot = ""
	req.ExternalUninstalled = true
	req.PackageFetcher = func(context.Context, string) ([]byte, error) {
		t.Fatal("uninstall fetched a package")
		return nil, nil
	}
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("uninstall notify-only: %+v %v", removed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil || view.ExitCode() != 0 {
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
	if !hooksInstalled || notifyInstalled {
		t.Fatalf("notify-only uninstall dropped hooks or kept notify: %+v", view.Targets)
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
			Client: "codex", InstallationID: "inst-codex", Profile: codexConfig,
			MCPConfig: filepath.Join(codexConfig, "config.toml"), Units: []string{"direct-mcp"},
		}},
	})
	t.Setenv("CODEX_HOME", filepath.Join(filepath.Dir(control), "later-env-codex"))
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
	for _, want := range []string{"--agents codex", "--codex-home " + codexConfig, "--mcp-config " + filepath.Join(codexConfig, "config.toml"), "--hooks false", "--agent-notify true", "--installation-id inst-codex"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("retry omitted %q: %v", want, got.Command)
		}
	}
}

func TestWizardResumeIgnoresEnvSnapshot(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, gen := managedRuntime(t)
	probe := buildProbe(t)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	envCodex := filepath.Join(filepath.Dir(control), "later-env-codex")
	scope := filepath.Join(filepath.Dir(control), "scope")
	for _, dir := range []string{codexConfig, envCodex, scope} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Profile: codexConfig, Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		ClientExecutable: probe, Helper: probe, ScopeRoot: scope, EnvCodexHome: envCodex,
	})
	if got.Reason == "pending_intent_conflict" || got.Reason == "client_config_required" {
		t.Fatalf("env snapshot treated as explicit: %+v %v", got, err)
	}
	if !strings.Contains(strings.Join(got.Command, " "), "--codex-home "+codexConfig) {
		t.Fatalf("resume lost intent profile: %v", got.Command)
	}
	if strings.Contains(strings.Join(got.Command, " "), envCodex) {
		t.Fatalf("resume used env snapshot: %v", got.Command)
	}
}

func TestWizardResumeIgnoresDefaultReleaseVersion(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, gen := managedRuntime(t)
	probe := buildProbe(t)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	scope := filepath.Join(filepath.Dir(control), "scope")
	for _, dir := range []string{codexConfig, scope} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen, SourceRevision: "1.42.0",
		Targets: []portablesetup.IntentTarget{{Client: "codex", Profile: codexConfig, Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		ClientExecutable: probe, Helper: probe, ScopeRoot: scope, DefaultReleaseVersion: "1.43.0",
	})
	if got.Reason == "pending_intent_conflict" {
		t.Fatalf("compiled version treated as explicit: %+v %v", got, err)
	}
	if got.Reason == "noninteractive_requires_yes" || got.Reason == "empty_selection" {
		t.Fatalf("did not resume pending install: %+v %v", got, err)
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

func TestWizardResumeRejectsDifferentBindingID(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets: []portablesetup.IntentTarget{{
			Client: "codex", InstallationID: "inst-codex", BindingID: "client_pending", Units: []string{"direct-mcp"},
		}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
		InstallationID: "inst-codex",
		BindingIDs:     map[string]string{"codex": "client_other"},
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different binding id: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
		InstallationID: "inst-codex",
		BindingIDs:     map[string]string{"codex": "client_pending"},
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching binding id rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentDataReceiptID(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets: []portablesetup.IntentTarget{{
			Client: "codex", InstallationID: "inst-codex", DataReceiptID: "receipt-pending", Units: []string{"direct-mcp"},
		}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
		InstallationID: "inst-codex",
		DataReceiptIDs: map[string]string{"codex": "receipt-other"},
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different data receipt: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
		InstallationID: "inst-codex",
		DataReceiptIDs: map[string]string{"codex": "receipt-pending"},
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching data receipt rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentTreeDigest(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen, TreeDigest: "tree-a", HelperDigest: "helper-a",
		Targets: []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, TreeDigest: "tree-b",
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different tree digest: %+v %v", got, err)
	}
	got, err = Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, TreeDigest: "tree-a", HelperDigest: "helper-b",
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different helper digest: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, TreeDigest: "tree-a", HelperDigest: "helper-a",
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching tree digest rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentHelperVersion(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen, HelperVersion: "1.43.0",
		Targets: []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, HelperVersion: "1.44.0",
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different helper version: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, HelperVersion: "1.43.0",
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching helper version rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentUnits(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
	on, off := true, false
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &on, AgentNotify: &on,
		ControlRoot: control, RuntimeRoot: runtime,
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different units: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off, AgentNotify: &on,
		ControlRoot: control, RuntimeRoot: runtime,
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching units rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentRevision(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen, SourceRevision: "1.43.0",
		Targets: []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, ReleaseVersion: "1.44.0",
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different revision: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, ReleaseVersion: "1.43.0",
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching revision rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentProfile(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets: []portablesetup.IntentTarget{{
			Client: "codex", Profile: "/pending/codex-home", Units: []string{"direct-mcp"},
		}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, CodexHome: "/other/codex-home",
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different profile: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime, CodexHome: "/pending/codex-home",
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching profile rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRejectsDifferentMCPConfig(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets: []portablesetup.IntentTarget{{
			Client: "codex", MCPConfig: "/pending/config.toml", Units: []string{"direct-mcp"},
		}},
	})
	got, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
		MCPConfig: map[string]string{"codex": "/other/config.toml"},
	})
	if err == nil || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different mcp-config: %+v %v", got, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtime,
		MCPConfig: map[string]string{"codex": "/pending/config.toml"},
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching mcp-config rejected: %+v %v", matched, err)
	}
}

func TestWizardResumeRestoresMixedPerClientUnits(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, gen := managedRuntime(t)
	probe := buildProbe(t)
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	scope := filepath.Join(filepath.Dir(control), "scope")
	for _, dir := range []string{claudeConfig, codexConfig, scope} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: gen,
		Targets: []portablesetup.IntentTarget{
			{Client: "claude", Profile: claudeConfig, Units: []string{"hooks"}},
			{Client: "codex", Profile: codexConfig, Units: []string{"agent-notify"}},
		},
	})
	t.Setenv("CODEX_HOME", filepath.Join(filepath.Dir(control), "later-env-codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(filepath.Dir(control), "later-env-claude"))
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
	joined := strings.Join(got.Command, " ")
	for _, want := range []string{
		"--agents claude,codex",
		"--claude-hooks true",
		"--codex-hooks false",
		"--claude-agent-notify false",
		"--codex-agent-notify true",
		"--claude-config " + claudeConfig,
		"--codex-home " + codexConfig,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("retry omitted %q: %v", want, got.Command)
		}
	}
	if strings.Contains(joined, "--hooks ") || strings.Contains(joined, "--agent-notify ") {
		t.Fatalf("mixed units collapsed to global flags: %v", got.Command)
	}
	on, off := true, false
	conflict, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true,
		Hooks: &on, AgentNotify: &on,
		ControlRoot: control, RuntimeRoot: runtime,
	})
	if err == nil || conflict.Outcome != "conflict" || conflict.Reason != "pending_intent_conflict" {
		t.Fatalf("global units matched mixed intent: %+v %v", conflict, err)
	}
	matched, err := Run(ctx, Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true,
		ClaudeHooks: &on, CodexHooks: &off, ClaudeAgentNotify: &off, CodexAgentNotify: &on,
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		ClaudeConfig: claudeConfig, CodexHome: codexConfig,
		ClientExecutable: probe, Helper: probe, ScopeRoot: scope,
	})
	if matched.Reason == "pending_intent_conflict" {
		t.Fatalf("matching mixed units rejected: %+v %v", matched, err)
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
	intent, err := portablesetup.ReadIntent(control)
	if err != nil || len(intent.Targets) != 1 || intent.Targets[0].DataReceiptID == "" {
		t.Fatalf("uninstall intent omitted known data receipt: %+v %v", intent, err)
	}
	if intent.ExternalUninstalled {
		t.Fatalf("hold recorded attestation: %+v", intent)
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

func TestWizardResumeRestoresExternalUninstalledFromIntent(t *testing.T) {
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
	held, err := Run(ctx, req)
	if err == nil || held.Outcome != "incomplete" || held.Reason != "external_uninstall_required" {
		t.Fatalf("hold: %+v %v", held, err)
	}
	if err := (portablesetup.Service{}).PatchIntentExternalUninstalled(ctx, control, runtime, ""); err != nil {
		t.Fatal(err)
	}
	resume := Request{
		Action: ActionUninstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: req.ScopeRoot,
	}
	removed, err := Run(ctx, resume)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("resume omitted attestation: %+v %v", removed, err)
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
	generation := removed.Generation
	req.PackageFetcher = func(context.Context, string) ([]byte, error) {
		t.Fatal("second uninstall fetched a package")
		return nil, nil
	}
	again, err := Run(ctx, req)
	if err != nil || again.ExitCode() != 0 {
		t.Fatalf("second uninstall: %+v %v", again, err)
	}
	if again.Outcome != "unchanged" || again.Reason != "already_absent" {
		t.Fatalf("second uninstall: %+v %v", again, err)
	}
	if !again.DataRetained {
		t.Fatalf("already_absent omitted data_retained: %+v", again)
	}
	if again.Generation != generation {
		t.Fatalf("already_absent mutated generation: %d -> %d", generation, again.Generation)
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
	if !view.DataRetained {
		t.Fatalf("inspect omitted data_retained: %+v", view)
	}
}

func installationIDFromState(t *testing.T, path string) string {
	t.Helper()
	state := loadInstallerState(t, path)
	if len(state.Installations) != 1 || state.Installations[0].InstallationID == "" {
		t.Fatalf("installations: %+v", state.Installations)
	}
	return state.Installations[0].InstallationID
}

func retainedDataRoot(t *testing.T, path string) string {
	t.Helper()
	state := loadInstallerState(t, path)
	if len(state.Installations) != 1 {
		t.Fatalf("installations: %+v", state.Installations)
	}
	for _, receipt := range state.Installations[0].DataReceipts {
		if receipt.Locator != "" {
			return receipt.Locator
		}
	}
	t.Fatal("missing data receipt locator")
	return ""
}

func loadInstallerState(t *testing.T, path string) struct {
	Installations []struct {
		InstallationID string `json:"installation_id"`
		DataReceipts   map[string]struct {
			Locator string `json:"locator"`
		} `json:"data_receipts"`
	} `json:"installations"`
} {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Installations []struct {
			InstallationID string `json:"installation_id"`
			DataReceipts   map[string]struct {
				Locator string `json:"locator"`
			} `json:"data_receipts"`
		} `json:"installations"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	return state
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

func TestWizardDefaultCodexProfileHandoffsDirectMCP(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, primary, gen := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	mcpConfig := filepath.Join(codexConfig, "config.toml")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
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
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("default-path handoff plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "codex-mcp="+mcpConfig) {
		t.Fatalf("plan omitted discovered mcp: %s", plan.Text)
	}
	retry := strings.Join(RetryCommand(plan.Request), " ")
	if !strings.Contains(retry, "--mcp-config "+mcpConfig) {
		t.Fatalf("plan retry omitted resolved mcp: %v", RetryCommand(plan.Request))
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("default-path handoff install: %+v %v", installed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var notify, direct, mcpFile string
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" {
			notify = target.Outcome
		}
		if target.Unit == "direct-mcp" {
			direct = target.Outcome
			mcpFile = target.ConfigPath
		}
	}
	if notify != "installed" || direct != "absent" || mcpFile != mcpConfig {
		t.Fatalf("default-path handoff inspect: notify=%s direct=%s mcp=%s targets=%+v", notify, direct, mcpFile, view.Targets)
	}
}

func TestFinishWizardIntentClearsExactRevisionRequired(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-repair-intent", Action: "repair", Stage: "confirmed",
		ExpectedGeneration: gen,
		Targets:            []portablesetup.IntentTarget{{Client: "claude", Units: []string{"agent-notify"}}, {Client: "codex", Units: []string{"agent-notify"}}},
	})
	got, err := finishWizardIntent(ctx, Request{ControlRoot: control}, runtime, Result{Outcome: "incomplete", Reason: "exact_revision_required"}, ErrRefused)
	if got.Outcome != "incomplete" || got.Reason != "exact_revision_required" {
		t.Fatalf("result: %+v %v", got, err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil {
		t.Fatalf("exact_revision_required kept reservation: %+v %v", snap.Ledger.PendingMutation, err)
	}
	if _, err := os.Lstat(portablesetup.IntentPath(control)); !os.IsNotExist(err) {
		t.Fatal("exact_revision_required retained intent")
	}
}

func TestFinishWizardIntentClearsActivationIncomplete(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "confirmed",
		ExpectedGeneration: gen,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Units: []string{"agent-notify"}}},
	})
	got, err := finishWizardIntent(ctx, Request{ControlRoot: control}, runtime, Result{Outcome: "incomplete", Reason: "activation_incomplete"}, errors.New("host seam refused"))
	if got.Outcome != "incomplete" || got.Reason != "activation_incomplete" {
		t.Fatalf("result: %+v %v", got, err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil {
		t.Fatalf("activation_incomplete kept reservation: %+v %v", snap.Ledger.PendingMutation, err)
	}
	if _, err := os.Lstat(portablesetup.IntentPath(control)); !os.IsNotExist(err) {
		t.Fatal("activation_incomplete retained intent")
	}
}

func TestFinishWizardIntentKeepsIncompleteHandoff(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, gen := managedRuntime(t)
	plantPendingIntent(t, ctx, control, runtime, gen, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "confirmed",
		ExpectedGeneration: gen,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Units: []string{"agent-notify"}}},
	})
	got, err := finishWizardIntent(ctx, Request{ControlRoot: control}, runtime, Result{Outcome: "incomplete", Reason: "plugin_root_required"}, errors.New("plugin root"))
	if got.Outcome != "incomplete" || got.Reason != "plugin_root_required" {
		t.Fatalf("result: %+v %v", got, err)
	}
	if err == nil {
		t.Fatal("expected incomplete error")
	}
	snap, readErr := installruntime.ReadInstalledSnapshot(control)
	if readErr != nil || snap.Ledger.PendingMutation == nil {
		t.Fatalf("dropped unfinished handoff: %+v %v", snap.Ledger.PendingMutation, readErr)
	}
}

func TestWizardInstallClearsConfirmationIntent(t *testing.T) {
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
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil {
		t.Fatalf("completed install left reservation: %+v %v", snap.Ledger.PendingMutation, err)
	}
	if _, err := os.Lstat(portablesetup.IntentPath(control)); !os.IsNotExist(err) {
		t.Fatal("completed install retained intent")
	}
}

func TestWizardConfirmationIntentSurvivesFailedHooks(t *testing.T) {
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
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "plugin_root_required" {
		t.Fatalf("hooks preflight: %+v %v", got, err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation == nil {
		t.Fatalf("failed hooks dropped intent: %+v %v", snap.Ledger.PendingMutation, err)
	}
	intent, err := portablesetup.ReadIntent(control)
	if err != nil || intent.Action != "install" || intent.Stage != "confirmed" {
		t.Fatalf("intent: %+v %v", intent, err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].Client != "codex" {
		t.Fatalf("targets: %+v", intent.Targets)
	}
	if intent.Targets[0].InstallationID == "" || intent.Targets[0].BindingID == "" {
		t.Fatalf("intent omitted reserved ids: %+v", intent.Targets[0])
	}
	if intent.TreeDigest == "" || intent.HelperDigest == "" || intent.HelperVersion == "" {
		t.Fatalf("intent omitted source/helper identity: %+v", intent)
	}
	beforeInspect, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	view, err := Run(ctx, Request{
		Action: ActionInspect, Agents: []string{"codex"},
		ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: probe,
	})
	if err != nil || view.Outcome != "completed" || view.ExitCode() != 0 {
		t.Fatalf("inspect pending install: %+v %v", view, err)
	}
	foundResume := false
	for _, next := range view.NextActions {
		if next.Kind == "test-notification" {
			t.Fatalf("inspect offered delivery while install is pending: %+v", view.NextActions)
		}
		if next.Kind != "resume" {
			continue
		}
		foundResume = true
		cmd := strings.Join(next.Command, " ")
		if !strings.Contains(cmd, "install") || !strings.Contains(cmd, "codex") {
			t.Fatalf("inspect omitted pending install resume: %v", next.Command)
		}
	}
	if !foundResume {
		t.Fatalf("inspect omitted pending install: %+v", view.NextActions)
	}
	afterInspect, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || afterInspect.Ledger.Generation != beforeInspect.Ledger.Generation || afterInspect.Ledger.PendingMutation == nil {
		t.Fatalf("inspect mutated pending install: %+v %v", afterInspect.Ledger, err)
	}
	resume := req
	resume.Agents = nil
	resume.Yes = false
	again, err := Run(ctx, resume)
	if again.Reason == "empty_selection" || again.Reason == "noninteractive_requires_yes" {
		t.Fatalf("resume ignored confirmation intent: %+v %v", again, err)
	}
	if again.Outcome != "incomplete" || again.Reason != "plugin_root_required" {
		t.Fatalf("resume: %+v %v", again, err)
	}
}

func TestWizardFailedHooksPersistsMixedUnits(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(filepath.Dir(control), "codex-profile")
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	for _, dir := range []string{codexConfig, claudeConfig, filepath.Join(filepath.Dir(control), "scope")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	on, off := true, false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true,
		ClaudeHooks: &off, CodexHooks: &on, ClaudeAgentNotify: &on, CodexAgentNotify: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: codexConfig, ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	got, err := Run(ctx, req)
	if err == nil || got.Outcome != "incomplete" || got.Reason != "plugin_root_required" {
		t.Fatalf("hooks preflight: %+v %v", got, err)
	}
	intent, err := portablesetup.ReadIntent(control)
	if err != nil {
		t.Fatal(err)
	}
	units := map[string]string{}
	for _, target := range intent.Targets {
		units[target.Client] = strings.Join(target.Units, ",")
	}
	if units["claude"] != "agent-notify" || units["codex"] != "hooks" {
		t.Fatalf("mixed units not persisted: %+v", intent.Targets)
	}
	resume := req
	resume.Agents = nil
	resume.Yes = false
	resume.ClaudeHooks, resume.CodexHooks = nil, nil
	resume.ClaudeAgentNotify, resume.CodexAgentNotify = nil, nil
	again, _ := Run(ctx, resume)
	if again.Reason == "empty_selection" || again.Reason == "noninteractive_requires_yes" || again.Reason == "pending_intent_conflict" {
		t.Fatalf("resume ignored mixed intent: %+v", again)
	}
	joined := strings.Join(again.Command, " ")
	for _, want := range []string{"--claude-hooks false", "--codex-hooks true", "--claude-agent-notify true", "--codex-agent-notify false"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("resume retry omitted %q: %v", want, again.Command)
		}
	}
}

func TestWizardCodexUninstallAttestsFromEmptyPluginList(t *testing.T) {
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
	req.ClientExecutable = writeCodexListStub(t, filepath.Dir(control), `{"installed":[]}`)
	removed, err := Run(ctx, req)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("observed uninstall: %+v %v", removed, err)
	}
	if strings.Contains(strings.Join(removed.Command, " "), "--external-uninstalled") && removed.Outcome != "completed" {
		t.Fatalf("empty list still required flag: %v", removed.Command)
	}
}

func writeCodexListStub(t *testing.T, dir, listJSON string) string {
	t.Helper()
	src := filepath.Join(dir, "codex-stub.go")
	code := fmt.Sprintf("package main\nimport (\"encoding/json\"; \"fmt\"; \"os\"; \"strings\")\nfunc main() {\n\tif strings.Join(os.Args[1:], \" \") == \"plugin list --json\" {\n\t\tfmt.Println(%s)\n\t\treturn\n\t}\n\tif len(os.Args) >= 3 && os.Args[1] == \"plugin\" && os.Args[2] == \"remove\" {\n\t\tjson.NewEncoder(os.Stdout).Encode(map[string]any{\"ok\": true})\n\t\treturn\n\t}\n\tos.Exit(1)\n}\n", strconv.Quote(listJSON))
	if err := os.WriteFile(src, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "codex-stub")
	cmd := exec.Command("go", "build", "-o", path, src)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build codex stub: %s %v", body, err)
	}
	return path
}

func TestDiscoverAgentsReportsPresenceWithoutExecuting(t *testing.T) {
	control := filepath.Join(t.TempDir(), "control")
	if err := os.MkdirAll(control, 0700); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	path := filepath.Join(binDir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	got := DiscoverAgents(Request{ControlRoot: control})
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("discover executed PATH candidate")
	}
	state := filepath.Join(filepath.Dir(control), "uap", "state")
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatal("discover created UAP state")
	}
	if len(got) != 2 || got[0].ID != "claude" || !got[0].Present || got[0].Path != path {
		t.Fatalf("claude: %+v", got)
	}
	if got[1].Present || got[1].Path != "" || got[0].Bound || got[1].Bound {
		t.Fatalf("codex should be absent: %+v", got)
	}
}

func TestPortableInstallFailedKeepsActivationIncomplete(t *testing.T) {
	err := portablesetup.ResultError{
		Result: uapinstaller.Result{
			Outcome: uapinstaller.OutcomeIncomplete,
			Reason:  "host seam refused after managed commit",
			Client: uapinstaller.ClientResult{
				ClientID:        "codex",
				Materialization: string(domain.MaterializationMaterialized),
				Activation:      string(domain.ActivationFailed),
			},
		},
		Err: errors.New("host seam refused after managed commit"),
	}
	got := portableInstallFailed(portable.Codex, Request{Action: ActionInstall, Agents: []string{"codex"}, ControlRoot: "/tmp/control"}, Result{Action: "install"}, err)
	if got.Outcome != "incomplete" || got.Reason != "activation_incomplete" {
		t.Fatalf("incomplete mapping: %+v", got)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "activate" || got.NextActions[0].Agents[0] != "codex" {
		t.Fatalf("activate action: %+v", got.NextActions)
	}
	if len(got.NextActions[0].Command) < 2 || got.NextActions[0].Command[0] != "setup-notifications" || got.NextActions[0].Command[1] != "wizard" {
		t.Fatalf("activate retry command: %v", got.NextActions[0].Command)
	}
	if got.Targets[0].Outcome != "incomplete" {
		t.Fatalf("target: %+v", got.Targets)
	}
	plain := portableInstallFailed(portable.Codex, Request{Action: ActionInstall}, Result{Action: "install"}, errors.New("missing helper"))
	if plain.Reason != "portable_install_failed" || len(plain.NextActions) != 0 {
		t.Fatalf("pre-commit failure: %+v", plain)
	}
}

func TestSiblingCompatibilityUnavailableMapsUpdateBoth(t *testing.T) {
	got := portableInstallFailed(portable.Codex, Request{Action: ActionUpdate, Agents: []string{"codex"}}, Result{Action: "update"}, uapinstaller.ErrCompatibilityUnavailable)
	if got.Outcome != "incomplete" || got.Reason != "sibling_compatibility_unavailable" {
		t.Fatalf("mapping: %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0].Client != "codex" || got.Targets[0].Reason != "sibling_compatibility_unavailable" {
		t.Fatalf("target: %+v", got.Targets)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" {
		t.Fatalf("next: %+v", got.NextActions)
	}
	if strings.Join(got.NextActions[0].Agents, ",") != "claude,codex" {
		t.Fatalf("retry agents: %v", got.NextActions[0].Agents)
	}
	mapped, err := mapPreviewFailure(context.Background(), Request{Action: ActionUpdate}, portable.Codex, portablesetup.Materializer{}, portablesetup.Identity{}, uapinstaller.ErrCompatibilityUnavailable, Result{Action: "update"})
	if err == nil || mapped.Reason != "sibling_compatibility_unavailable" {
		t.Fatalf("preview mapping: %+v %v", mapped, err)
	}
}

func TestGroupNotifyFailedReportsBothCommittedTargets(t *testing.T) {
	err := portablesetup.ResultError{
		Result: uapinstaller.Result{
			Outcome: uapinstaller.OutcomeIncomplete,
			Reason:  "host seam refused claude after managed commit",
			Client: uapinstaller.ClientResult{
				ClientID:        "codex",
				Materialization: string(domain.MaterializationMaterialized),
				Activation:      string(domain.ActivationActive),
			},
			Targets: []uapinstaller.ClientResult{
				{
					ClientID:        "codex",
					BindingID:       "bound-codex",
					Materialization: string(domain.MaterializationMaterialized),
					Activation:      string(domain.ActivationActive),
				},
				{
					ClientID:        "claude",
					BindingID:       "bound-claude",
					Materialization: string(domain.MaterializationMaterialized),
					Activation:      string(domain.ActivationActive),
				},
			},
		},
		Err: errors.New("host seam refused claude after managed commit"),
	}
	got := groupNotifyFailed(
		[]portable.Integration{portable.Codex, portable.Claude},
		Request{Action: ActionInstall, Agents: []string{"codex", "claude"}, ControlRoot: "/tmp/control"},
		Result{Action: "install"},
		err,
	)
	if got.Outcome != "incomplete" || got.Reason != "activation_incomplete" {
		t.Fatalf("group incomplete mapping: %+v", got)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("group targets: %+v", got.Targets)
	}
	seen := map[string]string{}
	for _, target := range got.Targets {
		seen[target.Client] = target.Reason
		if target.Outcome != "incomplete" || target.Reason != "activation_incomplete" {
			t.Fatalf("group target: %+v", target)
		}
	}
	if seen["codex"] == "" || seen["claude"] == "" {
		t.Fatalf("group omitted a committed client: %+v", got.Targets)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "activate" {
		t.Fatalf("group activate action: %+v", got.NextActions)
	}
	if len(got.NextActions[0].Agents) != 2 || got.NextActions[0].Agents[0] != "codex" || got.NextActions[0].Agents[1] != "claude" {
		t.Fatalf("group activate agents: %+v", got.NextActions[0].Agents)
	}
	if len(got.NextActions[0].Command) < 2 || got.NextActions[0].Command[0] != "setup-notifications" || got.NextActions[0].Command[1] != "wizard" {
		t.Fatalf("group activate retry command: %v", got.NextActions[0].Command)
	}
	plain := groupNotifyFailed(
		[]portable.Integration{portable.Codex, portable.Claude},
		Request{Action: ActionInstall, Agents: []string{"codex", "claude"}},
		Result{Action: "install"},
		errors.New("missing helper"),
	)
	if plain.Reason != "portable_install_failed" || len(plain.Targets) != 1 || len(plain.NextActions) != 0 {
		t.Fatalf("group pre-commit failure: %+v", plain)
	}
}

func TestGroupRemoveFailedReportsBothTargets(t *testing.T) {
	err := portablesetup.ResultError{
		Result: uapinstaller.Result{
			Outcome: uapinstaller.OutcomeIncomplete,
			Reason:  "remove interrupted after first target",
			Targets: []uapinstaller.ClientResult{
				{ClientID: "codex", BindingID: "bound-codex", Materialization: string(domain.MaterializationAbsent)},
				{ClientID: "claude", BindingID: "bound-claude", Materialization: string(domain.MaterializationMaterialized)},
			},
		},
		Err: errors.New("remove interrupted after first target"),
	}
	got := groupRemoveFailed(
		[]portable.Integration{portable.Codex, portable.Claude},
		Request{Action: ActionUninstall, Agents: []string{"codex", "claude"}, ExternalUninstalled: true, ControlRoot: "/tmp/control"},
		Result{Action: "uninstall"},
		err,
	)
	if got.Outcome != "incomplete" || got.Reason != "portable_remove_failed" {
		t.Fatalf("group remove mapping: %+v", got)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("group remove targets: %+v", got.Targets)
	}
	seen := map[string]TargetResult{}
	for _, target := range got.Targets {
		seen[target.Client] = target
	}
	if seen["codex"].Outcome != "unchanged" || seen["codex"].Reason != "already_absent" {
		t.Fatalf("codex target: %+v", seen["codex"])
	}
	if seen["claude"].Outcome != "incomplete" {
		t.Fatalf("claude target: %+v", seen["claude"])
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "uninstall" || len(got.NextActions[0].Agents) != 2 {
		t.Fatalf("group remove retry: %+v", got.NextActions)
	}
	if len(got.NextActions[0].Command) < 2 || got.NextActions[0].Command[0] != "setup-notifications" || got.NextActions[0].Command[1] != "wizard" {
		t.Fatalf("group remove retry command: %v", got.NextActions[0].Command)
	}
}

func TestWizardCodexLiveProfileConflict(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	live := filepath.Join(filepath.Dir(control), "codex-live")
	other := filepath.Join(filepath.Dir(control), "codex-other")
	for _, dir := range []string{live, other} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		CodexHome: live, ClientExecutable: probe, Helper: probe,
		ScopeRoot: filepath.Join(filepath.Dir(control), "scope"),
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := Run(ctx, req); err != nil || got.Outcome != "completed" {
		t.Fatalf("install: %+v %v", got, err)
	}
	mismatch := req
	mismatch.CodexHome = other
	got, err := Run(ctx, mismatch)
	if err == nil || got.Outcome != "conflict" || got.Reason != "live_profile_conflict" || got.ExitCode() != 1 {
		t.Fatalf("install mismatch: %+v %v", got, err)
	}
	plan, err := Plan(ctx, mismatch)
	if err == nil || plan.Ready || plan.Result.Outcome != "conflict" || plan.Result.Reason != "live_profile_conflict" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	mismatch.Action = ActionUninstall
	mismatch.ExternalUninstalled = true
	got, err = Run(ctx, mismatch)
	if err == nil || got.Outcome != "conflict" || got.Reason != "live_profile_conflict" {
		t.Fatalf("uninstall mismatch: %+v %v", got, err)
	}
	same := req
	same.Action = ActionUninstall
	same.ExternalUninstalled = true
	removed, err := Run(ctx, same)
	if err != nil || removed.Outcome != "completed" {
		t.Fatalf("matching uninstall: %+v %v", removed, err)
	}
	reinstall := mismatch
	reinstall.Action = ActionInstall
	reinstall.ExternalUninstalled = false
	if got, err := Run(ctx, reinstall); err != nil || got.Outcome != "completed" {
		t.Fatalf("reinstall other profile: %+v %v", got, err)
	}
}

func TestWizardDefaultClaudeRegistrationHandoffsDirectMCP(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, primary, gen := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	claudeConfig := filepath.Join(filepath.Dir(control), "claude-profile")
	mcpConfig := filepath.Join(filepath.Dir(control), ".claude.json")
	if err := os.MkdirAll(claudeConfig, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := clientsetup.Apply(ctx, clientsetup.Request{
		ControlRoot: control, RuntimeRoot: runtime, Command: primary, ConfigPath: mcpConfig,
		Provider: registration.Claude, Mode: clientsetup.Managed, ExpectedGeneration: gen,
	}); err != nil {
		t.Fatal(err)
	}
	off := false
	req := Request{
		Action: ActionInstall, Agents: []string{"claude"}, Yes: true, Hooks: &off,
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global,
		ClaudeConfig: claudeConfig, ClientExecutable: probe, Helper: probe,
		ScopeRoot:    filepath.Join(filepath.Dir(control), "scope"),
		MCPConfig:    map[string]string{"claude": mcpConfig},
		ClaudeRunner: listingRunner{configRoot: claudeConfig},
	}
	if err := os.MkdirAll(req.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("default-path handoff plan: %+v %v", plan, err)
	}
	if !strings.Contains(plan.Text, "claude-mcp="+mcpConfig) {
		t.Fatalf("plan omitted discovered mcp: %s", plan.Text)
	}
	retry := strings.Join(RetryCommand(plan.Request), " ")
	if !strings.Contains(retry, "--claude-mcp-config "+mcpConfig) {
		t.Fatalf("plan retry omitted resolved mcp: %v", RetryCommand(plan.Request))
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("default-path handoff install: %+v %v", installed, err)
	}
	req.Action = ActionInspect
	req.Yes = false
	view, err := Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var notify, direct, mcpFile string
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" {
			notify = target.Outcome
		}
		if target.Unit == "direct-mcp" {
			direct = target.Outcome
			mcpFile = target.ConfigPath
		}
	}
	if notify != "installed" || direct != "absent" || mcpFile != mcpConfig {
		t.Fatalf("default-path handoff inspect: notify=%s direct=%s mcp=%s targets=%+v", notify, direct, mcpFile, view.Targets)
	}
}
