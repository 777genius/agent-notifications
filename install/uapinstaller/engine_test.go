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
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
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

func TestNewInspectDiscoverDoNotInvokeAssess(t *testing.T) {
	calls := 0
	root := filepath.Join(t.TempDir(), "missing-state")
	eng, err := New(Config{
		StateRoot: root,
		Assess: func(context.Context, string, string) (Assessment, error) {
			calls++
			return Assessment{Outcome: AssessmentBlock, Reason: "constructor-scanner"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("New invoked Assess %d times", calls)
	}
	if _, err := eng.Inspect(testCtx(t)); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if calls != 0 {
		t.Fatalf("Inspect invoked Assess %d times", calls)
	}
	_ = eng.Discover()
	if calls != 0 {
		t.Fatalf("Discover invoked Assess %d times", calls)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("read-only Assess paths created state")
	}
}

func TestPrepareUnknownAndGroupOperationsDoNotMutate(t *testing.T) {
	ctx := testCtx(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
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
	for _, op := range []Operation{"", "install-group", "update-group", "repair-group", "remove-group", "switch"} {
		_, err := eng.Prepare(ctx, Request{Operation: op, ClientID: "codex", ClientConfigRoot: config})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%q: %v", op, err)
		}
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("unknown operation created state")
	}
}

func TestPrepareGroupMixedPackageRootsUnpublished(t *testing.T) {
	ctx := testCtx(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: filepath.Join(base, "uap")})
	if err != nil {
		t.Fatal(err)
	}
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: filepath.Join(base, "package-a"),
		InstallationID: "00000000-0000-4000-8000-0000000000b8", OperationID: "mixed-roots",
		ClientExecutable: filepath.Join(base, "probe"),
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, PackageRoot: filepath.Join(base, "package-a")},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, PackageRoot: filepath.Join(base, "package-b")},
		},
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("mixed package roots: %v", err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: filepath.Join(base, "package-a"),
		InstallationID: "00000000-0000-4000-8000-0000000000b8", OperationID: "mixed-update",
		ClientExecutable: filepath.Join(base, "probe"),
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, PackageRoot: filepath.Join(base, "package-a")},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, PackageRoot: filepath.Join(base, "package-b")},
		},
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("mixed update package roots: %v", err)
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("mixed package roots created state")
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
	if view.Installations[0].TreeDigest == "" || view.Installations[0].TreeDigest != prepared.Plan().TreeDigest {
		t.Fatalf("inspect omitted source digest: plan=%s inspect=%s", prepared.Plan().TreeDigest, view.Installations[0].TreeDigest)
	}
	recovered, err := eng.Recover(ctx, view)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Outcome != OutcomeUnchanged && recovered.Outcome != OutcomeCompleted {
		t.Fatalf("recover: %+v", recovered)
	}
	reserved, err := eng.ReserveIdentity(IdentityRequest{ClientID: "codex", Allocate: false})
	if err != nil || reserved.InstallationID != req.InstallationID || reserved.BindingID != view.Installations[0].Bindings[0].BindingID {
		t.Fatalf("reserve existing: %+v %v", reserved, err)
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
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: req.InstallationID, OperationID: "sample-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = updated.Close() }()
	update, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if update.Outcome != OutcomeCompleted && update.Outcome != OutcomeUnchanged {
		t.Fatalf("update: %+v", update)
	}
	if update.InstallationID != req.InstallationID {
		t.Fatalf("update changed installation: %s", update.InstallationID)
	}
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: req.InstallationID, OperationID: "sample-repair",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repaired.Close() }()
	repair, err := eng.Apply(ctx, repaired, Decision{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if repair.Outcome != OutcomeCompleted && repair.Outcome != OutcomeUnchanged {
		t.Fatalf("repair: %+v", repair)
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

func TestInstallGroupBothThenRemoveOne(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-0000000000b1"
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "group-install",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Plan().Targets) != 2 {
		t.Fatalf("group plan: %+v", prepared.Plan())
	}
	got, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("group install: %+v %v", got, err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("group result targets: %+v", got.Targets)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("both clients: %+v %v", view, err)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-remove-one",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := eng.Apply(ctx, rm, Decision{Confirmed: true})
	_ = rm.Close()
	if err != nil || removed.Outcome != OutcomeCompleted {
		t.Fatalf("group remove: %+v %v", removed, err)
	}
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("after group remove: %+v %v", view, err)
	}
	if len(view.Installations[0].Bindings) != 0 {
		t.Fatalf("bindings survived group remove: %+v", view.Installations[0].Bindings)
	}
}

func TestInstallGroupRepeatUnchangedReportsBothTargets(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-0000000000b3"
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "group-repeat-install",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	req.OperationID = "group-repeat-again"
	again, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Plan().NoChange {
		t.Fatalf("repeat group plan mutated: %+v", again.Plan())
	}
	got, err := eng.Apply(ctx, again, Decision{Confirmed: true})
	_ = again.Close()
	if err != nil || got.Outcome != OutcomeUnchanged || !got.NoChange {
		t.Fatalf("repeat group apply: %+v %v", got, err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("repeat group omitted targets: %+v", got.Targets)
	}
	seen := map[string]bool{}
	for _, target := range got.Targets {
		seen[target.ClientID] = true
		if target.BindingID == "" {
			t.Fatalf("repeat group omitted binding: %+v", target)
		}
	}
	if !seen["claude"] || !seen["codex"] {
		t.Fatalf("repeat group clients: %+v", got.Targets)
	}
}

func TestRepairGroupMissingOneDoesNotMutate(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-0000000000b2"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "group-repair-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = installed.Close()
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, InstallationID: id, OperationID: "group-repair-missing",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing sibling repair: %v", err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("missing sibling repair mutated state")
	}
}

func TestInstallGroupAddsMissingSiblingSameDigest(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-0000000000b4"
	first, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "group-add-first",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, first, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	added, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "group-add-second",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, added, Decision{Confirmed: true})
	_ = added.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("group add second: %+v %v", got, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || view.Installations[0].InstallationID != id {
		t.Fatalf("after group add: %+v %v", view, err)
	}
	if len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("missing sibling not added: %+v", view.Installations[0].Bindings)
	}
}

func TestInstallGroupDifferentDigestWhenLiveDoesNotMutate(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-0000000000b5"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "group-digest-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = installed.Close()
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "group-digest-rewrite",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("group install rewrite: %v", err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("group install rewrite mutated state")
	}
}

func TestInstallGroupSecondHostSeamFailureKeepsFirstClient(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Runner: listingRunner{configRoot: claudeConfig},
		OnCommittedBinding: func(_ context.Context, facts BindingFacts) error {
			if facts.ClientID == "claude" {
				return errors.New("host seam refused claude after managed commit")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-0000000000b6"
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "group-partial",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err == nil || got.Outcome != OutcomeIncomplete {
		t.Fatalf("partial group: %+v %v", got, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect after partial group: %+v %v", view, err)
	}
	seen := map[string]bool{}
	for _, binding := range view.Installations[0].Bindings {
		seen[binding.ClientID] = true
	}
	if !seen["codex"] {
		t.Fatalf("first client rolled back after second seam failure: %+v", view.Installations[0].Bindings)
	}
}

func TestInstallGroupRetryAfterSecondHostSeamFailureReconcilesWithoutDuplicatingFirst(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	failClaude := true
	runner := &capturingRunner{inner: listingRunner{configRoot: claudeConfig}}
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe, Runner: runner,
		OnCommittedBinding: func(_ context.Context, facts BindingFacts) error {
			if facts.BindingID == "" || facts.TargetPath == "" || facts.DataRoot == "" {
				return errors.New("committed binding missing identity")
			}
			calls = append(calls, facts.ClientID)
			if failClaude && facts.ClientID == "claude" {
				return errors.New("host seam refused claude after managed commit")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-0000000000b7"
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "group-partial-retry",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err == nil || first.Outcome != OutcomeIncomplete {
		t.Fatalf("partial group: %+v %v", first, err)
	}
	pluginMutations := countPluginMutations(runner.calls)
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect after partial group: %+v %v", view, err)
	}
	codexBefore := inspectedBinding(t, view, "codex")
	if codexBefore.BindingID == "" || codexBefore.DataRoot == "" {
		t.Fatalf("first client missing after partial group: %+v", view.Installations[0].Bindings)
	}
	claudeBefore := inspectedBinding(t, view, "claude")
	if claudeBefore.BindingID == "" || claudeBefore.DataRoot == "" {
		t.Fatalf("second client rolled back after seam failure: %+v", view.Installations[0].Bindings)
	}
	beforeRetry := append([]string(nil), calls...)
	if _, inspectErr := eng.Inspect(ctx); inspectErr != nil {
		t.Fatal(inspectErr)
	}
	preview, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != len(beforeRetry) {
		t.Fatalf("inspect/prepare executed host callback: %q -> %q", beforeRetry, calls)
	}
	failClaude = false
	retried, err := eng.Apply(ctx, preview, Decision{Confirmed: true})
	_ = preview.Close()
	if err != nil || (retried.Outcome != OutcomeCompleted && retried.Outcome != OutcomeUnchanged) {
		t.Fatalf("retry apply: %+v %v", retried, err)
	}
	var claudeCalls, codexCalls int
	for _, client := range calls {
		switch client {
		case "claude":
			claudeCalls++
		case "codex":
			codexCalls++
		}
	}
	if claudeCalls < 2 {
		t.Fatalf("retry skipped claude committed-binding reconciliation: %q", calls)
	}
	if codexCalls < 1 {
		t.Fatalf("first client callback missing: %q", calls)
	}
	if countPluginMutations(runner.calls) != pluginMutations {
		t.Fatalf("retry duplicated first client commit: %+v", runner.calls)
	}
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect after retry: %+v %v", view, err)
	}
	codexAfter := inspectedBinding(t, view, "codex")
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.DataRoot != codexBefore.DataRoot {
		t.Fatalf("retry rewrote first client: %+v -> %+v", codexBefore, codexAfter)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.DataRoot != claudeBefore.DataRoot {
		t.Fatalf("retry rewrote second client commit: %+v -> %+v", claudeBefore, claudeAfter)
	}
	if claudeAfter.Activation == string(domain.ActivationFailed) || claudeAfter.Activation == string(domain.ActivationPrepared) || claudeAfter.Activation == "" {
		t.Fatalf("retry left claude unactivated: %+v", claudeAfter)
	}
	if len(calls) != claudeCalls+codexCalls {
		t.Fatalf("inspect after retry executed host callback: %q", calls)
	}
}

func inspectedBinding(t *testing.T, view Inspection, clientID string) InspectedBinding {
	t.Helper()
	for _, installation := range view.Installations {
		for _, binding := range installation.Bindings {
			if binding.ClientID == clientID {
				return binding
			}
		}
	}
	t.Fatalf("missing %s binding: %+v", clientID, view)
	return InspectedBinding{}
}

type capturingRunner struct {
	inner listingRunner
	calls [][]string
}

func (r *capturingRunner) Run(ctx context.Context, cmd ports.Command) (ports.CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), cmd.Argv...))
	return r.inner.Run(ctx, cmd)
}

func countPluginMutations(calls [][]string) int {
	n := 0
	for _, argv := range calls {
		if containsArgSeq(argv, "plugin", "marketplace", "add") || containsArgSeq(argv, "plugin", "add") {
			n++
		}
	}
	return n
}

func bothClientTargets(codexConfig, claudeConfig, probe string) []ClientTarget {
	return []ClientTarget{
		{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
		{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
	}
}

func newBothClientSandbox(t *testing.T) (context.Context, *Engine, *capturingRunner, string, string, string, string) {
	t.Helper()
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	runner := &capturingRunner{inner: listingRunner{configRoot: claudeConfig}}
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe, Runner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, eng, runner, pkg, probe, codexConfig, claudeConfig
}

func installBothClients(t *testing.T, ctx context.Context, eng *Engine, pkg, probe, id, op string, targets []ClientTarget) Result {
	t.Helper()
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: op,
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe, Targets: targets,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("group install: %+v %v", got, err)
	}
	return got
}

func TestRecoverAfterPartialGroupDoesNotInvokeHostCallback(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe,
		Runner: listingRunner{configRoot: claudeConfig},
		OnCommittedBinding: func(_ context.Context, facts BindingFacts) error {
			calls++
			if facts.ClientID == "claude" {
				return errors.New("host seam refused claude after managed commit")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: "00000000-0000-4000-8000-0000000000b9",
		OperationID: "group-recover-partial", RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err == nil || got.Outcome != OutcomeIncomplete {
		t.Fatalf("partial group: %+v %v", got, err)
	}
	before := calls
	beforeState, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterState, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(beforeState, afterState) {
		t.Fatal("inspect after partial group mutated state")
	}
	if len(view.Installations) != 1 {
		t.Fatalf("inspect after partial group: %+v", view)
	}
	if inspectedBinding(t, view, "codex").BindingID == "" {
		t.Fatalf("first client missing after partial group: %+v", view.Installations[0].Bindings)
	}
	if _, err := eng.Recover(ctx, view); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatalf("recover invoked host callback: %d -> %d", before, calls)
	}
}

func TestPrepareGroupDeniedApplyWritesNoState(t *testing.T) {
	ctx, eng, runner, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	called := false
	eng.cfg.OnCommittedBinding = func(context.Context, BindingFacts) error {
		called = true
		return errors.New("denied apply must not invoke callbacks")
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: "00000000-0000-4000-8000-0000000000c1",
		OperationID: "group-denied", RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: bothClientTargets(codexConfig, claudeConfig, probe),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("group prepare wrote state")
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("group prepare acquired mutation lock")
	}
	before := len(runner.calls)
	cancelled, err := eng.Apply(ctx, prepared, Decision{})
	if !errors.Is(err, ErrCancelled) || cancelled.Outcome != OutcomeCancelled || cancelled.Reason != "host cancelled" {
		t.Fatalf("denied group apply: %+v %v", cancelled, err)
	}
	if called {
		t.Fatal("denied group apply invoked committed-binding callback")
	}
	if len(runner.calls) != before {
		t.Fatalf("denied group apply ran helper: %d -> %d", before, len(runner.calls))
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("denied group apply wrote state")
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("denied group apply acquired mutation lock")
	}
}

func TestDiscoverReportsBothClientsAfterGroupInstallWithoutMutating(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c2"
	installBothClients(t, ctx, eng, pkg, probe, id, "group-discover", bothClientTargets(codexConfig, claudeConfig, probe))
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	got := eng.Discover()
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("discover mutated state")
	}
	if len(got) != 2 || got[0].ClientID != "claude" || got[1].ClientID != "codex" {
		t.Fatalf("discover clients: %+v", got)
	}
	if len(got[0].Bindings) != 1 || got[0].Bindings[0].BindingID == "" {
		t.Fatalf("claude discover bindings: %+v", got[0])
	}
	if len(got[1].Bindings) != 1 || got[1].Bindings[0].BindingID == "" {
		t.Fatalf("codex discover bindings: %+v", got[1])
	}
	got[0].Bindings[0].ClientID = "mutated"
	again := eng.Discover()
	if again[0].Bindings[0].ClientID != "claude" {
		t.Fatalf("caller mutated discover result: %+v", again[0])
	}
}

func TestInspectReportsBothClientsAfterGroupInstallWithoutMutating(t *testing.T) {
	ctx, eng, runner, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c7"
	installBothClients(t, ctx, eng, pkg, probe, id, "group-inspect", bothClientTargets(codexConfig, claudeConfig, probe))
	beforeCalls := len(runner.calls)
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inspect mutated state")
	}
	if len(runner.calls) != beforeCalls {
		t.Fatalf("inspect ran helper: %d -> %d", beforeCalls, len(runner.calls))
	}
	if view.StateRoot != eng.cfg.StateRoot || view.Recovery.Required || len(view.Installations) != 1 {
		t.Fatalf("inspect: %+v", view)
	}
	if view.Installations[0].InstallationID != id || view.Installations[0].TreeDigest == "" {
		t.Fatalf("inspect installation: %+v", view.Installations[0])
	}
	claude := inspectedBinding(t, view, "claude")
	codex := inspectedBinding(t, view, "codex")
	if claude.BindingID == "" || claude.Profile != claudeConfig || claude.TargetPath == "" || claude.DataRoot == "" || claude.TreeDigest == "" {
		t.Fatalf("claude inspect: %+v", claude)
	}
	if codex.BindingID == "" || codex.Profile != codexConfig || codex.TargetPath == "" || codex.DataRoot == "" || codex.TreeDigest == "" {
		t.Fatalf("codex inspect: %+v", codex)
	}
	if claude.TreeDigest != view.Installations[0].TreeDigest || codex.TreeDigest != view.Installations[0].TreeDigest {
		t.Fatalf("same-revision inspect digests: installation=%s claude=%s codex=%s", view.Installations[0].TreeDigest, claude.TreeDigest, codex.TreeDigest)
	}
	view.Installations[0].Bindings[0].ClientID = "mutated"
	again, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inspectedBinding(t, again, "claude").ClientID != "claude" || inspectedBinding(t, again, "codex").ClientID != "codex" {
		t.Fatalf("caller mutated inspect result: %+v", again.Installations[0].Bindings)
	}
	recovered, err := eng.Recover(ctx, again)
	if err != nil || recovered.Outcome != OutcomeUnchanged || recovered.Reason != "already_recovered" {
		t.Fatalf("recover clean group: %+v %v", recovered, err)
	}
	if len(runner.calls) != beforeCalls {
		t.Fatalf("recover ran helper: %d -> %d", beforeCalls, len(runner.calls))
	}
	final, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, final) {
		t.Fatal("recover mutated clean group state")
	}
}

func TestPrepareRemoveGroupDoesNotDeactivateBeforeApply(t *testing.T) {
	ctx, eng, runner, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c3"
	targets := bothClientTargets(codexConfig, claudeConfig, probe)
	installBothClients(t, ctx, eng, pkg, probe, id, "group-remove-preview", targets)
	before := len(runner.calls)
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-remove-preview",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	if len(rm.Plan().Targets) != 2 || rm.Plan().NoChange {
		t.Fatalf("group remove plan: %+v", rm.Plan())
	}
	if len(runner.calls) != before {
		t.Fatalf("group remove prepare ran helper: %d -> %d %+v", before, len(runner.calls), runner.calls[before:])
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("prepare remove mutated bindings: %+v %v", view, err)
	}
	cancelled, err := eng.Apply(ctx, rm, Decision{})
	if !errors.Is(err, ErrCancelled) || cancelled.Outcome != OutcomeCancelled {
		t.Fatalf("denied group remove: %+v %v", cancelled, err)
	}
	if len(runner.calls) != before {
		t.Fatalf("denied group remove ran helper: %d -> %d", before, len(runner.calls))
	}
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("denied group remove mutated bindings: %+v %v", view, err)
	}
}

func TestPrepareRemoveGroupRejectsCorruptArtifactBeforeDeactivate(t *testing.T) {
	ctx, eng, runner, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000e4"
	targets := bothClientTargets(codexConfig, claudeConfig, probe)
	installBothClients(t, ctx, eng, pkg, probe, id, "group-remove-corrupt-install", targets)
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	target := inspectedBinding(t, view, "claude").TargetPath
	if err := os.WriteFile(filepath.Join(target, "tampered"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	_, err = eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-remove-corrupt",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err == nil {
		t.Fatal("corrupt managed artifact accepted")
	}
	if len(runner.calls) != before {
		t.Fatalf("group remove preflight deactivated client: %d -> %d", before, len(runner.calls))
	}
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("corrupt group remove mutated bindings: %+v %v", view, err)
	}
}

func TestApplyStaleGroupRemovePlanChangedPreservesSibling(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c4"
	installBothClients(t, ctx, eng, pkg, probe, id, "group-stale-install", bothClientTargets(codexConfig, claudeConfig, probe))
	stale, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-stale-remove",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stale.Close() }()
	live, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		InstallationID: id, OperationID: "claude-live-remove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, live, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = live.Close()
	moved, err := eng.Apply(ctx, stale, Decision{Confirmed: true})
	if !errors.Is(err, ErrPlanChanged) || moved.Outcome != OutcomeConflict || moved.Reason != "plan_changed" {
		t.Fatalf("stale group remove: %+v %v", moved, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect after stale group remove: %+v %v", view, err)
	}
	seen := map[string]bool{}
	for _, binding := range view.Installations[0].Bindings {
		seen[binding.ClientID] = true
	}
	if !seen["codex"] {
		t.Fatal("stale group remove lost sibling")
	}
	if seen["claude"] {
		t.Fatal("live claude remove did not take effect")
	}
}

func TestRemoveGroupOneAlreadyAbsentRemovesOnlyLive(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c5"
	installBothClients(t, ctx, eng, pkg, probe, id, "group-mixed-remove-install", bothClientTargets(codexConfig, claudeConfig, probe))
	live, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		InstallationID: id, OperationID: "claude-first-remove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, live, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = live.Close()
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-mixed-remove",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	if len(rm.Plan().Targets) != 2 {
		t.Fatalf("mixed remove plan: %+v", rm.Plan())
	}
	byClient := map[string]PlanTarget{}
	for _, target := range rm.Plan().Targets {
		byClient[target.ClientID] = target
	}
	if !byClient["claude"].NoChange || byClient["codex"].NoChange {
		t.Fatalf("mixed remove classification: %+v", rm.Plan().Targets)
	}
	got, err := eng.Apply(ctx, rm, Decision{Confirmed: true})
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("mixed group remove: %+v %v", got, err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("mixed group remove targets: %+v", got.Targets)
	}
	seen := map[string]ClientResult{}
	for _, target := range got.Targets {
		seen[target.ClientID] = target
	}
	if seen["claude"].Materialization != string(domain.MaterializationAbsent) {
		t.Fatalf("absent target not classified: %+v", got.Targets)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect after mixed group remove: %+v %v", view, err)
	}
	if len(view.Installations[0].Bindings) != 0 {
		t.Fatalf("live sibling survived mixed group remove: %+v", view.Installations[0].Bindings)
	}
}

func TestRemoveGroupBothAlreadyAbsentDoesNotCreateJournal(t *testing.T) {
	eng := plantRetainedInstallation(t)
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	codexConfig := filepath.Join(t.TempDir(), "codex-config")
	claudeConfig := filepath.Join(t.TempDir(), "claude-config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	rm, err := eng.Prepare(testCtx(t), Request{
		Operation: OpRemove, InstallationID: "00000000-0000-4000-8000-000000000070",
		OperationID: "group-both-absent",
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	if !rm.Plan().NoChange || len(rm.Plan().Targets) != 2 {
		t.Fatalf("both-absent plan: %+v", rm.Plan())
	}
	got, err := eng.Apply(testCtx(t), rm, Decision{Confirmed: true})
	if err != nil || got.Outcome != OutcomeUnchanged || got.Reason != "already_absent" || !got.NoChange || !got.DataRetained {
		t.Fatalf("both-absent apply: %+v %v", got, err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("both-absent targets: %+v", got.Targets)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("both-absent mutated state")
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("both-absent acquired mutation lock")
	}
	if _, err := os.Lstat(eng.cfg.OperationsDir); !os.IsNotExist(err) {
		t.Fatal("both-absent created operations journal dir")
	}
	if runner, ok := eng.cfg.Runner.(*countingRunner); ok && runner.n != 0 {
		t.Fatalf("both-absent ran helper %d times", runner.n)
	}
}

func TestInstallSameDigestDifferentDirectoryAddsSecondClient(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package source")
	acquired := filepath.Join(base, "acquired copy")
	writePackage(t, pkg, probe)
	writePackage(t, acquired, probe)
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
	id := "00000000-0000-4000-8000-000000000084"
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "codex-original",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Close()
	second, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: acquired, ClientID: "claude", ClientConfigRoot: claudeConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "claude-copy",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatalf("same digest different directory: %v", err)
	}
	if _, err := eng.Apply(ctx, second, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("both clients: %+v %v", view, err)
	}
}

func TestRemoveMissingSiblingIsAlreadyAbsent(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000068"
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
	rmClaude, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		InstallationID: id, OperationID: "claude-remove",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, rmClaude, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = rmClaude.Close()
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		InstallationID: id, OperationID: "claude-absent",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = absent.Close() }()
	if !absent.Plan().NoChange {
		t.Fatalf("missing sibling plan: %+v", absent.Plan())
	}
	got, err := eng.Apply(ctx, absent, Decision{Confirmed: true})
	if err != nil || got.Outcome != OutcomeUnchanged || got.Reason != "already_absent" || !got.NoChange {
		t.Fatalf("missing sibling remove: %+v %v", got, err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("missing sibling already_absent mutated state")
	}
	rmCodex, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe,
		InstallationID: id, OperationID: "codex-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, rmCodex, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = rmCodex.Close()
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("after both removes: %+v %v", view, err)
	}
	if len(view.Installations[0].Bindings) != 0 {
		t.Fatalf("live binding survived: %+v", view.Installations[0].Bindings)
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
	called := false
	eng.cfg.OnCommittedBinding = func(context.Context, BindingFacts) error {
		called = true
		return errors.New("observation seam must not invoke callbacks")
	}
	view, err := eng.Inspect(ctx)
	if err != nil || view.Recovery.Required {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("inspect persisted authoritative observations")
	}
	cancelled, err := eng.Apply(ctx, prepared, Decision{})
	if !errors.Is(err, ErrCancelled) || cancelled.Outcome != OutcomeCancelled {
		t.Fatalf("denied apply: %+v %v", cancelled, err)
	}
	if called {
		t.Fatal("denied apply invoked committed-binding callback")
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("denied apply wrote state")
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("denied apply acquired mutation lock")
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

func TestRequiredComponentsDoesNotStripAuthoredSkills(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000072",
		OperationID: "required-mcp-only", RequiredComponents: []string{"mcp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("mcp-only required install: %+v %v", got, err)
	}
	if strings.Join(got.Client.RequiredComponents, ",") != "mcp" {
		t.Fatalf("required components rewritten: %+v", got.Client.RequiredComponents)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	target := view.Installations[0].Bindings[0].TargetPath
	if _, err := os.Lstat(filepath.Join(target, "mcp.json")); err != nil {
		t.Fatalf("mcp projection missing: %v", err)
	}
	foundSkill := false
	if err := filepath.WalkDir(target, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == "SKILL.md" {
			foundSkill = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !foundSkill {
		t.Fatal("required mcp-only install stripped authored skills")
	}
}

func TestUpdateRejectedWithoutExistingBinding(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(testCtx(t), Request{
		Operation: OpUpdate, ClientID: "codex", ClientConfigRoot: root, PackageRoot: root,
		InstallationID: "00000000-0000-4000-8000-000000000086",
	})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("update without binding: %v", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("rejected update created state")
	}
}

func TestUpdateChangesLiveRevision(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000087"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	_ = installed.Close()
	if err != nil || first.Outcome != OutcomeCompleted {
		t.Fatalf("install: %+v %v", first, err)
	}
	before := installed.Plan().TreeDigest
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-blocked-install",
		RequiredComponents: []string{"mcp", "skills"},
	}); !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("install still upserts: %v", err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-apply",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	digest := updated.Plan().TreeDigest
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("update: %+v %v", got, err)
	}
	if digest == "" || digest == before {
		t.Fatalf("update kept old digest %s", digest)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || view.Installations[0].TreeDigest != digest {
		t.Fatalf("inspect after update: %+v %v", view, err)
	}
}

func TestUpdateCopiedPackageWithNewDigest(t *testing.T) {
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
		Runner: listingRunner{configRoot: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000098"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "claude", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "copied-update-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	before := installed.Plan().TreeDigest
	_ = installed.Close()
	if err != nil || first.Outcome != OutcomeCompleted || before == "" {
		t.Fatalf("install: %+v %v", first, err)
	}
	store := statev2.Store{Path: eng.cfg.StateFile}
	beforeState, err := store.Load()
	if err != nil || len(beforeState.Installations) != 1 {
		t.Fatalf("load: %+v %v", beforeState, err)
	}
	recordedCanonical := beforeState.Installations[0].Source.CanonicalSource
	recordedRequested := beforeState.Installations[0].Source.RequestedSource
	if recordedCanonical == "" {
		t.Fatal("missing recorded canonical source")
	}
	other := filepath.Join(base, "other package")
	writePackage(t, other, probe)
	if err := os.WriteFile(filepath.Join(other, "skills", "sample-notify", "SKILL.md"), []byte("---\nname: sample-notify\ndescription: Revised\n---\nCopied revision.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: other, ClientID: "claude", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "copied-update-blocked-install",
		RequiredComponents: []string{"mcp", "skills"},
	}); !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("install still upserts copied digest: %v", err)
	}
	if _, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: other, ClientID: "claude", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "copied-update-blocked-repair",
		RequiredComponents: []string{"mcp", "skills"},
	}); !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("repair rewrote copied digest: %v", err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: other, ClientID: "claude", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "copied-update-apply",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	digest := updated.Plan().TreeDigest
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("update copied package: %+v %v", got, err)
	}
	if digest == "" || digest == before {
		t.Fatalf("update kept old digest %s", digest)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || view.Installations[0].TreeDigest != digest {
		t.Fatalf("inspect after copied update: %+v %v", view, err)
	}
	afterState, err := store.Load()
	if err != nil || len(afterState.Installations) != 1 {
		t.Fatalf("load after update: %+v %v", afterState, err)
	}
	if afterState.Installations[0].Source.CanonicalSource != recordedCanonical {
		t.Fatalf("update rewrote canonical source: %s vs %s", recordedCanonical, afterState.Installations[0].Source.CanonicalSource)
	}
	if afterState.Installations[0].Source.RequestedSource != recordedRequested {
		t.Fatalf("update rewrote requested source: %s vs %s", recordedRequested, afterState.Installations[0].Source.RequestedSource)
	}
}

func TestSwitchRetainedCopiedPackageWithNewDigest(t *testing.T) {
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
	id := "00000000-0000-4000-8000-0000000000a1"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "retained-switch-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	before := installed.Plan().TreeDigest
	dataRoot := first.Binding.DataRoot
	_ = installed.Close()
	if err != nil || first.Outcome != OutcomeCompleted || dataRoot == "" {
		t.Fatalf("install: %+v %v", first, err)
	}
	sentinel := filepath.Join(dataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SwitchRetained(ctx, Request{
		PackageRoot: pkg, InstallationID: id, OperationID: "retained-switch-live",
	}, Decision{Confirmed: true}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("live switch: %v", err)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: id, OperationID: "retained-switch-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := eng.Apply(ctx, rm, Decision{Confirmed: true})
	_ = rm.Close()
	if err != nil || removed.Outcome != OutcomeCompleted || !removed.DataRetained {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	other := filepath.Join(base, "other-package")
	writePackage(t, other, probe)
	if err := os.WriteFile(filepath.Join(other, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	switched, err := eng.SwitchRetained(ctx, Request{
		PackageRoot: other, InstallationID: id, OperationID: "retained-switch-apply",
	}, Decision{Confirmed: true})
	if err != nil || switched.Outcome != OutcomeCompleted {
		t.Fatalf("switch retained: %+v %v", switched, err)
	}
	if switched.Binding.TreeDigest == "" || switched.Binding.TreeDigest == before {
		t.Fatalf("switch kept old digest %s", switched.Binding.TreeDigest)
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || !view.Installations[0].DataRetained {
		t.Fatalf("inspect after switch: %+v %v", view, err)
	}
	if view.Installations[0].TreeDigest != switched.Binding.TreeDigest {
		t.Fatalf("inspect digest: %s vs %s", view.Installations[0].TreeDigest, switched.Binding.TreeDigest)
	}
	if len(view.Installations[0].Bindings) != 0 {
		t.Fatalf("switch materialized a client: %+v", view.Installations[0].Bindings)
	}
	again, err := eng.SwitchRetained(ctx, Request{
		PackageRoot: other, InstallationID: id, OperationID: "retained-switch-again",
	}, Decision{Confirmed: true})
	if err != nil || again.Outcome != OutcomeCompleted {
		t.Fatalf("idempotent switch: %+v %v", again, err)
	}
	if again.Binding.TreeDigest != switched.Binding.TreeDigest {
		t.Fatalf("idempotent digest: %s vs %s", again.Binding.TreeDigest, switched.Binding.TreeDigest)
	}
	view, err = eng.Inspect(ctx)
	if err != nil || view.Installations[0].TreeDigest != again.Binding.TreeDigest {
		t.Fatalf("inspect after idempotent switch: %+v %v", view, err)
	}
	added, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: other, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "retained-switch-add",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, added, Decision{Confirmed: true})
	_ = added.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("add after switch: %+v %v", got, err)
	}
	body, err = os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA after add: %s %v", body, err)
	}
}

func TestRepairRematerializesMissingTarget(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000088"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "repair-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	target := installed.Plan().TargetPath
	_ = installed.Close()
	if err != nil || first.Outcome != OutcomeCompleted || target == "" {
		t.Fatalf("install: %+v %v path=%s", first, err, target)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "repair-apply",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, repaired, Decision{Confirmed: true})
	_ = repaired.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("repair: %+v %v", got, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore target: %v", err)
	}
}

func TestRepairMissingBindingIsNotInstalled(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(testCtx(t), Request{
		Operation: OpRepair, ClientID: "codex", ClientConfigRoot: root, PackageRoot: root,
		InstallationID: "00000000-0000-4000-8000-000000000089",
	})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("repair without binding: %v", err)
	}
}

func TestRepairDifferentDigestRequiresUpdate(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000090"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "repair-digest-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	_ = installed.Close()
	if err != nil || first.Outcome != OutcomeCompleted {
		t.Fatalf("install: %+v %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "repair-digest-blocked",
		RequiredComponents: []string{"mcp", "skills"},
	}); !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("repair rewrote revision: %v", err)
	}
}

func TestUpdateSameDigestIsUnchanged(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000091"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-same-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	_ = installed.Close()
	if err != nil || first.Outcome != OutcomeCompleted {
		t.Fatalf("install: %+v %v", first, err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-same-apply",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil {
		t.Fatalf("same-digest update: %+v %v", got, err)
	}
	if got.Outcome != OutcomeUnchanged && got.Outcome != OutcomeCompleted {
		t.Fatalf("same-digest update: %+v", got)
	}
	if got.InstallationID != id {
		t.Fatalf("update changed installation: %s", got.InstallationID)
	}
}

func TestUpdateOneClientKeepsSibling(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-000000000092"
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
	install("codex", codexConfig, "sibling-codex-install")
	install("claude", claudeConfig, "sibling-claude-install")
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "sibling-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("sibling inspect: %+v %v", view, err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath || claudeAfter.DataRoot != claudeBefore.DataRoot || claudeAfter.Profile != claudeBefore.Profile {
		t.Fatalf("update rewrote untouched sibling: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	codexAfter := inspectedBinding(t, view, "codex")
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.Profile != codexBefore.Profile {
		t.Fatalf("update re-bound selected client: before=%+v after=%+v", codexBefore, codexAfter)
	}
	if view.Installations[0].InstallationID != id {
		t.Fatalf("installation id: %s", view.Installations[0].InstallationID)
	}
}

func TestRepairOneClientKeepsSibling(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c8"
	installBothClients(t, ctx, eng, pkg, probe, id, "repair-sibling-install", bothClientTargets(codexConfig, claudeConfig, probe))
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	if err := os.RemoveAll(codexBefore.TargetPath); err != nil {
		t.Fatal(err)
	}
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "repair-sibling-codex",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, repaired, Decision{Confirmed: true})
	_ = repaired.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex repair: %+v %v", got, err)
	}
	if _, err := os.Stat(codexBefore.TargetPath); err != nil {
		t.Fatalf("repair did not restore selected target: %v", err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("sibling inspect: %+v %v", view, err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath || claudeAfter.DataRoot != claudeBefore.DataRoot || claudeAfter.Profile != claudeBefore.Profile {
		t.Fatalf("repair rewrote untouched sibling: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	codexAfter := inspectedBinding(t, view, "codex")
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.Profile != codexBefore.Profile || codexAfter.TargetPath != codexBefore.TargetPath {
		t.Fatalf("repair re-bound selected client: before=%+v after=%+v", codexBefore, codexAfter)
	}
}

func TestRepairOlderSiblingAfterSubsetUpdate(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d2"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	installBothClients(t, ctx, eng, r1, probe, id, "older-sibling-install", bothClientTargets(codexConfig, claudeConfig, probe))
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "older-sibling-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	if claudeBefore.TreeDigest == "" || claudeBefore.TreeDigest == codexBefore.TreeDigest {
		t.Fatalf("inspect collapsed mixed revisions: claude=%+v codex=%+v", claudeBefore, codexBefore)
	}
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: r1, ClientID: "claude", ClientConfigRoot: claudeConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "older-sibling-claude-repair",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = eng.Apply(ctx, repaired, Decision{Confirmed: true})
	_ = repaired.Close()
	if err != nil || (got.Outcome != OutcomeUnchanged && got.Outcome != OutcomeCompleted) {
		t.Fatalf("claude r1 repair: %+v %v", got, err)
	}
	if got.Binding.TreeDigest != claudeBefore.TreeDigest || got.Client.TreeDigest != claudeBefore.TreeDigest {
		t.Fatalf("claude r1 repair result collapsed digest: binding=%s client=%s want=%s", got.Binding.TreeDigest, got.Client.TreeDigest, claudeBefore.TreeDigest)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect after older sibling repair: %+v %v", view, err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	codexAfter := inspectedBinding(t, view, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath || claudeAfter.DataRoot != claudeBefore.DataRoot || claudeAfter.Profile != claudeBefore.Profile {
		t.Fatalf("r1 repair rewrote claude: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TargetPath != codexBefore.TargetPath || codexAfter.DataRoot != codexBefore.DataRoot || codexAfter.Profile != codexBefore.Profile {
		t.Fatalf("r1 repair rewrote codex sibling: before=%+v after=%+v", codexBefore, codexAfter)
	}
	if claudeAfter.TreeDigest != claudeBefore.TreeDigest || codexAfter.TreeDigest != codexBefore.TreeDigest {
		t.Fatalf("r1 repair rewrote digests: before claude=%s codex=%s after claude=%s codex=%s", claudeBefore.TreeDigest, codexBefore.TreeDigest, claudeAfter.TreeDigest, codexAfter.TreeDigest)
	}
	discovered := map[string]string{}
	for _, client := range eng.Discover() {
		for _, binding := range client.Bindings {
			discovered[binding.ClientID] = binding.TreeDigest
		}
	}
	if discovered["claude"] != claudeAfter.TreeDigest || discovered["codex"] != codexAfter.TreeDigest {
		t.Fatalf("discover collapsed mixed revisions: %+v inspect claude=%s codex=%s", discovered, claudeAfter.TreeDigest, codexAfter.TreeDigest)
	}
}

func TestRepairGroupIntactReportsBothTargets(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c9"
	installBothClients(t, ctx, eng, pkg, probe, id, "repair-group-intact-install", bothClientTargets(codexConfig, claudeConfig, probe))
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, InstallationID: id, OperationID: "repair-group-intact",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: bothClientTargets(codexConfig, claudeConfig, probe),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, repaired, Decision{Confirmed: true})
	_ = repaired.Close()
	if err != nil || (got.Outcome != OutcomeUnchanged && got.Outcome != OutcomeCompleted) {
		t.Fatalf("intact group repair: %+v %v", got, err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("intact group repair omitted targets: %+v", got.Targets)
	}
	seen := map[string]bool{}
	for _, target := range got.Targets {
		seen[target.ClientID] = true
		if target.BindingID == "" {
			t.Fatalf("intact group repair omitted binding: %+v", target)
		}
	}
	if !seen["claude"] || !seen["codex"] {
		t.Fatalf("intact group repair clients: %+v", got.Targets)
	}
	for _, target := range got.Targets {
		if target.TreeDigest == "" {
			t.Fatalf("intact group repair omitted digest: %+v", target)
		}
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect after intact group repair: %+v %v", view, err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	codexAfter := inspectedBinding(t, view, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath || claudeAfter.DataRoot != claudeBefore.DataRoot || claudeAfter.Profile != claudeBefore.Profile {
		t.Fatalf("intact group repair rewrote claude: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TargetPath != codexBefore.TargetPath || codexAfter.DataRoot != codexBefore.DataRoot || codexAfter.Profile != codexBefore.Profile {
		t.Fatalf("intact group repair rewrote codex: before=%+v after=%+v", codexBefore, codexAfter)
	}
}

func TestRepairGroupMixedRevisionsUsesPerTargetPackage(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d1"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	installBothClients(t, ctx, eng, r1, probe, id, "mixed-repair-install", bothClientTargets(codexConfig, claudeConfig, probe))
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "mixed-repair-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, InstallationID: id, OperationID: "mixed-repair-group",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, PackageRoot: pkg},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe, PackageRoot: r1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := repaired.Plan()
	claudeDigest := ""
	codexDigest := ""
	for _, target := range plan.Targets {
		switch target.ClientID {
		case "claude":
			claudeDigest = target.TreeDigest
		case "codex":
			codexDigest = target.TreeDigest
		}
	}
	if claudeDigest == "" || codexDigest == "" || claudeDigest == codexDigest {
		t.Fatalf("mixed repair plan digests: %+v", plan.Targets)
	}
	projected := map[string]string{}
	eng.cfg.ServerName = "sample-notify"
	eng.cfg.ProjectArgs = func(facts BindingFacts) ([]string, error) {
		projected[facts.ClientID] = facts.TreeDigest
		return []string{"portable-launch", "--locator", facts.DataRoot}, nil
	}
	callbacks := map[string]string{}
	eng.cfg.OnCommittedBinding = func(_ context.Context, facts BindingFacts) error {
		callbacks[facts.ClientID] = facts.TreeDigest
		return nil
	}
	got, err = eng.Apply(ctx, repaired, Decision{Confirmed: true})
	_ = repaired.Close()
	if err != nil || (got.Outcome != OutcomeUnchanged && got.Outcome != OutcomeCompleted) {
		t.Fatalf("mixed revision repair: %+v %v", got, err)
	}
	seen := map[string]ClientResult{}
	for _, target := range got.Targets {
		seen[target.ClientID] = target
	}
	if seen["claude"].TreeDigest != claudeDigest || seen["codex"].TreeDigest != codexDigest {
		t.Fatalf("mixed repair result collapsed digests: %+v plan claude=%s codex=%s", got.Targets, claudeDigest, codexDigest)
	}
	if callbacks["claude"] != claudeDigest || callbacks["codex"] != codexDigest {
		t.Fatalf("mixed repair callback collapsed digests: %+v plan claude=%s codex=%s", callbacks, claudeDigest, codexDigest)
	}
	if got.Outcome == OutcomeCompleted && (projected["claude"] != claudeDigest || projected["codex"] != codexDigest) {
		t.Fatalf("mixed repair projection collapsed digests: %+v plan claude=%s codex=%s", projected, claudeDigest, codexDigest)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect after mixed repair: %+v %v", view, err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	codexAfter := inspectedBinding(t, view, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath || claudeAfter.DataRoot != claudeBefore.DataRoot || claudeAfter.Profile != claudeBefore.Profile {
		t.Fatalf("mixed repair rewrote claude: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TargetPath != codexBefore.TargetPath || codexAfter.DataRoot != codexBefore.DataRoot || codexAfter.Profile != codexBefore.Profile {
		t.Fatalf("mixed repair rewrote codex: before=%+v after=%+v", codexBefore, codexAfter)
	}
}

func TestRepairGroupMixedRevisionsAssessesEachSnapshot(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d2"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	installBothClients(t, ctx, eng, r1, probe, id, "mixed-repair-assess-install", bothClientTargets(codexConfig, claudeConfig, probe))
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "mixed-repair-assess-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	if claudeBefore.TreeDigest == "" || codexBefore.TreeDigest == "" || claudeBefore.TreeDigest == codexBefore.TreeDigest {
		t.Fatalf("mixed inspect digests: claude=%s codex=%s", claudeBefore.TreeDigest, codexBefore.TreeDigest)
	}
	stateBefore, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	eng.cfg.Assess = func(_ context.Context, _, digest string) (Assessment, error) {
		seen[digest]++
		if digest == claudeBefore.TreeDigest {
			return Assessment{TreeDigest: digest, Outcome: AssessmentBlock, Reason: "block-claude"}, nil
		}
		return Assessment{TreeDigest: digest, Outcome: AssessmentAllow}, nil
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpRepair, InstallationID: id, OperationID: "mixed-repair-assess-group",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, PackageRoot: pkg},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe, PackageRoot: r1},
		},
	})
	if !errors.Is(err, ErrAssessmentRejected) {
		t.Fatalf("blocked mixed repair: %v", err)
	}
	if seen[codexBefore.TreeDigest] != 1 || seen[claudeBefore.TreeDigest] != 1 {
		t.Fatalf("mixed repair assess calls: %+v claude=%s codex=%s", seen, claudeBefore.TreeDigest, codexBefore.TreeDigest)
	}
	stateAfter, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("blocked mixed repair mutated state")
	}
	if entries, readErr := os.ReadDir(eng.cfg.TempRoot); readErr == nil && len(entries) != 0 {
		t.Fatalf("blocked mixed repair left snapshots: %v", names(entries))
	}
	view, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	codexAfter := inspectedBinding(t, view, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TreeDigest != claudeBefore.TreeDigest || claudeAfter.TargetPath != claudeBefore.TargetPath {
		t.Fatalf("blocked mixed repair rewrote claude: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TreeDigest != codexBefore.TreeDigest || codexAfter.TargetPath != codexBefore.TargetPath {
		t.Fatalf("blocked mixed repair rewrote codex: before=%+v after=%+v", codexBefore, codexAfter)
	}
}

func TestRepairGroupMixedRevisionsAssessDigestMismatch(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d5"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	installBothClients(t, ctx, eng, r1, probe, id, "mixed-repair-mismatch-install", bothClientTargets(codexConfig, claudeConfig, probe))
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "mixed-repair-mismatch-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	stateBefore, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	eng.cfg.Assess = func(_ context.Context, _, digest string) (Assessment, error) {
		seen[digest]++
		if digest == claudeBefore.TreeDigest {
			return Assessment{TreeDigest: "sha256:" + strings.Repeat("cd", 32), Outcome: AssessmentAllow}, nil
		}
		return Assessment{TreeDigest: digest, Outcome: AssessmentAllow}, nil
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpRepair, InstallationID: id, OperationID: "mixed-repair-mismatch-group",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, PackageRoot: pkg},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe, PackageRoot: r1},
		},
	})
	if !errors.Is(err, ErrAssessmentRejected) {
		t.Fatalf("mismatched mixed repair: %v", err)
	}
	if seen[codexBefore.TreeDigest] != 1 || seen[claudeBefore.TreeDigest] != 1 {
		t.Fatalf("mixed repair mismatch assess calls: %+v claude=%s codex=%s", seen, claudeBefore.TreeDigest, codexBefore.TreeDigest)
	}
	stateAfter, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("mismatched mixed repair mutated state")
	}
}

func TestRepairGroupSameRootAssessesOnce(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d6"
	installBothClients(t, ctx, eng, pkg, probe, id, "same-root-assess-install", bothClientTargets(codexConfig, claudeConfig, probe))
	calls := 0
	digest := ""
	eng.cfg.Assess = func(_ context.Context, _, got string) (Assessment, error) {
		calls++
		digest = got
		return Assessment{TreeDigest: got, Outcome: AssessmentAllow}, nil
	}
	if _, err := eng.Inspect(ctx); err != nil {
		t.Fatal(err)
	}
	_ = eng.Discover()
	if recovered, err := eng.RecoverCurrent(ctx); err != nil || recovered.Outcome != OutcomeUnchanged {
		t.Fatalf("recover: %+v %v", recovered, err)
	}
	if calls != 0 {
		t.Fatalf("inspect/discover/recover invoked Assess %d times", calls)
	}
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, InstallationID: id, OperationID: "same-root-assess-repair",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: bothClientTargets(codexConfig, claudeConfig, probe),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = repaired.Close()
	if calls != 1 || digest == "" {
		t.Fatalf("same-root repair assess calls=%d digest=%s", calls, digest)
	}
}

func TestRepairMixedRevisionRematerializesDeletedOlderSibling(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000ed"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	installBothClients(t, ctx, eng, r1, probe, id, "mixed-repair-delete-install", bothClientTargets(codexConfig, claudeConfig, probe))
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "mixed-repair-delete-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claudeBefore := inspectedBinding(t, before, "claude")
	codexBefore := inspectedBinding(t, before, "codex")
	if err := os.RemoveAll(claudeBefore.TargetPath); err != nil {
		t.Fatal(err)
	}
	repaired, err := eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: r1, ClientID: "claude", ClientConfigRoot: claudeConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "mixed-repair-delete-claude",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = eng.Apply(ctx, repaired, Decision{Confirmed: true})
	_ = repaired.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("mixed rematerialize: %+v %v", got, err)
	}
	if got.Client.TreeDigest != claudeBefore.TreeDigest || got.Binding.TreeDigest != claudeBefore.TreeDigest {
		t.Fatalf("mixed rematerialize result collapsed digest: client=%s binding=%s want=%s", got.Client.TreeDigest, got.Binding.TreeDigest, claudeBefore.TreeDigest)
	}
	if _, err := os.Stat(claudeBefore.TargetPath); err != nil {
		t.Fatalf("mixed rematerialize did not restore claude: %v", err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("inspect after mixed rematerialize: %+v %v", view, err)
	}
	claudeAfter := inspectedBinding(t, view, "claude")
	codexAfter := inspectedBinding(t, view, "codex")
	if claudeAfter.BindingID != claudeBefore.BindingID || claudeAfter.TargetPath != claudeBefore.TargetPath || claudeAfter.DataRoot != claudeBefore.DataRoot || claudeAfter.Profile != claudeBefore.Profile {
		t.Fatalf("mixed rematerialize rewrote claude: before=%+v after=%+v", claudeBefore, claudeAfter)
	}
	if codexAfter.BindingID != codexBefore.BindingID || codexAfter.TargetPath != codexBefore.TargetPath || codexAfter.DataRoot != codexBefore.DataRoot || codexAfter.Profile != codexBefore.Profile {
		t.Fatalf("mixed rematerialize rewrote codex sibling: before=%+v after=%+v", codexBefore, codexAfter)
	}
}

func TestRepairGroupSamePackageRefusesOlderSibling(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d3"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	installBothClients(t, ctx, eng, r1, probe, id, "same-root-mixed-repair-install", bothClientTargets(codexConfig, claudeConfig, probe))
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "same-root-mixed-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpRepair, PackageRoot: pkg, InstallationID: id, OperationID: "same-root-mixed-repair",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: bothClientTargets(codexConfig, claudeConfig, probe),
	})
	if !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("same-root mixed repair: %v", err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("same-root mixed repair mutated state")
	}
}

func TestRepairGroupMixedRevisionsIncompleteOlderPackage(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000d4"
	r1 := filepath.Join(filepath.Dir(pkg), "package-r1")
	copyPackage(t, pkg, r1)
	if err := os.RemoveAll(filepath.Join(r1, "skills")); err != nil {
		t.Fatal(err)
	}
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: r1, InstallationID: id, OperationID: "mixed-repair-incomplete-install",
		RequiredComponents: []string{"mcp"}, ClientExecutable: probe,
		Targets: bothClientTargets(codexConfig, claudeConfig, probe),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Apply(ctx, installed, Decision{Confirmed: true})
	_ = installed.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("group install: %+v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "mixed-repair-incomplete-codex-update",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = eng.Apply(ctx, updated, Decision{Confirmed: true})
	_ = updated.Close()
	if err != nil || got.Outcome != OutcomeCompleted {
		t.Fatalf("codex update: %+v %v", got, err)
	}
	stateBefore, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpRepair, InstallationID: id, OperationID: "mixed-repair-incomplete-group",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, PackageRoot: pkg},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe, PackageRoot: r1},
		},
	})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete older mixed repair: %v", err)
	}
	stateAfter, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("incomplete mixed repair mutated state")
	}
}

func TestUpdateOneClientRefusesWhenSiblingProfileUnknown(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000c6"
	installBothClients(t, ctx, eng, pkg, probe, id, "sibling-profile-install", bothClientTargets(codexConfig, claudeConfig, probe))
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	removed := 0
	if err := filepath.Walk(eng.cfg.PluginDataBase, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Name() != LiveProfilesFile {
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
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: codexConfig,
		ClientExecutable: probe, InstallationID: id, OperationID: "sibling-profile-missing",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrCompatibilityUnavailable) {
		t.Fatalf("missing sibling profile: %v", err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("missing sibling profile mutated state")
	}
	view, err = eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("bindings after refused update: %+v %v", view, err)
	}
}

func TestUpdateAndRepairCancelledBeforeMutation(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	for _, tc := range []struct {
		op Operation
		id string
	}{
		{OpUpdate, "00000000-0000-4000-8000-000000000093"},
		{OpRepair, "00000000-0000-4000-8000-000000000094"},
	} {
		t.Run(string(tc.op), func(t *testing.T) {
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
			installed, err := eng.Prepare(ctx, Request{
				Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
				ClientExecutable: probe, InstallationID: tc.id, OperationID: string(tc.op) + "-install",
				RequiredComponents: []string{"mcp", "skills"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			_ = installed.Close()
			before, err := os.ReadFile(eng.cfg.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := eng.Prepare(ctx, Request{
				Operation: tc.op, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
				ClientExecutable: probe, InstallationID: tc.id, OperationID: string(tc.op) + "-cancel",
				RequiredComponents: []string{"mcp", "skills"},
			})
			if err != nil {
				t.Fatal(err)
			}
			cancelled, err := eng.Apply(ctx, prepared, Decision{})
			_ = prepared.Close()
			if !errors.Is(err, ErrCancelled) || cancelled.Outcome != OutcomeCancelled {
				t.Fatalf("cancelled %s: %+v %v", tc.op, cancelled, err)
			}
			after, err := os.ReadFile(eng.cfg.StateFile)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("cancelled %s mutated state: %v", tc.op, err)
			}
		})
	}
}

func TestUpdateAndRepairRefusePendingJournalWithoutRecovering(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	for _, tc := range []struct {
		op Operation
		id string
	}{
		{OpUpdate, "00000000-0000-4000-8000-000000000095"},
		{OpRepair, "00000000-0000-4000-8000-000000000096"},
	} {
		t.Run(string(tc.op), func(t *testing.T) {
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
			installed, err := eng.Prepare(ctx, Request{
				Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
				ClientExecutable: probe, InstallationID: tc.id, OperationID: string(tc.op) + "-journal-install",
				RequiredComponents: []string{"mcp", "skills"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			_ = installed.Close()
			receipt := plantOpenJournal(t, eng, string(tc.op)+"-pending")
			prepared, err := eng.Prepare(ctx, Request{
				Operation: tc.op, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
				ClientExecutable: probe, InstallationID: tc.id, OperationID: string(tc.op) + "-journal-blocked",
				RequiredComponents: []string{"mcp", "skills"},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
			_ = prepared.Close()
			if !errors.Is(err, ErrRecoveryRequired) || result.Outcome != OutcomeRecovery {
				t.Fatalf("pending journal %s: %+v %v", tc.op, result, err)
			}
			open, listErr := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
			if listErr != nil || len(open) != 1 || open[0].OperationID != receipt.OperationID {
				t.Fatalf("%s recovered journal: %+v %v", tc.op, open, listErr)
			}
		})
	}
}

func TestRemoveRefusesPendingJournalWithoutRecovering(t *testing.T) {
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
	id := "00000000-0000-4000-8000-0000000000e1"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "remove-journal-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = installed.Close()
	receipt := plantOpenJournal(t, eng, "remove-pending")
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "remove-journal-blocked",
		ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if !errors.Is(err, ErrRecoveryRequired) || result.Outcome != OutcomeRecovery {
		t.Fatalf("pending journal remove: %+v %v", result, err)
	}
	open, listErr := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if listErr != nil || len(open) != 1 || open[0].OperationID != receipt.OperationID {
		t.Fatalf("remove recovered journal: %+v %v", open, listErr)
	}
}

func TestRemoveGroupRefusesPendingJournalWithoutRecovering(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	codexConfig := filepath.Join(base, "codex-config")
	claudeConfig := filepath.Join(base, "claude-config")
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
	id := "00000000-0000-4000-8000-0000000000e2"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, InstallationID: id, OperationID: "remove-group-journal-install",
		RequiredComponents: []string{"mcp", "skills"}, ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = installed.Close()
	receipt := plantOpenJournal(t, eng, "remove-group-pending")
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "remove-group-journal-blocked",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if !errors.Is(err, ErrRecoveryRequired) || result.Outcome != OutcomeRecovery {
		t.Fatalf("pending journal remove group: %+v %v", result, err)
	}
	open, listErr := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if listErr != nil || len(open) != 1 || open[0].OperationID != receipt.OperationID {
		t.Fatalf("remove group recovered journal: %+v %v", open, listErr)
	}
}

func TestUpdateStalePlanChanged(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000097"
	installed, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-plan-install",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, installed, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = installed.Close()
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := eng.Prepare(ctx, Request{
		Operation: OpUpdate, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "update-plan-stale",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = updated.Close() }()
	store := statev2.Store{Path: eng.cfg.StateFile}
	state, err := store.Load()
	if err != nil || len(state.Installations) != 1 {
		t.Fatalf("load: %+v %v", state, err)
	}
	for bindingID, binding := range state.Installations[0].Clients {
		binding.TargetLocator = filepath.Join(base, "moved-target")
		state.Installations[0].Clients[bindingID] = binding
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	planted, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := eng.Apply(ctx, updated, Decision{Confirmed: true})
	if !errors.Is(err, ErrPlanChanged) || moved.Outcome != OutcomeConflict || moved.Reason != "plan_changed" {
		t.Fatalf("stale update apply: %+v %v", moved, err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(planted, after) {
		t.Fatalf("plan_changed mutated state: %v", err)
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

func TestPrepareRemoveRejectsMissingPluginDataBeforeDeactivate(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-0000000000e7",
		OperationID: "remove-data-preflight", RequiredComponents: []string{"mcp", "skills"},
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
	if err := os.Remove(filepath.Join(result.Binding.DataRoot, ".agentplugins-data-owner.json")); err != nil {
		t.Fatal(err)
	}
	runner := &countingRunner{}
	check, err := New(Config{StateRoot: state, HelperExecutable: probe, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	_, err = check.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "remove-missing-data", ExternalUninstalled: true,
	})
	if err == nil {
		t.Fatal("missing PLUGIN_DATA accepted")
	}
	if runner.n != 0 {
		t.Fatalf("remove preflight deactivated client: %d", runner.n)
	}
}

func TestPrepareRemoveGroupRejectsMissingPluginDataBeforeDeactivate(t *testing.T) {
	ctx, eng, runner, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000e8"
	targets := bothClientTargets(codexConfig, claudeConfig, probe)
	installed := installBothClients(t, ctx, eng, pkg, probe, id, "group-remove-data-install", targets)
	if installed.Binding.DataRoot == "" {
		t.Fatal("install omitted data root")
	}
	if err := os.Remove(filepath.Join(installed.Binding.DataRoot, ".agentplugins-data-owner.json")); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	_, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-remove-missing-data",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err == nil {
		t.Fatal("missing PLUGIN_DATA accepted")
	}
	if len(runner.calls) != before {
		t.Fatalf("group remove preflight deactivated client: %d -> %d", before, len(runner.calls))
	}
}

func TestApplyRemoveGroupRepeatsPluginDataPreflightBeforeDeactivate(t *testing.T) {
	ctx, eng, runner, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	id := "00000000-0000-4000-8000-0000000000ec"
	targets := bothClientTargets(codexConfig, claudeConfig, probe)
	installed := installBothClients(t, ctx, eng, pkg, probe, id, "group-remove-data-apply-install", targets)
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, InstallationID: id, OperationID: "group-remove-data-stale",
		ClientExecutable: probe,
		Targets: []ClientTarget{
			{ClientID: "codex", ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true},
			{ClientID: "claude", ClientConfigRoot: claudeConfig, ClientExecutable: probe},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	if err := os.Remove(filepath.Join(installed.Binding.DataRoot, ".agentplugins-data-owner.json")); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	if _, err := eng.Apply(ctx, prepared, Decision{Confirmed: true}); err == nil {
		t.Fatal("stale PLUGIN_DATA accepted")
	}
	if len(runner.calls) != before {
		t.Fatalf("group apply remove deactivated after data preflight: %d -> %d", before, len(runner.calls))
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 2 {
		t.Fatalf("group apply remove mutated bindings: %+v %v", view, err)
	}
}

func TestApplyRemoveRepeatsPluginDataPreflightBeforeDeactivate(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-0000000000eb",
		OperationID: "remove-data-apply", RequiredComponents: []string{"mcp", "skills"},
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
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: req.InstallationID, OperationID: "remove-data-stale", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rm.Close() }()
	if err := os.Remove(filepath.Join(result.Binding.DataRoot, ".agentplugins-data-owner.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, rm, Decision{Confirmed: true}); err == nil {
		t.Fatal("stale PLUGIN_DATA accepted")
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("apply remove mutated bindings: %+v %v", view, err)
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
	if !strings.Contains(text, "Recover(") || !strings.Contains(text, "OpUpdate") || !strings.Contains(text, "OpRepair") {
		t.Fatal("example omits published lifecycle operations")
	}
	if !strings.Contains(text, "ClientTarget") || !strings.Contains(text, "Targets:") {
		t.Fatal("example omits published group Request.Targets")
	}
	if !strings.Contains(text, "repairTargets") {
		t.Fatal("example omits per-target PackageRoot on group Repair")
	}
}

func TestExampleFlaggedPathRunsAgainstLocalModule(t *testing.T) {
	skipWindowsLauncherExecuteBit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	state := filepath.Join(base, "uap")
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(base, "sample")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "go.mod", "go.sum"} {
		body, err := os.ReadFile(filepath.Join("example", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	mod, err := os.ReadFile(filepath.Join(work, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	replaced := string(mod) + "\nreplace github.com/777genius/agent-notifications => " + repo + "\n"
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(replaced), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(work, "sample")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Dir = work
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off")
	if body, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sample: %s %v", body, err)
	}
	run := exec.CommandContext(ctx, bin,
		"-state", state, "-package", pkg, "-config", config,
		"-helper", probe, "-client-exe", probe,
	)
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("sample: %s %v", out, err)
	}
	text := string(out)
	for _, want := range []string{"discover=", "install=", "inspect-bindings=1", "recover=", "repeat=", "update=", "repair=", "remove="} {
		if !strings.Contains(text, want) {
			t.Fatalf("sample omitted %s:\n%s", want, text)
		}
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

func TestSeamActivatorDelegatesProviderPreflight(t *testing.T) {
	eng, err := New(Config{StateRoot: filepath.Join(t.TempDir(), "uap")})
	if err != nil {
		t.Fatal(err)
	}
	svc := eng.lifecycle(nil, BindingFacts{})
	type activationCapabilities interface {
		AutomaticallyActivates(domain.ActivationRequest) bool
		PreflightActivation(domain.ActivationRequest) error
	}
	activator, ok := svc.Activator.(activationCapabilities)
	if !ok {
		t.Fatal("seamActivator dropped AutomaticallyActivates/PreflightActivation")
	}
	if activator.AutomaticallyActivates(domain.ActivationRequest{
		Client: domain.DetectedClient{ClientID: domain.ClientCodex},
	}) {
		t.Fatal("codex without runner/executable should not automatically activate")
	}
	if err := activator.PreflightActivation(domain.ActivationRequest{
		Client: domain.DetectedClient{ClientID: domain.ClientClaude, ConfigRoot: filepath.Join(t.TempDir(), "claude")},
	}); err == nil {
		t.Fatal("claude preflight accepted an incomplete request")
	}
	type pluginDataStager interface {
		StageWithPluginData(context.Context, domain.PackageEnvelope, domain.DeliveryPlan, string, domain.CompatibilityHints, string) (domain.StagedDelivery, error)
		PreflightManagedStdio(string) error
	}
	if _, ok := svc.Stager.(pluginDataStager); !ok {
		t.Fatal("seamStager dropped StageWithPluginData/PreflightManagedStdio")
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

func TestRetainedInstallDifferentDigestIsUpdateRequired(t *testing.T) {
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
	id := "00000000-0000-4000-8000-000000000073"
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "retain-digest-install",
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
	sentinel := filepath.Join(result.Binding.DataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rm, err := eng.Prepare(ctx, Request{
		Operation: OpRemove, ClientID: "codex", ClientConfigRoot: config, ClientExecutable: probe,
		InstallationID: id, OperationID: "retain-digest-remove", ExternalUninstalled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, rm, Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	_ = rm.Close()
	other := filepath.Join(base, "other")
	writePackage(t, other, probe)
	if err := os.WriteFile(filepath.Join(other, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"sample-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: other, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: id, OperationID: "retain-digest-migrate",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("retained different digest: %v", err)
	}
	after, err := os.ReadFile(eng.cfg.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("different digest rewrote retained state")
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", got, err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || len(view.Installations) != 1 || !view.Installations[0].DataRetained || len(view.Installations[0].Bindings) != 0 {
		t.Fatalf("inspect after rejected retained update: %+v %v", view, err)
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

func TestProgressReportsGroupCoarsePhases(t *testing.T) {
	ctx, eng, _, pkg, probe, codexConfig, claudeConfig := newBothClientSandbox(t)
	var phases []ProgressPhase
	eng.cfg.Progress = func(event ProgressEvent) { phases = append(phases, event.Phase) }
	id := "00000000-0000-4000-8000-0000000000d7"
	installBothClients(t, ctx, eng, pkg, probe, id, "group-progress-install", bothClientTargets(codexConfig, claudeConfig, probe))
	joined := ""
	for _, phase := range phases {
		joined += string(phase) + ","
	}
	for _, want := range []ProgressPhase{ProgressPrepare, ProgressPreflight, ProgressStage, ProgressCommit, ProgressActivate, ProgressVerify, ProgressComplete} {
		if !strings.Contains(joined, string(want)+",") {
			t.Fatalf("missing group phase %s in %s", want, joined)
		}
	}
}

func TestCancelledApplyDoesNotReportMutationPhases(t *testing.T) {
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
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-0000000000d8",
		OperationID: "cancel-progress", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := eng.Apply(ctx, prepared, Decision{})
	_ = prepared.Close()
	if !errors.Is(err, ErrCancelled) || cancelled.Outcome != OutcomeCancelled {
		t.Fatalf("cancelled apply: %+v %v", cancelled, err)
	}
	joined := ""
	for _, phase := range phases {
		joined += string(phase) + ","
	}
	if !strings.Contains(joined, string(ProgressPrepare)+",") {
		t.Fatalf("prepare missing: %s", joined)
	}
	for _, blocked := range []ProgressPhase{ProgressStage, ProgressCommit, ProgressActivate, ProgressVerify, ProgressComplete} {
		if strings.Contains(joined, string(blocked)+",") {
			t.Fatalf("cancelled apply reported %s: %s", blocked, joined)
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

type recordingCodexRunner struct {
	calls     [][]string
	installed bool
	pluginID  string
}

func (r *recordingCodexRunner) Run(_ context.Context, cmd ports.Command) (ports.CommandResult, error) {
	argv := append([]string(nil), cmd.Argv...)
	r.calls = append(r.calls, argv)
	if containsArgSeq(argv, "plugin", "marketplace", "add") || containsArgSeq(argv, "plugin", "marketplace", "update") || containsArgSeq(argv, "plugin", "add") {
		r.installed = true
		for _, arg := range argv {
			if i := strings.Index(arg, "@"); i > 0 {
				r.pluginID = arg
			}
		}
		return ports.CommandResult{Stdout: []byte(`{"ok":true}`)}, nil
	}
	if containsArgSeq(argv, "plugin", "list") {
		if !r.installed || r.pluginID == "" || !strings.Contains(r.pluginID, "@") {
			return ports.CommandResult{Stdout: []byte(`{"installed":[]}`)}, nil
		}
		name, market, _ := strings.Cut(r.pluginID, "@")
		body := `{"installed":[{"pluginId":"` + r.pluginID + `","name":"` + name + `","marketplaceName":"` + market + `","installed":true,"enabled":true}]}`
		return ports.CommandResult{Stdout: []byte(body)}, nil
	}
	return ports.CommandResult{}, nil
}

func containsArgSeq(argv []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(argv); i++ {
		match := true
		for j := range seq {
			if argv[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func argvHas(calls [][]string, seq ...string) bool {
	for _, argv := range calls {
		if containsArgSeq(argv, seq...) {
			return true
		}
	}
	return false
}

func TestRetryAfterCommittedBindingFailureReconcilesBeforeVerifyOnly(t *testing.T) {
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
	var calls int
	runner := &recordingCodexRunner{}
	eng, err := New(Config{
		StateRoot: filepath.Join(base, "uap"), HelperExecutable: probe, Runner: runner,
		OnCommittedBinding: func(_ context.Context, facts BindingFacts) error {
			if facts.BindingID == "" || facts.TargetPath == "" || facts.DataRoot == "" {
				return errors.New("committed binding missing identity")
			}
			calls++
			if calls == 1 {
				return errors.New("host seam refused after managed commit")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: "00000000-0000-4000-8000-000000000076",
		OperationID: "commit-then-retry", RequiredComponents: []string{"mcp", "skills"},
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.Apply(ctx, prepared, Decision{Confirmed: true})
	_ = prepared.Close()
	if err == nil || first.Outcome != OutcomeIncomplete {
		t.Fatalf("first apply: %+v %v", first, err)
	}
	if argvHas(runner.calls, "plugin", "marketplace", "add") || argvHas(runner.calls, "plugin", "add") {
		t.Fatalf("first apply activated before host callback: %+v", runner.calls)
	}
	beforeRetry := calls
	if _, inspectErr := eng.Inspect(ctx); inspectErr != nil {
		t.Fatal(inspectErr)
	}
	preview, err := eng.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != beforeRetry {
		t.Fatalf("inspect/prepare executed host callback: %d -> %d", beforeRetry, calls)
	}
	retried, err := eng.Apply(ctx, preview, Decision{Confirmed: true})
	_ = preview.Close()
	if err != nil || retried.Outcome != OutcomeCompleted {
		t.Fatalf("retry apply: %+v %v", retried, err)
	}
	if calls != 2 {
		t.Fatalf("retry skipped committed-binding reconciliation: %d", calls)
	}
	if !argvHas(runner.calls, "plugin", "marketplace", "add") && !argvHas(runner.calls, "plugin", "marketplace", "update") {
		t.Fatalf("retry stayed on VerifyOnly listing: %+v", runner.calls)
	}
	if !argvHas(runner.calls, "plugin", "add") {
		t.Fatalf("retry omitted mutating plugin add: %+v", runner.calls)
	}
	view, inspectErr := eng.Inspect(ctx)
	if inspectErr != nil || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 {
		t.Fatalf("inspect after retry: %+v %v", view, inspectErr)
	}
	if retried.Binding.DataRoot == "" {
		t.Fatal("retry omitted PLUGIN_DATA")
	}
	if _, err := os.Lstat(retried.Binding.DataRoot); err != nil {
		t.Fatalf("PLUGIN_DATA after retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("inspect after retry executed host callback: %d", calls)
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

func TestReserveIdentityDoesNotStage(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "uap")
	temp := filepath.Join(base, "tmp")
	eng, err := New(Config{StateRoot: state, TempRoot: temp})
	if err != nil {
		t.Fatal(err)
	}
	first, err := eng.ReserveIdentity(IdentityRequest{ClientID: "codex", Allocate: true})
	if err != nil || first.InstallationID == "" || first.BindingID != "" {
		t.Fatalf("allocate: %+v %v", first, err)
	}
	if _, err := os.Lstat(temp); !os.IsNotExist(err) {
		t.Fatal("ReserveIdentity created TempRoot")
	}
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatal("ReserveIdentity created StateRoot")
	}
	again, err := eng.ReserveIdentity(IdentityRequest{
		ClientID: "codex", InstallationID: first.InstallationID, Allocate: true,
	})
	if err != nil || again.InstallationID != first.InstallationID {
		t.Fatalf("reuse reserved id: %+v %v", again, err)
	}
	absent, err := eng.ReserveIdentity(IdentityRequest{ClientID: "codex", Allocate: false})
	if err != nil || absent.InstallationID != "" {
		t.Fatalf("no allocate: %+v %v", absent, err)
	}
	if _, err := eng.ReserveIdentity(IdentityRequest{ClientID: "cursor", Allocate: true}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported client: %v", err)
	}
}

func TestReserveIdentityBindingIDMatchesPrepare(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	state := filepath.Join(base, "uap")
	temp := filepath.Join(base, "tmp")
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: state, TempRoot: temp})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := eng.ReserveIdentity(IdentityRequest{
		ClientID: "codex", Allocate: true, DeclaredName: "sample-notify", ClientConfigRoot: config,
	})
	if err != nil || reserved.InstallationID == "" || reserved.BindingID == "" || reserved.TargetPath == "" {
		t.Fatalf("reserve: %+v %v", reserved, err)
	}
	if _, err := os.Lstat(temp); !os.IsNotExist(err) {
		t.Fatal("ReserveIdentity created TempRoot")
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: reserved.InstallationID,
		OperationID: "identity-prepare", RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close() }()
	plan := prepared.Plan()
	if plan.BindingID != reserved.BindingID || plan.TargetPath != reserved.TargetPath || plan.InstallationID != reserved.InstallationID {
		t.Fatalf("prepare identity drifted: plan=%+v reserved=%+v", plan, reserved)
	}
}

func TestReserveIdentitySurvivesDirectCollision(t *testing.T) {
	ctx := testCtx(t)
	probe := buildProbe(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	state := filepath.Join(base, "uap")
	temp := filepath.Join(base, "tmp")
	config := filepath.Join(base, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	eng, err := New(Config{StateRoot: state, TempRoot: temp})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := eng.ReserveIdentity(IdentityRequest{
		ClientID: "codex", Allocate: true, DeclaredName: "sample-notify", ClientConfigRoot: config,
	})
	if err != nil || reserved.TargetPath == "" {
		t.Fatalf("reserve: %+v %v", reserved, err)
	}
	if err := os.MkdirAll(reserved.TargetPath, 0700); err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(ctx, Request{
		Operation: OpInstall, PackageRoot: pkg, ClientID: "codex", ClientConfigRoot: config,
		ClientExecutable: probe, InstallationID: reserved.InstallationID,
		OperationID: "identity-collision", RequiredComponents: []string{"mcp", "skills"},
	})
	if err == nil {
		_ = prepared.Close()
		t.Fatal("unmanaged collision was accepted")
	}
	if !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("collision: %v", err)
	}
	if prepared != nil {
		t.Fatal("failed prepare returned a handle")
	}
	again, err := eng.ReserveIdentity(IdentityRequest{
		ClientID: "codex", InstallationID: reserved.InstallationID, Allocate: true,
		DeclaredName: "sample-notify", ClientConfigRoot: config,
	})
	if err != nil || again.InstallationID != reserved.InstallationID || again.BindingID != reserved.BindingID || again.TargetPath != reserved.TargetPath {
		t.Fatalf("ids lost after collision: %+v %v", again, err)
	}
	if _, err := os.Lstat(eng.cfg.StateFile); !os.IsNotExist(err) {
		t.Fatal("collision prepare wrote state")
	}
}
