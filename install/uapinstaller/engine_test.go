package uapinstaller

import (
	"context"
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
