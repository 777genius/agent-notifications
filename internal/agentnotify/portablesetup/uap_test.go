//go:build linux || darwin

package portablesetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

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

func buildProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(src, []byte(`package main
import ("encoding/json"; "os")
func main() {
	json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "PLUGIN_DATA": os.Getenv("PLUGIN_DATA")})
}
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
		"skills/agent-notify/SKILL.md": []byte("---\nname: agent-notify\ndescription: Isolated portable setup fixture\n---\nFixture only.\n"),
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

func TestUAPMaterializerTwoClientsShareDataIndependentLocators(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: filepath.Join(root, "home", "claude config")},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000007",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	install := func(integration portable.Integration, gen uint64) portable.Binding {
		t.Helper()
		config := filepath.Join(root, "home", string(integration)+" config")
		if err := os.MkdirAll(config, 0700); err != nil {
			t.Fatal(err)
		}
		got, err := mat.Install(testCtx(t), MaterializeRequest{
			Identity: id, Integration: integration, ExpectedGeneration: gen,
			PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
			OperationID: "portable-" + string(integration),
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	codexB := install(portable.Codex, ledger.Generation)
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	claudeB := install(portable.Claude, snap.Ledger.Generation)
	if codexB.DataRoot != claudeB.DataRoot {
		t.Fatalf("clients did not share PLUGIN_DATA: %s vs %s", codexB.DataRoot, claudeB.DataRoot)
	}
	codexName, err := codexB.Filename()
	if err != nil {
		t.Fatal(err)
	}
	claudeName, err := claudeB.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if codexName == claudeName {
		t.Fatal("shared data used one locator")
	}
	lease, err := portable.Acquire(testCtx(t), codexB.DataRoot, codexName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease, err = portable.Acquire(testCtx(t), claudeB.DataRoot, claudeName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("UAP installation missing")
	}
	var sawLocator bool
	for _, binding := range installation.Clients {
		mcp := filepath.Join(binding.TargetLocator, ".mcp.json")
		body, err := os.ReadFile(mcp)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "portable-launch") || !strings.Contains(string(body), "--locator") {
			t.Fatalf("locator missing from projection %s: %s", mcp, body)
		}
		sawLocator = true
	}
	if !sawLocator {
		t.Fatal("no projected MCP")
	}
	snap, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: filepath.Join(root, "home", "claude config"), ClientExecutable: probe,
		OperationID: "portable-claude-remove",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.Acquire(testCtx(t), claudeB.DataRoot, claudeName); err == nil {
		t.Fatal("removed locator still acquired")
	}
	if _, err := portable.Acquire(testCtx(t), codexB.DataRoot, codexName); err != nil {
		t.Fatal("sibling locator lost")
	}
	if _, err := os.Stat(filepath.Join(codex.RuntimeRoot, codex.Primary)); err != nil {
		t.Fatal("shared runtime removed")
	}
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: filepath.Join(root, "home", "claude config"), ClientExecutable: probe,
		OperationID: "portable-claude-remove-again",
	}); err == nil || !errors.Is(err, ErrAlreadyAbsent) {
		t.Fatalf("removed Claude while Codex is live: %v", err)
	}
}

func TestUAPMaterializerRepeatedRemoveOfRetainedInstallationIsAlreadyAbsent(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000008",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	config := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
		OperationID: "portable-codex-install",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: config, ClientExecutable: probe, OperationID: "portable-codex-remove",
		ExternalUninstalled: true,
	}); err != nil {
		t.Fatal(err)
	}
	empty, err := mat.RetainedEmpty(id.InstallationID)
	if err != nil || !empty {
		t.Fatalf("last-client remove did not retain empty installation: %v empty=%v", err, empty)
	}
	before, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	generation := snap.Ledger.Generation
	lockBefore, lockBeforeErr := os.Stat(mat.Roots.LockFile)
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: generation,
		OperationID: "portable-codex-remove-again",
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("already_absent mutated UAP state")
	}
	snap, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil || snap.Ledger.Generation != generation {
		t.Fatalf("already_absent mutated generation: %+v %v", snap.Ledger, err)
	}
	if snap.Ledger.PendingMutation != nil {
		t.Fatalf("already_absent left pending mutation: %+v", snap.Ledger.PendingMutation)
	}
	lockAfter, lockAfterErr := os.Stat(mat.Roots.LockFile)
	if os.IsNotExist(lockBeforeErr) != os.IsNotExist(lockAfterErr) {
		t.Fatalf("already_absent changed UAP lock presence: before=%v after=%v", lockBeforeErr, lockAfterErr)
	}
	if lockBefore != nil && lockAfter != nil && (lockBefore.Size() != lockAfter.Size() || !lockBefore.ModTime().Equal(lockAfter.ModTime())) {
		t.Fatal("already_absent rewrote UAP lock")
	}
}

func plantUAPPendingJournal(t *testing.T, ops, owned, opID string) {
	t.Helper()
	staging := filepath.Join(owned, ".agentplugins-staging-pending")
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
	if err := os.MkdirAll(ops, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ops, opID+".json"), append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverOwnedJournalsReleasesCoordinatorLeaseBeforeUAP(t *testing.T) {
	codex, _ := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	uapRoot := filepath.Join(root, "uap")
	ops := filepath.Join(uapRoot, "state", "operations")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    ops,
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	plantUAPPendingJournal(t, ops, filepath.Join(uapRoot, "managed"), "portable-pending-journal")
	config := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	req := MaterializeRequest{
		Identity: Identity{
			InstallationID: "00000000-0000-4000-8000-000000000010",
			ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
			ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
			Primary: codex.Primary,
		},
		Integration: portable.Codex, ClientConfigRoot: config, ClientExecutable: probe,
	}
	if err := mat.recoverOwnedJournals(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	open, err := dirswap.Manager{JournalDir: ops}.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("UAP journal survived recover: %+v %v", open, err)
	}
	release, err := installruntime.AcquireCoordinatorLease(testCtx(t), codex.ControlRoot)
	if err != nil {
		t.Fatalf("coordinator lease still held after UAP recover: %v", err)
	}
	release()
}

func TestRecoverOwnedJournalsRecoversKernelThenUAP(t *testing.T) {
	codex, _ := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	uapRoot := filepath.Join(root, "uap")
	ops := filepath.Join(uapRoot, "state", "operations")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    ops,
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(codex.RuntimeRoot, "hook")
	crash := installruntime.Request{
		ControlRoot: codex.ControlRoot, RuntimeRoot: codex.RuntimeRoot,
		Owner: codex.Owner, ConsumerID: "existing",
		Files: []installruntime.File{{Path: hook, Data: []byte("new"), Mode: 0700}},
		Fault: func(phase string) error {
			if phase == "transaction" {
				return fmt.Errorf("crash")
			}
			return nil
		},
	}
	if _, err := installruntime.Commit(testCtx(t), crash); err == nil {
		t.Fatal("kernel fault not reached")
	}
	if _, err := os.Lstat(filepath.Join(codex.ControlRoot, "transaction.json")); err != nil {
		t.Fatal("missing kernel journal")
	}
	plantUAPPendingJournal(t, ops, filepath.Join(uapRoot, "managed"), "portable-both-journals")
	config := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	req := MaterializeRequest{
		Identity: Identity{
			InstallationID: "00000000-0000-4000-8000-000000000012",
			ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
			ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
			Primary: codex.Primary,
		},
		Integration: portable.Codex, ClientConfigRoot: config, ClientExecutable: probe,
	}
	if err := mat.recoverOwnedJournals(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(codex.ControlRoot, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("kernel journal survived recover")
	}
	open, err := dirswap.Manager{JournalDir: ops}.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("UAP journal survived recover: %+v %v", open, err)
	}
	got, err := os.ReadFile(hook)
	if err != nil || string(got) != "new" {
		t.Fatalf("kernel recover did not finish: %s %v", got, err)
	}
}

func TestGuardSecondClientDoesNotRecoverPendingJournal(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	ops := filepath.Join(uapRoot, "state", "operations")
	claudeConfig := filepath.Join(root, "home", "claude config")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    ops,
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000011",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-codex-guard",
	}); err != nil {
		t.Fatal(err)
	}
	plantUAPPendingJournal(t, ops, filepath.Join(uapRoot, "managed"), "guard-pending-journal")
	if err := mat.GuardSecondClient(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, PackageRoot: pkg,
		ClientConfigRoot: claudeConfig, ClientExecutable: probe, OperationID: "portable-claude-guard",
	}); err != nil {
		t.Fatal(err)
	}
	open, err := dirswap.Manager{JournalDir: ops}.ListOpen()
	if err != nil || len(open) != 1 || open[0].OperationID != "guard-pending-journal" {
		t.Fatalf("guard recovered journal: %+v %v", open, err)
	}
}

func TestRemoveDoesNotRecoverPendingJournal(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	ops := filepath.Join(uapRoot, "state", "operations")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    ops,
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000013",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	codexConfig := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-codex-remove-journal",
	})
	if err != nil {
		t.Fatal(err)
	}
	plantUAPPendingJournal(t, ops, filepath.Join(uapRoot, "managed"), "remove-pending-journal")
	err = mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ClientConfigRoot: codexConfig,
		ClientExecutable: probe, OperationID: "portable-codex-remove-blocked",
		ExternalUninstalled: true,
	})
	if !errors.Is(err, uapinstaller.ErrRecoveryRequired) {
		t.Fatalf("remove recovered or ignored journal: %v", err)
	}
	open, listErr := dirswap.Manager{JournalDir: ops}.ListOpen()
	if listErr != nil || len(open) != 1 || open[0].OperationID != "remove-pending-journal" {
		t.Fatalf("remove recovered journal: %+v %v", open, listErr)
	}
	requirePortableLocator(t, installed)
}

func TestRemoveGroupDoesNotRecoverPendingJournal(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	ops := filepath.Join(uapRoot, "state", "operations")
	claudeConfig := filepath.Join(root, "home", "claude config")
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    ops,
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000e3",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-journal-install",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-journal-install",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plantUAPPendingJournal(t, ops, filepath.Join(uapRoot, "managed"), "remove-group-pending-journal")
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true,
			OperationID: "portable-group-remove-blocked",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-blocked",
		},
	})
	if !errors.Is(err, uapinstaller.ErrRecoveryRequired) {
		t.Fatalf("remove group recovered or ignored journal: %v", err)
	}
	open, listErr := dirswap.Manager{JournalDir: ops}.ListOpen()
	if listErr != nil || len(open) != 1 || open[0].OperationID != "remove-group-pending-journal" {
		t.Fatalf("remove group recovered journal: %+v %v", open, listErr)
	}
	if len(installed) != 2 {
		t.Fatalf("group install: %+v", installed)
	}
	requirePortableLocator(t, installed[0])
	requirePortableLocator(t, installed[1])
}

func requirePortableLocator(t *testing.T, b portable.Binding) {
	t.Helper()
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := portable.Acquire(testCtx(t), b.DataRoot, name)
	if err != nil {
		t.Fatalf("locator missing: %v", err)
	}
	lease.Release()
}

func TestRemoveRejectsCorruptArtifactBeforeRevoke(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000e5",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	codexConfig := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-codex-remove-corrupt",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok || len(installation.Clients) != 1 {
		t.Fatalf("installed: %+v", installation)
	}
	tampered := false
	for _, binding := range installation.Clients {
		if err := os.WriteFile(filepath.Join(binding.TargetLocator, "tampered"), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		tampered = true
	}
	if !tampered {
		t.Fatal("codex target missing")
	}
	err = mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ClientConfigRoot: codexConfig,
		ClientExecutable: probe, OperationID: "portable-codex-remove-corrupt-blocked",
		ExternalUninstalled: true,
	})
	if err == nil {
		t.Fatal("corrupt managed artifact accepted")
	}
	requirePortableLocator(t, installed)
}

func TestRemoveGroupRejectsCorruptArtifactBeforeRevoke(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude config")
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000e6",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-corrupt-install",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-corrupt-install",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok || len(installation.Clients) != 2 {
		t.Fatalf("installed: %+v", installation)
	}
	tampered := false
	for _, binding := range installation.Clients {
		if binding.ClientID != string(portable.Claude) {
			continue
		}
		if err := os.WriteFile(filepath.Join(binding.TargetLocator, "tampered"), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		tampered = true
	}
	if !tampered {
		t.Fatal("claude target missing")
	}
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true,
			OperationID: "portable-group-remove-corrupt",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-corrupt",
		},
	})
	if err == nil {
		t.Fatal("corrupt managed artifact accepted")
	}
	if len(installed) != 2 {
		t.Fatalf("group install: %+v", installed)
	}
	requirePortableLocator(t, installed[0])
	requirePortableLocator(t, installed[1])
}

func TestRemoveRejectsMissingPluginDataBeforeRevoke(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000e9",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	codexConfig := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-codex-remove-data",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(installed.DataRoot, ".agentplugins-data-owner.json")); err != nil {
		t.Fatal(err)
	}
	err = mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ClientConfigRoot: codexConfig,
		ClientExecutable: probe, OperationID: "portable-codex-remove-data-blocked",
		ExternalUninstalled: true,
	})
	if err == nil {
		t.Fatal("missing PLUGIN_DATA accepted")
	}
	requirePortableLocator(t, installed)
}

func TestRemoveGroupRejectsMissingPluginDataBeforeRevoke(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude config")
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ea",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-data-install",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-data-install",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 2 {
		t.Fatalf("group install: %+v", installed)
	}
	if err := os.Remove(filepath.Join(installed[0].DataRoot, ".agentplugins-data-owner.json")); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true,
			OperationID: "portable-group-remove-data",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-data",
		},
	})
	if err == nil {
		t.Fatal("missing PLUGIN_DATA accepted")
	}
	requirePortableLocator(t, installed[0])
	requirePortableLocator(t, installed[1])
}

func TestRemoveRejectsMovedTargetBeforeRevoke(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ee",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	codexConfig := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(codexConfig, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-codex-remove-moved",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	moved := false
	for i := range state.Installations {
		if state.Installations[i].InstallationID != id.InstallationID {
			continue
		}
		for bid, binding := range state.Installations[i].Clients {
			binding.TargetLocator = filepath.Join(root, "moved-target")
			state.Installations[i].Clients[bid] = binding
			moved = true
		}
	}
	if !moved {
		t.Fatal("codex target missing")
	}
	if err := mat.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	err = mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ClientConfigRoot: codexConfig,
		ClientExecutable: probe, OperationID: "portable-codex-remove-moved-blocked",
		ExternalUninstalled: true,
	})
	if err == nil {
		t.Fatal("moved managed target accepted")
	}
	requirePortableLocator(t, installed)
}

func TestGuardSecondClientAllowsCopiedSameDigest(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	acquired := filepath.Join(root, "acquired copy")
	writePackage(t, pkg, probe)
	writePackage(t, acquired, probe)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude config")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000085",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{codexConfig, claudeConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-codex-copy-src",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mat.GuardSecondClient(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, PackageRoot: acquired,
		ClientConfigRoot: claudeConfig, ClientExecutable: probe, OperationID: "portable-claude-copy-src",
	}); err != nil {
		t.Fatalf("copied same digest: %v", err)
	}
}

func TestUAPMaterializerInstallRefusesConfirmedDigestDrift(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000083",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	config := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	previewReq := MaterializeRequest{
		Identity: id, Integration: portable.Codex, PackageRoot: pkg,
		ClientConfigRoot: config, ClientExecutable: probe, OperationID: "portable-digest-preview",
	}
	plan, err := mat.PreviewPlan(testCtx(t), previewReq)
	if err != nil || plan.TreeDigest == "" || plan.HelperDigest == "" {
		t.Fatalf("preview: %+v %v", plan, err)
	}
	drift := MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
		TreeDigest: "deadbeef", HelperDigest: plan.HelperDigest, HelperVersion: plan.HelperVersion,
		OperationID: "portable-digest-drift",
	}
	if _, err := mat.Install(testCtx(t), drift); !errors.Is(err, ErrSourceIdentityDrift) {
		t.Fatalf("wrong tree digest: %v", err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findInstallation(state, id.InstallationID); ok {
		t.Fatal("digest drift created installation")
	}
	drift.TreeDigest = plan.TreeDigest
	drift.HelperDigest = "deadbeef"
	drift.OperationID = "portable-helper-drift"
	if _, err := mat.Install(testCtx(t), drift); !errors.Is(err, ErrSourceIdentityDrift) {
		t.Fatalf("wrong helper digest: %v", err)
	}
	matched := drift
	matched.HelperDigest = plan.HelperDigest
	matched.HelperVersion = plan.HelperVersion
	matched.OperationID = "portable-digest-match"
	if _, err := mat.Install(testCtx(t), matched); err != nil {
		t.Fatal(err)
	}
}

func TestUAPMaterializerUpdateChangesLiveRevision(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000098",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	config := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	installed, err := mat.Install(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
		OperationID: "portable-update-install",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mat.Update(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
		OperationID: "portable-update-apply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.InstallationID != installed.InstallationID {
		t.Fatalf("update changed installation: %s vs %s", installed.InstallationID, got.InstallationID)
	}
}

func TestUAPMaterializerApplyGroupBothClientsShareDataIndependentLocators(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude config")
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000099",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	got, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-codex",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-claude",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("group bindings: %+v", got)
	}
	if got[0].DataRoot != got[1].DataRoot {
		t.Fatalf("clients did not share PLUGIN_DATA: %s vs %s", got[0].DataRoot, got[1].DataRoot)
	}
	codexName, err := got[0].Filename()
	if err != nil {
		t.Fatal(err)
	}
	claudeName, err := got[1].Filename()
	if err != nil {
		t.Fatal(err)
	}
	if codexName == claudeName {
		t.Fatal("shared data used one locator")
	}
	lease, err := portable.Acquire(testCtx(t), got[0].DataRoot, codexName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease, err = portable.Acquire(testCtx(t), got[1].DataRoot, claudeName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	other := filepath.Join(root, "other package")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, PackageRoot: pkg,
			ClientConfigRoot: codexConfig, ClientExecutable: probe,
		},
		{
			Identity: id, Integration: portable.Claude, PackageRoot: other,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		},
	})
	if err == nil || !errors.Is(err, ErrPreflight) {
		t.Fatalf("mixed package roots: %v", err)
	}
}

func TestUAPMaterializerApplyGroupRepairMixedRevisions(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(root, "package-r1")
	copyPackage(t, pkg, r1)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude-config")
	codexConfig := filepath.Join(root, "home", "codex-config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000d4",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: r1, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-mixed-repair-install-codex",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: r1, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-mixed-repair-install-claude",
		},
	})
	if err != nil || len(installed) != 2 {
		t.Fatalf("install: %+v %v", installed, err)
	}
	id.InstallationID = installed[0].InstallationID
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := mat.Update(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, PackageRoot: pkg,
		ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-mixed-repair-codex-update",
	})
	if err != nil {
		t.Fatal(err)
	}
	id.InstallationID = updated.InstallationID
	got, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, PackageRoot: pkg,
			ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-mixed-repair-group", Operation: uapinstaller.OpRepair,
		},
		{
			Identity: id, Integration: portable.Claude, PackageRoot: r1,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-mixed-repair-group", Operation: uapinstaller.OpRepair,
		},
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("mixed repair: %+v %v", got, err)
	}
	if got[0].InstallationID != id.InstallationID || got[1].InstallationID != id.InstallationID {
		t.Fatalf("mixed repair changed installation: %+v", got)
	}
	if got[0].BindingID != updated.BindingID {
		t.Fatalf("mixed repair rewrote codex: before=%s after=%s", updated.BindingID, got[0].BindingID)
	}
	claudeBefore := ""
	for _, binding := range installed {
		if binding.Integration == portable.Claude {
			claudeBefore = binding.BindingID
		}
	}
	if claudeBefore == "" || got[1].BindingID != claudeBefore {
		t.Fatalf("mixed repair rewrote claude: before=%s after=%s", claudeBefore, got[1].BindingID)
	}
}

func TestUAPMaterializerRepairMixedRematerializeDeletedSibling(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	r1 := filepath.Join(root, "package-r1")
	copyPackage(t, pkg, r1)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude-config")
	codexConfig := filepath.Join(root, "home", "codex-config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-0000000000ef",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: r1, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-mixed-rematerialize-install",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: r1, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-mixed-rematerialize-install",
		},
	})
	if err != nil || len(installed) != 2 {
		t.Fatalf("install: %+v %v", installed, err)
	}
	id.InstallationID = installed[0].InstallationID
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := mat.Update(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Codex, PackageRoot: pkg,
		ClientConfigRoot: codexConfig, ClientExecutable: probe,
		OperationID: "portable-mixed-rematerialize-codex-update",
	})
	if err != nil {
		t.Fatal(err)
	}
	id.InstallationID = updated.InstallationID
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("installation missing")
	}
	claudeTarget := ""
	for _, binding := range installation.Clients {
		if binding.ClientID == string(portable.Claude) {
			claudeTarget = binding.TargetLocator
		}
	}
	if claudeTarget == "" {
		t.Fatal("claude target missing")
	}
	if err := os.RemoveAll(claudeTarget); err != nil {
		t.Fatal(err)
	}
	got, err := mat.Repair(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, PackageRoot: r1,
		ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		OperationID: "portable-mixed-rematerialize-claude", Operation: uapinstaller.OpRepair,
	})
	if err != nil {
		t.Fatalf("mixed rematerialize: %v", err)
	}
	if _, err := os.Stat(claudeTarget); err != nil {
		t.Fatalf("mixed rematerialize did not restore claude: %v", err)
	}
	if got.BindingID == "" || got.InstallationID != id.InstallationID {
		t.Fatalf("mixed rematerialize claude: %+v", got)
	}
	claudeBefore := ""
	codexBefore := updated.BindingID
	for _, binding := range installed {
		if binding.Integration == portable.Claude {
			claudeBefore = binding.BindingID
		}
	}
	if claudeBefore == "" || got.BindingID != claudeBefore {
		t.Fatalf("mixed rematerialize rewrote claude: before=%s after=%s", claudeBefore, got.BindingID)
	}
	state, err = mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok = findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("installation missing after rematerialize")
	}
	codexAfter := ""
	for _, binding := range installation.Clients {
		if binding.ClientID == string(portable.Codex) {
			codexAfter = binding.ClientBindingID
		}
	}
	if codexAfter != codexBefore {
		t.Fatalf("mixed rematerialize rewrote codex sibling: before=%s after=%s", codexBefore, codexAfter)
	}
	requirePortableLocator(t, got)
	requirePortableLocator(t, updated)
}

func TestUAPMaterializerApplyGroupRepeatUnchanged(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude config")
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-00000000009a",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	reqs := []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-repeat",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-repeat",
		},
	}
	first, err := mat.ApplyGroup(testCtx(t), reqs)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	reqs[0].ExpectedGeneration = snap.Ledger.Generation
	reqs[1].ExpectedGeneration = snap.Ledger.Generation
	reqs[0].OperationID = "portable-group-repeat-again"
	reqs[1].OperationID = "portable-group-repeat-again"
	again, err := mat.ApplyGroup(testCtx(t), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0].InstallationID != first[0].InstallationID || again[1].InstallationID != first[1].InstallationID {
		t.Fatalf("repeat group: first=%+v again=%+v", first, again)
	}
	if again[0].BindingID != first[0].BindingID || again[1].BindingID != first[1].BindingID {
		t.Fatalf("repeat group rebound: first=%+v again=%+v", first, again)
	}
}

func TestUAPMaterializerRemoveGroupBothAndAlreadyAbsent(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	claudeConfig := filepath.Join(root, "home", "claude config")
	codexConfig := filepath.Join(root, "home", "codex config")
	for _, dir := range []string{claudeConfig, codexConfig} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: claudeConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-00000000009b",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-install",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-install",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	codexName, err := installed[0].Filename()
	if err != nil {
		t.Fatal(err)
	}
	claudeName, err := installed[1].Filename()
	if err != nil {
		t.Fatal(err)
	}
	_, err = mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, HoldOnly: true,
			ClientConfigRoot: codexConfig, ClientExecutable: probe,
		},
		{
			Identity: id, Integration: portable.Claude,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		},
	})
	if err == nil || !errors.Is(err, ErrPreflight) {
		t.Fatalf("hold-only group remove: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true,
			OperationID: "portable-group-remove",
		},
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].AlreadyAbsent || got[1].AlreadyAbsent {
		t.Fatalf("group remove: %+v", got)
	}
	if _, err := portable.Acquire(testCtx(t), installed[0].DataRoot, codexName); err == nil {
		t.Fatal("codex locator survived group remove")
	}
	if _, err := portable.Acquire(testCtx(t), installed[1].DataRoot, claudeName); err == nil {
		t.Fatal("claude locator survived group remove")
	}
	if _, err := os.Stat(filepath.Join(codex.RuntimeRoot, codex.Primary)); err != nil {
		t.Fatal("shared runtime removed")
	}
	_, err = mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-reinstall",
		},
		{
			Identity: id, Integration: portable.Claude,
			PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-reinstall",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: claudeConfig, ClientExecutable: probe,
		OperationID: "portable-group-remove-claude",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err = mat.RemoveGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: claudeConfig, ClientExecutable: probe,
			OperationID: "portable-group-remove-mixed",
		},
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation,
			ClientConfigRoot: codexConfig, ClientExecutable: probe, ExternalUninstalled: true,
			OperationID: "portable-group-remove-mixed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].AlreadyAbsent || got[1].AlreadyAbsent {
		t.Fatalf("mixed group remove: %+v", got)
	}
}
