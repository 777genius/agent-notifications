package uapinstaller

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	name := "probe"
	if runtime.GOOS == "windows" {
		name = "probe.exe"
	}
	out := filepath.Join(dir, name)
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
		"plugin.json":                   []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.0"}`),
		"mcp.json":                      []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"sample-notify":{"type":"stdio","command":"./bin/probe","args":[],"env":{}}}}`),
		"skills/sample-notify/SKILL.md": []byte("---\nname: sample-notify\ndescription: Isolated installer sample\n---\nFixture only.\n"),
		"bin/probe":                     body,
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

func writePackageMode(t *testing.T, root, probe string, binMode os.FileMode) {
	t.Helper()
	writePackage(t, root, probe)
	if err := os.Chmod(filepath.Join(root, "bin", "probe"), binMode); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsRelativeStateRootAndDoesNotCreateDirs(t *testing.T) {
	if _, err := New(Config{StateRoot: "relative"}); err == nil {
		t.Fatal("relative StateRoot accepted")
	}
	root := filepath.Join(t.TempDir(), "missing-state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("New created StateRoot")
	}
	if eng.cfg.StateFile != filepath.Join(root, "state-v2.json") {
		t.Fatalf("default state file: %s", eng.cfg.StateFile)
	}
}

func skipWindowsLauncherExecuteBit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("UAP managedstdio.NewSource requires Perm()&0111; Go Windows FileMode does not set execute bits on regular files")
	}
}

func TestInstallInspectRepeatRemove(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package source")
	writePackage(t, pkg, probe)
	state := filepath.Join(base, "uap")
	config := filepath.Join(base, "client config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(config, "foreign.txt")
	if err := os.WriteFile(foreign, []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: state, HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000042",
		OperationID: "sample-install", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if _, err := os.ReadFile(foreign); err != nil {
		t.Fatal("prepare mutated foreign client file")
	}
	if _, err := eng.Apply(ctx, prepared, Decision{}); err == nil {
		t.Fatal("unconfirmed apply accepted")
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("cancelled apply wrote state")
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted || result.InstallationID != req.InstallationID {
		t.Fatalf("install result: %+v", result)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	again, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	repeat, err := eng.Apply(ctx, again, Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !repeat.NoChange && repeat.Outcome != OutcomeUnchanged && repeat.Outcome != OutcomeCompleted {
		t.Fatalf("repeat install: %+v", repeat)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "sample-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	removed, err := eng.Apply(ctx, rm, Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if removed.Outcome != OutcomeCompleted && removed.Outcome != OutcomeUnchanged {
		t.Fatalf("remove: %+v", removed)
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "keep\n" {
		t.Fatalf("foreign entry: %s %v", got, err)
	}
	view, err = eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, installation := range view.Installations {
		for _, binding := range installation.Bindings {
			if binding.ClientID == "codex" && binding.TargetPath != "" {
				if _, err := os.Lstat(binding.TargetPath); err == nil {
					t.Fatal("codex projection survived remove")
				}
			}
		}
	}
}

func TestUnsupportedUpdateRejectedBeforeMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prepare(testCtx(t), Request{Operation: OpUpdate, ClientID: "codex", ClientConfigRoot: root, PackageRoot: root}); err == nil {
		t.Fatal("update published")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("rejected update created state")
	}
}

func TestPrepareSnapshotIgnoresLaterSourceMutation(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000043",
		OperationID: "sealed-source", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	digest := prepared.Plan().TreeDigest
	if digest == "" {
		t.Fatal("prepare returned empty tree digest")
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"name":"mutated"}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCompleted {
		t.Fatalf("sealed apply: %+v", result)
	}
	if prepared.Plan().TreeDigest != digest {
		t.Fatal("source mutation changed owned snapshot digest")
	}
}

func TestPrepareMarksBinExecutableWithoutHostExecuteBits(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "client")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap")})
	if err != nil {
		t.Fatal(err)
	}
	req := func(pkg string) Request {
		return Request{
			Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
			ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000044",
			OperationID: "logical-exec", RequiredComponents: []string{"mcp", "skills"},
		}
	}
	plain := filepath.Join(base, "plain")
	exec := filepath.Join(base, "exec")
	writePackageMode(t, plain, probe, 0644)
	writePackageMode(t, exec, probe, 0755)
	plainPrep, err := eng.Prepare(ctx, req(plain))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plainPrep.Close() }()
	execPrep, err := eng.Prepare(ctx, req(exec))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = execPrep.Close() }()
	if plainPrep.Plan().TreeDigest == "" || plainPrep.Plan().TreeDigest != execPrep.Plan().TreeDigest {
		t.Fatalf("host execute bits changed TreeDigest: %s vs %s", plainPrep.Plan().TreeDigest, execPrep.Plan().TreeDigest)
	}
}

func TestInstallerSourceDoesNotImportNotifications(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "internal/agentnotify") {
			t.Fatalf("%s imports Notifications types", entry.Name())
		}
	}
}

func TestRecoverEmptyRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.RecoverCurrent(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(testCtx(t))
	if err != nil || view.Recovery.Required || view.StateRoot != root {
		t.Fatalf("empty inspect: %+v %v", view, err)
	}
}

func TestApplyRejectsClosedWrongAndRepeatedHandles(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000045",
		OperationID: "handle-reuse", RequiredComponents: []string{"mcp", "skills"},
	}
	closed, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, closed, Decision{Confirmed: true}); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("apply after close: %v", err)
	}
	other, err := New(Config{StateRoot: filepath.Join(base, "other")})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if _, err := other.Apply(ctx, prepared, Decision{Confirmed: true}); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("foreign engine: %v", err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("first apply: %+v %v", result, err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("double apply: %v", err)
	}
}

func TestNewCopiesConfigAndRejectsRelativeHelper(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	helper := filepath.Join(t.TempDir(), "helper")
	cfg := Config{StateRoot: root, HelperExecutable: helper}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateRoot = filepath.Join(t.TempDir(), "other")
	cfg.HelperExecutable = filepath.Join(t.TempDir(), "other")
	if eng.cfg.StateRoot != root || eng.cfg.HelperExecutable != helper {
		t.Fatal("New did not copy Config")
	}
	if _, err := New(Config{StateRoot: root, HelperExecutable: "relative-helper"}); err == nil {
		t.Fatal("relative HelperExecutable accepted")
	}
}

func TestPrepareCopiesRequestAndPlan(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap")})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000046",
		OperationID: "copy-request", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if prepared.Plan().DigestAlgorithm != "agentplugins-tree-sha256-v1" || prepared.Plan().TreeDigest == "" {
		t.Fatalf("canonical tree digest: %+v", prepared.Plan())
	}
	req.RequiredComponents[0] = "mutated"
	req.PackageRoot = filepath.Join(base, "other")
	req.ClientID = "claude"
	plan := prepared.Plan()
	plan.TreeDigest = "tampered"
	plan.RequiredMissing = []string{"x"}
	got := prepared.Plan()
	if got.TreeDigest == "tampered" || got.ClientID != "codex" || prepared.req.PackageRoot != pkg {
		t.Fatalf("prepare did not seal request/plan: %+v", got)
	}
	if prepared.req.RequiredComponents[0] != "mcp" {
		t.Fatal("caller slice mutation changed prepared request")
	}
}

func TestFailedPrepareRemovesOwnedSnapshot(t *testing.T) {
	ctx := testCtx(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	if err := os.MkdirAll(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "uap")
	eng, err := New(Config{StateRoot: state})
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: filepath.Join(base, "missing-client"), InstallationID: "00000000-0000-4000-8000-000000000047",
		OperationID: "failed-prepare",
	})
	if err == nil {
		t.Fatal("invalid package accepted")
	}
	tmp := filepath.Join(state, "tmp")
	entries, readErr := os.ReadDir(tmp)
	if readErr == nil && len(entries) != 0 {
		t.Fatalf("failed prepare left snapshots: %v", names(entries))
	}
}

func TestInvalidHelperRejectedBeforeStateFile(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "uap")
	missing := filepath.Join(base, "missing-helper")
	eng, err := New(Config{StateRoot: state, HelperExecutable: missing})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000048",
		OperationID: "missing-helper", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err == nil {
		t.Fatal("missing helper accepted")
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("invalid helper wrote state")
	}
}

func TestApplyUsesSealedSnapshotAfterSourceRemoved(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000049",
		OperationID: "deleted-source", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if err := os.RemoveAll(pkg); err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("apply after source delete: %+v %v", result, err)
	}
}

func TestPrepareRemoveRejectsCorruptArtifactBeforeDeactivate(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "uap")
	eng, err := New(Config{StateRoot: state, HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000050",
		OperationID: "remove-preflight", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	target := view.Installations[0].Bindings[0].TargetPath
	if err := os.WriteFile(filepath.Join(target, "tampered"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &countingRunner{}
	check, err := New(Config{StateRoot: state, HelperExecutable: probe, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	_, err = check.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "remove-corrupt", ExternalUninstalled: true,
	})
	if err == nil {
		t.Fatal("corrupt managed artifact accepted")
	}
	if runner.n != 0 {
		t.Fatalf("remove preflight deactivated client: %d", runner.n)
	}
}

func TestPrepareRemoveDoesNotRunHelper(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "uap")
	eng, err := New(Config{StateRoot: state, HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000051",
		OperationID: "remove-preview", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	runner := &countingRunner{}
	check, err := New(Config{StateRoot: state, HelperExecutable: probe, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	rm, err := check.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "remove-preview", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	if runner.n != 0 {
		t.Fatalf("prepare remove ran helper %d times", runner.n)
	}
}

func TestExampleModuleStaysExternal(t *testing.T) {
	mod, err := os.ReadFile(filepath.Join("example", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(mod)
	if strings.Contains(body, "replace ") || strings.Contains(body, "internal/") {
		t.Fatalf("example module is not external:\n%s", body)
	}
	src, err := os.ReadFile(filepath.Join("example", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, `"github.com/777genius/agent-notifications/install/uapinstaller"`) {
		t.Fatal("example does not import public installer API")
	}
	if strings.Contains(text, "internal/") || strings.Contains(text, "plugin-kit-ai/install/integrationctl/agentplugins/transaction") {
		t.Fatal("example imports raw Store/Kernel types")
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}
