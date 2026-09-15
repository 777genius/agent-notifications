//go:build linux || darwin

package setupwizard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if got.Outcome != "incomplete" || got.Reason != "recovery_required" {
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
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.PendingMutation != nil || snap.Ledger.Generation != gen {
		t.Fatalf("plan recovered journal: %+v %v", snap.Ledger, err)
	}
}

func plantWizardJournal(t *testing.T, controlRoot string) {
	t.Helper()
	owned := filepath.Join(filepath.Dir(controlRoot), "uap", "managed")
	staging := filepath.Join(owned, ".agentplugins-staging-pending")
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	opID := "wizard-pending-op"
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

func TestWizardUnsupportedRepair(t *testing.T) {
	control, _, _, _, _ := managedRuntime(t)
	got, err := Run(testCtx(t), Request{Action: ActionRepair, Agents: []string{"codex"}, Yes: true, ControlRoot: control})
	if err == nil || got.Outcome != "incomplete" || got.Reason != "action_not_published" {
		t.Fatalf("repair: %+v %v", got, err)
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
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
	view, err := Run(ctx, Request{Action: ActionInspect, Agents: []string{"codex"}, ControlRoot: control})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("hidden retained migration: %+v", view.Targets)
		}
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
	path := filepath.Join(dir, "codex-stub")
	script := "#!/bin/sh\ncase \"$*\" in\n  \"plugin list --json\") printf '%s\\n' '" + listJSON + "';;\n  \"plugin remove \"*) echo '{\"ok\":true}';;\n  *) exit 1;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
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
