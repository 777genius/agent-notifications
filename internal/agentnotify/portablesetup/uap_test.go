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
	}); err == nil || !errors.Is(err, ErrPreflight) {
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
