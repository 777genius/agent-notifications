package uapinstaller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
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

func TestNewRejectsWindowsUNCStateRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC volume names are a Windows path form")
	}
	if _, err := New(Config{StateRoot: `\\server\share\uap`}); err == nil {
		t.Fatal("UNC StateRoot accepted")
	}
}

func TestPrepareRejectsTempRootOverlappingSource(t *testing.T) {
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
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), TempRoot: pkg})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000065",
		OperationID: "overlap-temp", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("overlapping temp: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(pkg, "plugin.json")); err != nil {
		t.Fatal("prepare mutated overlapping source")
	}
}

func TestPrepareRejectsCaseAliasTempRoot(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "Pkg")
	writePackage(t, pkg, probe)
	alias := filepath.Join(base, "pkg")
	info, err := os.Stat(pkg)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, aliasErr := os.Stat(alias)
	if aliasErr != nil || !os.SameFile(info, aliasInfo) {
		t.Skip("filesystem is case-sensitive")
	}
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), TempRoot: alias})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000066",
		OperationID: "case-alias-temp", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("case alias temp: %v", err)
	}
}

func TestPrepareRejectsSymlinkAliasTempRoot(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	link := filepath.Join(base, "tmp-link")
	if err := os.Symlink(pkg, link); err != nil {
		t.Skip("symlink not permitted")
	}
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), TempRoot: link})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000067",
		OperationID: "symlink-alias-temp", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("symlink alias temp: %v", err)
	}
}

func TestPrepareRejectsUnicodeAliasTempRoot(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "caf\u00e9")
	writePackage(t, pkg, probe)
	alias := filepath.Join(base, "cafe\u0301")
	info, err := os.Stat(pkg)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, aliasErr := os.Stat(alias)
	if aliasErr != nil || !os.SameFile(info, aliasInfo) {
		t.Skip("filesystem does not alias Unicode NFC/NFD names")
	}
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), TempRoot: alias})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000068",
		OperationID: "unicode-alias-temp", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unicode alias temp: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(pkg, "plugin.json")); err != nil {
		t.Fatal("prepare mutated unicode-aliased source")
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
	cancelled, err := eng.Apply(ctx, prepared, Decision{})
	if !errors.Is(err, ErrCancelled) || cancelled.Outcome != OutcomeCancelled || cancelled.Reason != "host cancelled" {
		t.Fatalf("cancelled apply: %+v %v", cancelled, err)
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
	if result.Client.ClientID != "codex" || strings.Join(result.Client.RequiredComponents, ",") != "mcp,skills" {
		t.Fatalf("client result: %+v", result.Client)
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

func TestInstallSecondClientPreservesFirst(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package source")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex config")
	claudeConfig := filepath.Join(base, "claude config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Runner: listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000062"
	install := func(client, config, op string) {
		t.Helper()
		prepared, err := eng.Prepare(ctx, Request{
			Operation: OpInstall, PackageRoot: pkg, ClientID: client, ClientConfigRoot: config,
			ClientExecutable: probe, InstallationID: id, OperationID: op,
			RequiredComponents: []string{"mcp", "skills"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		_ = prepared.Close()
	}
	install("codex", codexConfig, "codex-add")
	install("claude", claudeConfig, "claude-add")
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("both clients: %+v %v", view, err)
	}
	clients := map[string]bool{}
	for _, binding := range view.Installations[0].Bindings {
		clients[binding.ClientID] = true
	}
	if !clients["codex"] || !clients["claude"] {
		t.Fatalf("bindings: %+v", view.Installations[0].Bindings)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		InstallationID: id, OperationID: "claude-remove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, rm, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = rm.Close()
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("after claude remove: %+v %v", view, err)
	}
	clients = map[string]bool{}
	for _, binding := range view.Installations[0].Bindings {
		clients[binding.ClientID] = true
	}
	if !clients["codex"] {
		t.Fatal("codex binding lost")
	}
	if clients["claude"] {
		t.Fatal("claude binding survived")
	}
}

func isolateClientEnv(t *testing.T, base string) (home, codex, claude string) {
	t.Helper()
	home = filepath.Join(base, "env-home")
	codex = filepath.Join(base, "env-codex")
	claude = filepath.Join(base, "env-claude")
	for _, dir := range []string{home, codex, claude} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CLAUDE_HOME", claude)
	return home, codex, claude
}

func assertEnvSentinelsUnchanged(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "keep.txt" {
			t.Fatalf("env default %s mutated: %v", dir, names(entries))
		}
		got, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
		if err != nil || string(got) != "keep\n" {
			t.Fatalf("env sentinel %s: %s %v", dir, got, err)
		}
	}
}

func TestInstallUsesExplicitConfigRootNotEnv(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	envHome, envCodex, envClaude := isolateClientEnv(t, base)
	explicit := filepath.Join(base, "explicit-codex")
	if err := os.MkdirAll(explicit, 0700); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: explicit,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000063",
		OperationID: "explicit-profile", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if prepared.Plan().ConfigRoot != explicit {
		t.Fatalf("plan mixed env default: %+v", prepared.Plan())
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	claudeRoot := filepath.Join(base, "explicit-claude")
	if err := os.MkdirAll(claudeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	eng.cfg.Runner = listingRunner{configRoot: claudeRoot}
	second, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "claude", ClientConfigRoot: claudeRoot,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000063",
		OperationID: "explicit-claude", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if second.Plan().ConfigRoot != claudeRoot {
		t.Fatalf("claude plan mixed env default: %+v", second.Plan())
	}
	if _, err := eng.Apply(ctx, second, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	for _, binding := range view.Installations[0].Bindings {
		for _, envDir := range []string{envHome, envCodex, envClaude} {
			if binding.TargetPath == envDir || strings.HasPrefix(binding.TargetPath, envDir+string(os.PathSeparator)) {
				t.Fatalf("binding used env default %s: %+v", envDir, binding)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(claudeRoot, "skills")); err != nil {
		t.Fatalf("explicit claude profile was not written: %v", err)
	}
	assertEnvSentinelsUnchanged(t, envHome, envCodex, envClaude)
}

func TestInstallStoresHelperIdentityInManagedSource(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	wantDigest := hex.EncodeToString(sum[:])
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000064",
		OperationID: "helper-identity", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	plan := prepared.Plan()
	if plan.HelperVersion != "uap-installer-helper-v1" || plan.HelperDigest != wantDigest {
		t.Fatalf("plan helper identity: %+v want %s", plan, wantDigest)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	claudeRoot := filepath.Join(base, "claude")
	if err := os.MkdirAll(claudeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	eng.cfg.Runner = listingRunner{configRoot: claudeRoot}
	second, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "claude", ClientConfigRoot: claudeRoot,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000064",
		OperationID: "helper-claude", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if second.Plan().HelperDigest != wantDigest {
		t.Fatalf("claude plan helper digest: %+v want %s", second.Plan(), wantDigest)
	}
	if _, err := eng.Apply(ctx, second, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	var meta []byte
	for _, binding := range view.Installations[0].Bindings {
		if binding.ClientID != "claude" || binding.TargetPath == "" {
			continue
		}
		err := filepath.WalkDir(binding.TargetPath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || d.Name() != "metadata.json" {
				return walkErr
			}
			if !strings.Contains(filepath.ToSlash(path), managedstdio.RelativeDirectory) {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			meta = body
			return filepath.SkipAll
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(meta) == 0 {
		t.Fatal("managed helper metadata missing from Claude projection")
	}
	var stored struct {
		SHA256  string `json:"sha256"`
		Version string `json:"cliVersion"`
	}
	if err := json.Unmarshal(meta, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.SHA256 != wantDigest || stored.Version != plan.HelperVersion {
		t.Fatalf("managed helper identity: %+v plan=%+v", stored, plan)
	}
}

func TestPrepareDoesNotPersistWhenObservationSeamEnabled(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(base, "client config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "uap")
	eng, err := New(Config{StateRoot: root, HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	eng.persistObservations = true
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000061",
		OperationID: "observe-prepare", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("prepare persisted authoritative observations")
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("prepare acquired mutation lock")
	}
	if _, err := os.Lstat(eng.cfg.OperationsDir); !os.IsNotExist(err) {
		t.Fatal("prepare created operations journal dir")
	}
}

func TestPrepareMissingRequiredComponentsDoesNotCreateState(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	if err := os.RemoveAll(filepath.Join(pkg, "skills")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "uap")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000071",
		OperationID: "missing-skills", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("missing skills: %v", err)
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("incomplete prepare wrote state")
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
	if prepared.Plan().HelperVersion != "uap-installer-helper-v1" || prepared.Plan().HelperDigest != "" {
		t.Fatalf("helper identity without executable: %+v", prepared.Plan())
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

func TestApplyRefusesPendingJournalWithoutRecovering(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	eng, receipt := plantPendingJournal(t)
	pkg := filepath.Join(eng.cfg.StateRoot, "package")
	writePackage(t, pkg, probe)
	config := filepath.Join(eng.cfg.StateRoot, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000052",
		OperationID: "blocked-by-journal", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if !errors.Is(err, ErrRecoveryRequired) || result.Outcome != OutcomeRecovery {
		t.Fatalf("pending journal apply: %+v %v", result, err)
	}
	if len(result.NextActions) != 1 || result.NextActions[0].Kind != "recover" {
		t.Fatalf("recovery next actions: %+v", result.NextActions)
	}
	open, listErr := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if listErr != nil || len(open) != 1 || open[0].OperationID != receipt.OperationID {
		t.Fatalf("apply recovered journal: %+v %v", open, listErr)
	}
}

func TestCloseDuringApplyReturnsBusyWithoutReleasingSnapshot(t *testing.T) {
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
	started := make(chan struct{})
	release := make(chan struct{})
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		OnCommittedBinding: func(context.Context, BindingFacts) error {
			close(started)
			<-release
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000053",
		OperationID: "busy-handle", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	applyErr := make(chan error, 1)
	go func() {
		_, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
		applyErr <- err
	}()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		close(release)
		t.Fatal("apply did not reach committed-binding callback")
	}
	busy := make(chan error, 1)
	go func() {
		_, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
		busy <- err
	}()
	select {
	case err := <-busy:
		if !errors.Is(err, ErrHandleBusy) {
			close(release)
			t.Fatalf("concurrent apply: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("concurrent apply blocked on the in-flight mutex")
	}
	if err := prepared.Close(); !errors.Is(err, ErrHandleBusy) {
		close(release)
		t.Fatalf("close during apply: %v", err)
	}
	close(release)
	if err := <-applyErr; err != nil {
		t.Fatalf("in-flight apply: %v", err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); !errors.Is(err, ErrAlreadyApplied) {
		t.Fatalf("after busy apply: %v", err)
	}
}

func TestInstallDifferentDigestRejectedBeforeMutation(t *testing.T) {
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
	foreign := filepath.Join(config, "foreign.txt")
	if err := os.WriteFile(foreign, []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000054",
		OperationID: "first-revision", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	other := filepath.Join(base, "other")
	writePackage(t, other, probe)
	if err := os.WriteFile(filepath.Join(other, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = other
	req.OperationID = "other-revision"
	_, err = eng.Prepare(ctx, req)
	if !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("different digest prepare: %v", err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect after rejected revision: %+v %v", view, err)
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "keep\n" {
		t.Fatalf("foreign entry: %s %v", got, err)
	}
}

func TestProjectionSeamReplacesDeclaredServerArgs(t *testing.T) {
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
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		ServerName: "sample-notify",
		ProjectArgs: func(BindingFacts) ([]string, error) {
			return []string{"portable-launch", "--locator", "bound"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000055",
		OperationID: "projection-seam", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	body, err := os.ReadFile(filepath.Join(view.Installations[0].Bindings[0].TargetPath, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "portable-launch") || !strings.Contains(string(body), "bound") {
		t.Fatalf("projection args missing: %s", body)
	}
}

func TestRemoveRetainsPluginDataAfterLastClient(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000056",
		OperationID: "retain-data", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.DataRoot == "" {
		t.Fatal("install omitted data root")
	}
	sentinel := filepath.Join(result.Binding.DataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "retain-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := eng.Apply(ctx, rm, Decision{Confirmed: true})
	_ = rm.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !removed.DataRetained {
		t.Fatalf("remove result omitted data_retained: %+v", removed)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", got, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || !view.Installations[0].DataRetained {
		t.Fatalf("inspect retained: %+v %v", view, err)
	}
	if len(view.Installations[0].Bindings) != 0 {
		t.Fatalf("live binding survived last-client remove: %+v", view.Installations[0].Bindings)
	}
	if len(view.Installations[0].DataRoots) == 0 {
		t.Fatal("inspect omitted retained data roots")
	}
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	again, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "retain-remove-again", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Plan().NoChange {
		t.Fatalf("repeated remove plan: %+v", again.Plan())
	}
	absent, err := eng.Apply(ctx, again, Decision{Confirmed: true})
	_ = again.Close()
	if err != nil || absent.Outcome != OutcomeUnchanged || absent.Reason != "already_absent" || !absent.NoChange {
		t.Fatalf("already_absent: %+v %v", absent, err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("already_absent mutated state")
	}
}

func TestReinstallAfterRemoveUsesExplicitProfile(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexOld := filepath.Join(base, "codex-old")
	codexNew := filepath.Join(base, "codex-new")
	claudeConfig := filepath.Join(base, "claude-config")
	for _, dir := range []string{codexOld, codexNew, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Runner: listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000069"
	install := func(client, config, op string) Result {
		t.Helper()
		prepared, err := eng.Prepare(ctx, Request{
			Operation: OpInstall, PackageRoot: pkg, ClientID: client, ClientConfigRoot: config,
			ClientExecutable: probe, InstallationID: id, OperationID: op,
			RequiredComponents: []string{"mcp", "skills"},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
		_ = prepared.Close()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	codexFirst := install("codex", codexOld, "codex-first")
	if codexFirst.Binding.DataRoot == "" {
		t.Fatal("install omitted data root")
	}
	sentinel := filepath.Join(codexFirst.Binding.DataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	claudeFirst := install("claude", claudeConfig, "claude-first")
	if claudeFirst.Binding.BindingID == "" {
		t.Fatal("claude binding omitted")
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: codexOld, ClientExecutable: probe,
		InstallationID: id, OperationID: "codex-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, rm, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = rm.Close()
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("after codex remove: %+v %v", view, err)
	}
	if len(view.Installations[0].Bindings) != 1 || view.Installations[0].Bindings[0].ClientID != "claude" {
		t.Fatalf("claude binding lost: %+v", view.Installations[0].Bindings)
	}
	if view.Installations[0].Bindings[0].BindingID != claudeFirst.Binding.BindingID {
		t.Fatalf("claude binding revised: %s vs %s", claudeFirst.Binding.BindingID, view.Installations[0].Bindings[0].BindingID)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexNew,
		ClientExecutable: probe, InstallationID: id, OperationID: "codex-reinstall",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Plan().ConfigRoot != codexNew || prepared.Plan().InstallationID != id {
		t.Fatalf("reinstall plan reused old profile: %+v", prepared.Plan())
	}
	reinstalled, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil {
		t.Fatal(err)
	}
	if reinstalled.InstallationID != id {
		t.Fatalf("retained installation lost: %+v", reinstalled)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", got, err)
	}
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("reinstall inspect: %+v %v", view, err)
	}
	clients := map[string]string{}
	for _, binding := range view.Installations[0].Bindings {
		clients[binding.ClientID] = binding.BindingID
	}
	if clients["claude"] != claudeFirst.Binding.BindingID {
		t.Fatalf("claude binding changed after reinstall: %+v", view.Installations[0].Bindings)
	}
	if clients["codex"] == "" {
		t.Fatal("codex binding missing after reinstall")
	}
}

func TestAssessBlockAndUnavailableNeverBecomeAllow(t *testing.T) {
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
	for _, outcome := range []AssessmentOutcome{AssessmentBlock, AssessmentUnavailable} {
		eng, err := New(Config{
			StateRoot: filepath.Join(base, "uap-"+string(outcome)), HelperExecutable: probe,
			Assess: func(_ context.Context, _, digest string) (Assessment, error) {
				return Assessment{TreeDigest: digest, Outcome: outcome, Reason: string(outcome)}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = eng.Prepare(ctx, Request{
			Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
			ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000057",
			OperationID: "assess-" + string(outcome), RequiredComponents: []string{"mcp", "skills"},
		})
		if !errors.Is(err, ErrAssessmentRejected) {
			t.Fatalf("%s assess: %v", outcome, err)
		}
		if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
			t.Fatalf("%s assess wrote state", outcome)
		}
	}
}

func TestAssessDigestMismatchRefuses(t *testing.T) {
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
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Assess: func(context.Context, string, string) (Assessment, error) {
			return Assessment{TreeDigest: "sha256:" + strings.Repeat("ab", 32), Outcome: AssessmentAllow}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000058",
		OperationID: "assess-mismatch", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrAssessmentRejected) {
		t.Fatalf("mismatched assess: %v", err)
	}
}

func TestOldBridgeTreeDigestRefusesWithoutRewrite(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000059",
		OperationID: "bridge-install", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	store := statev2.Store{Path: eng.cfg.StateFile}
	state, err := store.Load()
	if err != nil || len(state.Installations) != 1 {
		t.Fatalf("load: %+v %v", state, err)
	}
	state.Installations[0].Source.TreeDigest = "sha256:" + strings.Repeat("cd", 32)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: req.InstallationID,
		OperationID: "bridge-rewrite", RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("old-bridge prepare: %v", err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("old-bridge rewrote state: %v", err)
	}
}

func TestProgressReportsCoarsePhases(t *testing.T) {
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
	var phases []ProgressPhase
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Progress: func(event ProgressEvent) { phases = append(phases, event.Phase) },
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000060",
		OperationID: "progress", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	joined := ""
	for _, phase := range phases {
		joined += string(phase) + ","
	}
	for _, want := range []ProgressPhase{ProgressPrepare, ProgressPreflight, ProgressStage, ProgressCommit, ProgressActivate, ProgressVerify, ProgressComplete} {
		if !strings.Contains(joined, string(want)+",") {
			t.Fatalf("missing phase %s in %s", want, joined)
		}
	}
}

func TestDiscoverDoesNotCreateStateOrRunHelper(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-state")
	runner := &countingRunner{}
	eng, err := New(Config{
		StateRoot: root, Runner: runner,
		HelperExecutable: filepath.Join(t.TempDir(), "helper"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := eng.Discover()
	if len(got) != 2 || got[0].ClientID != "claude" || got[1].ClientID != "codex" {
		t.Fatalf("discover: %+v", got)
	}
	if len(got[0].Scopes) != 1 || got[0].Scopes[0] != "user" {
		t.Fatalf("scopes: %+v", got)
	}
	if runner.n != 0 {
		t.Fatalf("discover ran helper %d times", runner.n)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("discover created state root")
	}
}

func TestDiscoverDoesNotCreateEnvHomes(t *testing.T) {
	base := t.TempDir()
	envHome, envCodex, envClaude := isolateClientEnv(t, base)
	root := filepath.Join(base, "missing-state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.Discover()
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("discover created state root")
	}
	assertEnvSentinelsUnchanged(t, envHome, envCodex, envClaude)
}

func TestDiscoverReportsExecutablePresenceWithoutExecuting(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	name := "claude"
	body := "#!/bin/sh\ntouch " + marker + "\n"
	if runtime.GOOS == "windows" {
		name = "claude.bat"
		body = "@echo off\r\necho.>" + marker + "\r\n"
	}
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".BAT;.COM;.EXE")
	}
	runner := &countingRunner{}
	root := filepath.Join(t.TempDir(), "missing-state")
	eng, err := New(Config{StateRoot: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	got := eng.Discover()
	if runner.n != 0 {
		t.Fatalf("discover ran helper %d times", runner.n)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("discover executed PATH candidate")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("discover created state root")
	}
	if len(got) != 2 || !got[0].ExecutablePresent || got[0].ClientID != "claude" {
		t.Fatalf("claude presence: %+v", got)
	}
	if got[0].ExecutablePath != path {
		t.Fatalf("claude path: %s want %s", got[0].ExecutablePath, path)
	}
	if got[1].ExecutablePresent || got[1].ExecutablePath != "" {
		t.Fatalf("codex should be absent: %+v", got[1])
	}
}

func TestDiscoverLstatsExplicitPathWithoutExecuting(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	path := filepath.Join(dir, "codex-probe")
	body := "#!/bin/sh\ntouch " + marker + "\n"
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "codex-probe.bat")
		body = "@echo off\r\necho.>" + marker + "\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "empty-path"))
	caller := map[string]string{"codex": path, "claude": filepath.Join(dir, "missing-claude")}
	root := filepath.Join(t.TempDir(), "missing-state")
	runner := &countingRunner{}
	eng, err := New(Config{StateRoot: root, Runner: runner, ClientExecutables: caller})
	if err != nil {
		t.Fatal(err)
	}
	caller["codex"] = filepath.Join(dir, "mutated")
	got := eng.Discover()
	if runner.n != 0 {
		t.Fatalf("discover ran helper %d times", runner.n)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("discover executed explicit path")
	}
	if got[0].ExecutablePresent || got[0].ExecutablePath != "" {
		t.Fatalf("missing explicit claude: %+v", got[0])
	}
	if !got[1].ExecutablePresent || got[1].ExecutablePath != path {
		t.Fatalf("explicit codex: %+v", got[1])
	}
}

func TestDiscoverReportsCurrentBindingsWithoutMutating(t *testing.T) {
	eng, _ := plantStateCommittedReceipt(t)
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	got := eng.Discover()
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("discover mutated state")
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("discover acquired mutation lock")
	}
	if len(got) != 2 || len(got[0].Bindings) != 0 {
		t.Fatalf("claude bindings: %+v", got[0])
	}
	if len(got[1].Bindings) != 1 || got[1].Bindings[0].ClientID != "codex" || got[1].Bindings[0].BindingID == "" {
		t.Fatalf("codex bindings: %+v", got[1])
	}
	got[1].Bindings[0].ClientID = "mutated"
	again := eng.Discover()
	if again[1].Bindings[0].ClientID != "codex" {
		t.Fatalf("caller mutated discover result: %+v", again[1])
	}
}

func TestRemoveAlreadyAbsentDoesNotRunHelperOrMutateState(t *testing.T) {
	eng := plantRetainedInstallation(t)
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	rm, err := eng.Prepare(testCtx(t), Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config,
		InstallationID: "00000000-0000-4000-8000-000000000070", OperationID: "already-absent",
		ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	if !rm.Plan().NoChange || rm.Plan().HelperVersion != "uap-installer-helper-v1" {
		t.Fatalf("already_absent plan: %+v", rm.Plan())
	}
	result, err := eng.Apply(testCtx(t), rm, Decision{Confirmed: true})
	if err != nil || result.Outcome != OutcomeUnchanged || result.Reason != "already_absent" || !result.NoChange || !result.DataRetained {
		t.Fatalf("already_absent apply: %+v %v", result, err)
	}
	if len(result.NextActions) != 0 {
		t.Fatalf("already_absent next actions: %+v", result.NextActions)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("already_absent mutated state")
	}
	if runner, ok := eng.cfg.Runner.(*countingRunner); ok && runner.n != 0 {
		t.Fatalf("already_absent ran helper %d times", runner.n)
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("already_absent acquired mutation lock file")
	}
}

func TestRemoveApplyPlanChangedWhenLiveTargetMoves(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000061",
		OperationID: "plan-install", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.Client.Materialization == "" || result.Client.ClientID != "codex" {
		t.Fatalf("install omitted client result: %+v", result.Client)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "plan-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	store := statev2.Store{Path: eng.cfg.StateFile}
	state, err := store.Load()
	if err != nil || len(state.Installations) != 1 {
		t.Fatalf("load: %+v %v", state, err)
	}
	for id, binding := range state.Installations[0].Clients {
		binding.TargetLocator = filepath.Join(base, "moved-target")
		state.Installations[0].Clients[id] = binding
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	planted, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := eng.Apply(ctx, rm, Decision{Confirmed: true})
	if !errors.Is(err, ErrPlanChanged) || moved.Outcome != OutcomeConflict || moved.Reason != "plan_changed" {
		t.Fatalf("stale remove apply: %+v %v", moved, err)
	}
	if len(moved.NextActions) != 1 || moved.NextActions[0].Kind != "reprepare" {
		t.Fatalf("plan_changed next actions: %+v", moved.NextActions)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(planted, after) {
		t.Fatalf("plan_changed mutated state: %v", err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect after plan_changed: %+v %v", view, err)
	}
	if view.Installations[0].Bindings[0].TargetPath != filepath.Join(base, "moved-target") {
		t.Fatalf("plan_changed mutated binding: %+v", view.Installations[0].Bindings[0])
	}
}

func TestCommittedBindingFailureKeepsManagedCommit(t *testing.T) {
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
	var phases []ProgressPhase
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Progress: func(event ProgressEvent) { phases = append(phases, event.Phase) },
		OnCommittedBinding: func(context.Context, BindingFacts) error {
			return errors.New("host seam refused after managed commit")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000063",
		OperationID: "commit-then-fail", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err == nil || result.Outcome != OutcomeIncomplete {
		t.Fatalf("post-commit failure: %+v %v", result, err)
	}
	if result.Outcome == OutcomeCancelled || result.Outcome == OutcomeCompleted {
		t.Fatalf("post-commit hid incomplete behind %s", result.Outcome)
	}
	if result.Client.Materialization == "" || result.Client.Materialization == string(domain.MaterializationAbsent) {
		t.Fatalf("managed commit missing: %+v", result.Client)
	}
	if result.Client.Activation == string(domain.ActivationActive) {
		t.Fatalf("activation claimed success: %+v", result.Client)
	}
	if result.Client.Materialization == result.Client.Activation {
		t.Fatalf("materialization and activation collapsed: %+v", result.Client)
	}
	if result.Binding.BindingID == "" || result.Binding.DataRoot == "" || result.Binding.TargetPath == "" {
		t.Fatalf("incomplete omitted binding facts: %+v", result.Binding)
	}
	if _, err := os.Lstat(result.Binding.TargetPath); err != nil {
		t.Fatalf("managed target rolled back: %v", err)
	}
	if _, err := os.Lstat(result.Binding.DataRoot); err != nil {
		t.Fatalf("PLUGIN_DATA rolled back: %v", err)
	}
	if len(result.NextActions) != 1 || result.NextActions[0].Kind != "activate" {
		t.Fatalf("activate next action: %+v", result.NextActions)
	}
	joined := ""
	for _, phase := range phases {
		joined += string(phase) + ","
	}
	if !strings.Contains(joined, string(ProgressCommit)+",") {
		t.Fatalf("missing commit phase in %s", joined)
	}
	if strings.Contains(joined, string(ProgressComplete)+",") {
		t.Fatalf("complete claimed after failed activation: %s", joined)
	}
	view, inspectErr := eng.Inspect(ctx)
	if inspectErr != nil || view.Recovery.Required {
		t.Fatalf("inspect after incomplete: %+v %v", view, inspectErr)
	}
	if len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("binding lost after incomplete: %+v", view)
	}
	got := view.Installations[0].Bindings[0]
	if got.Materialization != result.Client.Materialization || got.Activation != result.Client.Activation {
		t.Fatalf("inspect lifecycle diverged: %+v vs %+v", got, result.Client)
	}
}

func TestCancelAfterManagedCommitKeepsBinding(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		OnCommittedBinding: func(cbCtx context.Context, facts BindingFacts) error {
			if facts.BindingID == "" {
				return errors.New("committed binding missing identity")
			}
			cancel()
			return cbCtx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000064",
		OperationID: "cancel-after-commit", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err == nil || result.Outcome != OutcomeIncomplete {
		t.Fatalf("cancel after commit: %+v %v", result, err)
	}
	if result.Outcome == OutcomeCancelled {
		t.Fatal("after-effect cancel rolled back to cancelled")
	}
	if result.Client.Materialization == "" || result.Client.Materialization == string(domain.MaterializationAbsent) {
		t.Fatalf("after-effect cancel dropped materialization: %+v", result.Client)
	}
	if result.Binding.TargetPath == "" {
		t.Fatalf("after-effect cancel omitted target: %+v", result.Binding)
	}
	if _, err := os.Lstat(result.Binding.TargetPath); err != nil {
		t.Fatalf("after-effect cancel removed target: %v", err)
	}
	view, inspectErr := eng.Inspect(context.Background())
	if inspectErr != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("after-effect cancel lost installation: %+v %v", view, inspectErr)
	}
}

func TestCommittedBindingCallbackCannotReenterApply(t *testing.T) {
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
	var nestedApply, nestedInspect error
	var eng *Engine
	var prepared *PreparedOperation
	eng, err = New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		OnCommittedBinding: func(cbCtx context.Context, facts BindingFacts) error {
			if facts.BindingID == "" {
				return errors.New("committed binding missing identity")
			}
			_, nestedApply = eng.Apply(cbCtx, prepared, Decision{Confirmed: true})
			_, nestedInspect = eng.Inspect(cbCtx)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000065",
		OperationID: "no-reenter", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("outer apply: %+v %v", result, err)
	}
	if !errors.Is(nestedApply, ErrHandleBusy) {
		t.Fatalf("nested apply from callback: %v", nestedApply)
	}
	if nestedInspect != nil {
		t.Fatalf("inspect from callback mutated or failed: %v", nestedInspect)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("reentrant callback created a second commit: %+v %v", view, err)
	}
}

func TestInstalledTargetSurvivesSourceDeleteAfterApply(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000066",
		OperationID: "survive-source", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("install: %+v %v", result, err)
	}
	if err := os.RemoveAll(pkg); err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect after source delete: %+v %v", view, err)
	}
	target := view.Installations[0].Bindings[0].TargetPath
	if target == "" {
		t.Fatal("inspect omitted target")
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("installed launcher lost after source delete: %v", err)
	}
	if result.Binding.DataRoot != "" {
		if _, err := os.Lstat(result.Binding.DataRoot); err != nil {
			t.Fatalf("PLUGIN_DATA lost after source delete: %v", err)
		}
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}
