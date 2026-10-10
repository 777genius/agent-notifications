//go:build linux || darwin

package portablesetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/directoryidentity"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/loader"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/profileauthority"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/specregistry"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/cursor"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
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
	compiler, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate TEST Go compiler: %v", err)
	}
	cmd := exec.Command(compiler, "build", "-o", out, src)
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
		"plugin.json":                         []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.0"}`),
		"mcp.json":                            []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"agent-notify":{"type":"stdio","command":"./bin/probe","args":[],"env":{}}}}`),
		"skills/agent-notifications/SKILL.md": []byte("---\nname: agent-notifications\ndescription: Isolated portable setup fixture\n---\nFixture only.\n"),
		"bin/probe":                           body,
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
		if binding.ClientID != string(portable.Codex) {
			if !strings.Contains(string(body), "portable-launch") || !strings.Contains(string(body), "--locator") {
				t.Fatalf("Claude locator missing from projection %s: %s", mcp, body)
			}
			sawLocator = true
			continue
		}
		var projected struct {
			MCPServers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(body, &projected); err != nil {
			t.Fatal(err)
		}
		server, ok := projected.MCPServers["agent-notify"]
		if !ok {
			t.Fatalf("agent-notify missing from projection %s: %s", mcp, body)
		}
		want := []string{"portable-launch", "--data-root", codexB.DataRoot, "--locator", codexName}
		if !reflect.DeepEqual(server.Args, want) {
			t.Fatalf("projection args = %q, want %q", server.Args, want)
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
	staging := filepath.Join(owned, ".agentplugins-staging-"+opID)
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	manager := dirswap.Manager{JournalDir: ops, Fault: func(phase string) error {
		if phase == dirswap.PhaseBackupPending {
			return errors.New("fixture: leave a pending journal")
		}
		return nil
	}}
	receipt, err := manager.Apply(context.Background(), dirswap.Input{
		OperationID: opID, ClientBindingID: "client-binding-1", Sequence: 1,
		OwnedBase: owned, ActivePath: filepath.Join(owned, "plugin"), StagingPath: staging,
		RequireAbsent: true,
	})
	if err == nil || receipt.OperationID != opID {
		t.Fatalf("create pending journal: %+v %v", receipt, err)
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
	claudeID := id
	claudeID.ScopeRoot = filepath.Join(root, "claude project scope")
	if err := os.MkdirAll(claudeID.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{
		{
			Identity: id, Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
			PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe,
			OperationID: "portable-group-codex",
		},
		{
			Identity: claudeID, Integration: portable.Claude, ExpectedGeneration: ledger.Generation,
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
	snap, err := installruntime.ReadInstalledSnapshot(id.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []Identity{id, claudeID} {
		if got[i].ScopeRoot != physicalRoot(want.ScopeRoot) || !portable.ExactCommittedBinding(snap.Ledger, got[i]) {
			t.Fatalf("group callback did not commit the selected identity: got=%+v want=%+v", got[i], want)
		}
		if exact, err := portable.ExactLocator(got[i]); err != nil || !exact {
			t.Fatalf("group locator differs from returned identity: %+v exact=%v err=%v", got[i], exact, err)
		}
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
	for _, tc := range []struct {
		name   string
		change func(*MaterializeRequest)
	}{
		{"duplicate client", func(r *MaterializeRequest) { r.Integration = portable.Codex }},
		{"unknown client", func(r *MaterializeRequest) { r.Integration = portable.Integration("unknown") }},
		{"installation", func(r *MaterializeRequest) { r.Identity.InstallationID = "00000000-0000-4000-8000-000000000098" }},
		{"component", func(r *MaterializeRequest) { r.Identity.ComponentID = "other" }},
		{"owner", func(r *MaterializeRequest) { r.Identity.Owner = "other" }},
		{"control root", func(r *MaterializeRequest) { r.Identity.ControlRoot = filepath.Join(root, "other-control") }},
		{"runtime root", func(r *MaterializeRequest) { r.Identity.RuntimeRoot = filepath.Join(root, "other-runtime") }},
		{"invalid scope", func(r *MaterializeRequest) { r.Identity.ScopeRoot = "relative" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reqs := []MaterializeRequest{
				{Identity: id, Integration: portable.Codex, ExpectedGeneration: snap.Ledger.Generation, PackageRoot: pkg, ClientConfigRoot: codexConfig, ClientExecutable: probe},
				{Identity: claudeID, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation, PackageRoot: pkg, ClientConfigRoot: claudeConfig, ClientExecutable: probe},
			}
			tc.change(&reqs[1])
			if _, err := mat.ApplyGroup(testCtx(t), reqs); err == nil {
				t.Fatal("invalid group was accepted")
			}
			after, err := installruntime.ReadInstalledSnapshot(id.ControlRoot)
			if err != nil || !reflect.DeepEqual(after.Ledger, snap.Ledger) {
				t.Fatalf("invalid group mutated the runtime: %v", err)
			}
		})
	}
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

// Regression: replacing the caller's registry with NewRegistry in either
// single or group engine construction silently re-enables an omitted client.
func TestMaterializerExplicitRegistryReachesPublicPrepare(t *testing.T) {
	mat, req, _ := registryMaterializerFixture(t)
	registry, err := clients.NewRegistry(codex.New())
	if err != nil {
		t.Fatal(err)
	}
	mat.Registry = registry
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprintf("group=%v", group), func(t *testing.T) {
			var ids map[string]Identity
			if group {
				ids = map[string]Identity{"claude": req.Identity, "codex": req.Identity}
			}
			eng, err := mat.engineWithIdentities(req, new(uint64), nil, ids)
			if err != nil {
				t.Fatal(err)
			}
			if !eng.SupportsClient("codex") || eng.SupportsClient("claude") || eng.SupportsClient("cursor") || eng.SupportsClient("local") {
				t.Fatal("engine did not retain the explicit registry")
			}
			prepared, err := eng.Prepare(testCtx(t), uapinstaller.Request{
				Operation: uapinstaller.OpInstall, ClientID: "claude",
				ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
				PackageRoot: req.PackageRoot, InstallationID: req.Identity.InstallationID,
			})
			if prepared != nil {
				_ = prepared.Close()
			}
			if !errors.Is(err, uapinstaller.ErrUnsupported) {
				t.Fatalf("real Prepare admitted unregistered Claude: %v", err)
			}
		})
	}
}

// Regression: adding selected Cursor to composition must not change a nil
// caller's Claude/Codex preparation or admit historical Cursor/Local by default.
func TestMaterializerNilRegistryPublicPrepareDefaults(t *testing.T) {
	mat, req, root := registryMaterializerFixture(t)
	eng, err := mat.engine(req, new(uint64), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"claude", "codex", "cursor", "local"} {
		t.Run(client, func(t *testing.T) {
			config := filepath.Join(root, "profiles", client)
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			prepared, err := eng.Prepare(testCtx(t), uapinstaller.Request{
				Operation: uapinstaller.OpInstall, ClientID: client,
				ClientConfigRoot: config, ClientExecutable: req.ClientExecutable,
				PackageRoot: req.PackageRoot, InstallationID: req.Identity.InstallationID,
				RequiredComponents: []string{"mcp", "skills"},
			})
			if client == "cursor" || client == "local" {
				if prepared != nil {
					_ = prepared.Close()
				}
				if !errors.Is(err, uapinstaller.ErrUnsupported) {
					t.Fatalf("default Prepare admitted %s: %v", client, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.Close() }()
			plan := prepared.Plan()
			if plan.ClientID != client || plan.Delivery.Status == string(domain.PlanUnsupported) || !plan.SelectedDelivery.IsZero() {
				t.Fatalf("default %s plan changed: %+v", client, plan)
			}
		})
	}
}

// Regression: lookup by Cursor ID alone can admit its historical MCP adapter,
// or an empty registry can fall back to defaults and mutate recovery/discovery.
func TestMaterializerCursorInvalidRegistryHasNoMutations(t *testing.T) {
	for _, kind := range []string{"nil", "empty", "zero", "codex-only", "historical-cursor"} {
		t.Run(kind, func(t *testing.T) {
			mat, req, root := registryMaterializerFixture(t)
			req.Integration = portable.Cursor
			var err error
			switch kind {
			case "empty":
				mat.Registry, err = clients.NewRegistry()
			case "zero":
				mat.Registry = new(clients.Registry)
			case "codex-only":
				mat.Registry, err = clients.NewRegistry(codex.New())
			case "historical-cursor":
				mat.Registry, err = clients.NewRegistry(cursor.New())
			}
			if err != nil {
				t.Fatal(err)
			}
			before := registryFixtureFiles(t, root)
			if _, err := mat.Install(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatalf("invalid Cursor registry install: %v", err)
			}
			if _, err := mat.PreviewPlan(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatalf("invalid Cursor registry preview: %v", err)
			}
			if err := mat.Remove(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatalf("invalid Cursor registry remove: %v", err)
			}
			if err := mat.RecoverJournals(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatalf("invalid Cursor registry recovery: %v", err)
			}
			if _, err := mat.SwitchRetained(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatalf("invalid Cursor registry retained switch: %v", err)
			}
			if after := registryFixtureFiles(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected Cursor request changed TEST files or directories")
			}
		})
	}
}

// Regression: the frozen selected vendor adapter can be replaced by defaults,
// or unsupported full ancestry can be bypassed by its QualificationID text.
// This is a real public Prepare refusal, never a physical positive or native run.
func TestMaterializerSelectedCursorUnsupportedPrepareBeforeMutation(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("NOT_RUN: frozen Cursor tuple requires Linux amd64")
	}
	mat, req, root := registryMaterializerFixture(t)
	req.Integration = portable.Cursor
	if _, err := profileauthority.Capture(testCtx(t), req.ClientConfigRoot); !errors.Is(err, directoryidentity.ErrUnsupported) {
		if err != nil {
			t.Fatal(err)
		}
		t.Skip("NOT_RUN: this negative regression requires unsupported full ancestry; no native attempt authorized")
	}
	// ReserveIdentity is inert and uses the actual public Cursor target resolver.
	var err error
	mat.Registry, err = clients.NewRegistry(cursor.New())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := mat.engine(req, new(uint64), nil)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{
		ClientID: "cursor", InstallationID: req.Identity.InstallationID,
		DeclaredName: "agent-notify", ClientConfigRoot: req.ClientConfigRoot,
	})
	if err != nil || reserved.BindingID == "" {
		t.Fatalf("reserve actual Cursor target: %+v %v", reserved, err)
	}
	data := filepath.Join(mat.Roots.PluginDataBase, domain.ComputePhysicalArtifactID("agent-notify", reserved.InstallationID))
	b, err := Complete(req.Identity, portable.Cursor, "cursor", reserved.Scope, reserved.TargetPath, data)
	if err != nil {
		t.Fatal(err)
	}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(req.ClientExecutable)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, &cursorinstall.Authority{
		ProfileRoot: req.ClientConfigRoot, CursorVersion: "2026.09.28-64d2043",
		// Deliberately no affirmative evidence: this label must never grant.
		QualificationID: "TEST-unsupported-ancestry-NOT-qualified",
		Executable:      req.ClientExecutable, ExecutableDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)),
		Selector: filepath.Join(b.DataRoot, name), ObjectID: "TEST-selected-cursor-stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	mat.Registry, err = clients.NewRegistry(adapter, codex.New())
	if err != nil {
		t.Fatal(err)
	}
	before := registryFixtureFiles(t, root)
	eng, err = mat.engine(req, new(uint64), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := eng.Prepare(testCtx(t), uapinstaller.Request{
		Operation: uapinstaller.OpInstall, ClientID: "cursor", InstallationID: req.Identity.InstallationID,
		PackageRoot: req.PackageRoot, ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		RequiredComponents: []string{"mcp", "skills"},
	})
	if prepared != nil {
		_ = prepared.Close()
	}
	if !errors.Is(err, directoryidentity.ErrUnsupported) {
		t.Fatalf("public Prepare bypassed unsupported original ancestry: %v", err)
	}
	if _, err := mat.PreviewPlan(testCtx(t), req); !errors.Is(err, directoryidentity.ErrUnsupported) {
		t.Fatalf("selected preview bypassed unsupported original ancestry: %v", err)
	}
	if _, err := mat.PreviewInstall(testCtx(t), req); !errors.Is(err, directoryidentity.ErrUnsupported) {
		t.Fatalf("recovering preview bypassed unsupported original ancestry: %v", err)
	}
	if err := mat.RecoverJournals(testCtx(t), req); !errors.Is(err, directoryidentity.ErrUnsupported) {
		t.Fatalf("recovery bypassed unsupported original ancestry: %v", err)
	}
	if _, err := mat.Install(testCtx(t), req); !errors.Is(err, directoryidentity.ErrUnsupported) {
		t.Fatalf("selected Cursor did not reach public unsupported refusal: %v", err)
	}
	if after := registryFixtureFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("unsupported physical Prepare changed TEST files or directories")
	}
	t.Log("negative refusal verified; positive physical/installed/native Cursor NOT_RUN")
}

// Regression: admission alone can leave selected ProjectArgs bypassed, so an
// acknowledged binding has empty MCP argv or a source digest used as projection
// ownership. Exercise actual Materializer/public Engine and TEST file readback.
func TestMaterializerSelectedCursorPublicProjectionAndAcknowledgement(t *testing.T) {
	mat, req, _ := registryMaterializerFixture(t)
	ctx := testCtx(t)
	req.Integration = portable.Cursor
	token, err := profileauthority.Capture(ctx, req.ClientConfigRoot)
	if errors.Is(err, directoryidentity.ErrUnsupported) {
		if !token.IsZero() {
			t.Fatal("unsupported original ancestry supplied authority")
		}
		t.Logf("NOT_RUN positive materializer: original full ancestry unsupported: %v; installed/native E NOT_RUN", err)
		return
	}
	if err != nil || token.IsZero() {
		t.Fatalf("actual original profile Capture: %v", err)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Log("NOT_RUN positive materializer: selected Linux amd64 tuple required; installed/native E NOT_RUN")
		return
	}
	// Accepted source tuple is an additional fixture bound, never physical proof.
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil {
		t.Fatal(err)
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+string(osRelease), "\nID=ubuntu\n") ||
		!strings.Contains(string(osRelease), `VERSION="24.04.5 LTS`) || strings.TrimSpace(string(kernel)) != "6.17.0-1022-azure" {
		t.Log("NOT_RUN positive materializer: accepted Ubuntu24.04/kernel tuple required; installed/native E NOT_RUN")
		return
	}
	// ReserveIdentity resolves the real target without granting historical Cursor.
	mat.Registry, err = clients.NewRegistry(cursor.New())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := mat.engine(req, new(uint64), nil)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{
		ClientID: "cursor", InstallationID: req.Identity.InstallationID,
		DeclaredName: "agent-notify", ClientConfigRoot: req.ClientConfigRoot,
	})
	if err != nil || reserved.BindingID == "" {
		t.Fatalf("reserve real target: %+v %v", reserved, err)
	}
	data := filepath.Join(mat.Roots.PluginDataBase, domain.ComputePhysicalArtifactID("agent-notify", reserved.InstallationID))
	expected, err := Complete(req.Identity, portable.Cursor, "cursor", reserved.Scope, reserved.TargetPath, data)
	if err != nil {
		t.Fatal(err)
	}
	name, err := expected.Filename()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(req.ClientExecutable)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, &cursorinstall.Authority{
		ProfileRoot: req.ClientConfigRoot, CursorVersion: "2026.09.28-64d2043",
		QualificationID: "TEST-materializer-source-only-public098",
		Executable:      req.ClientExecutable, ExecutableDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)),
		Selector: filepath.Join(expected.DataRoot, name), ObjectID: "TEST-materializer-stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	mat.Registry, err = clients.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	eng, err = mat.engine(req, new(uint64), nil)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := eng.LocalPackageTreeDigest(ctx, req.PackageRoot)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource := registryFixtureFiles(t, req.PackageRoot)
	before, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || portable.ExactCommittedBinding(before.Ledger, expected) {
		t.Fatal("fresh TEST fixture already acknowledged", err)
	}
	preview, err := mat.PreviewPlan(ctx, req)
	if err != nil || preview.ProfileAuthority == nil || !preview.ProfileAuthority.Equal(token) || preview.SelectedDelivery.IsZero() || preview.TreeDigest != canonical {
		t.Fatalf("actual selected public preview: %+v %v", preview, err)
	}
	binding, err := mat.Install(ctx, req)
	if err != nil || binding != expected {
		t.Fatalf("actual selected Materializer install: %+v %v", binding, err)
	}
	state, err := (statev2.Store{Path: mat.Roots.StateFile}).Load()
	if err != nil || len(state.Installations) != 1 {
		t.Fatal("public committed TEST state missing", err)
	}
	i := state.Installations[0]
	c := i.Clients[expected.BindingID]
	facts, selected := c.SelectedDelivery.CursorFacts()
	// Source registration prepares Cursor; native activation is not run here.
	if !selected || c.ProfileAuthority == nil || !c.ProfileAuthority.Equal(token) || c.ProfileNamespace != filepath.Dir(mat.Roots.StateFile) ||
		i.Source.TreeDigest != canonical || c.PackageRevision == nil || c.PackageRevision.TreeDigest != canonical ||
		facts.CanonicalDigest != canonical || facts.ProjectionDigest == "" || facts.ProjectionDigest == canonical ||
		c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" || c.Activation != domain.ActivationPrepared || c.Verification != domain.VerificationPackageValid {
		t.Fatalf("selected installed source/token/ack differs: %+v %+v", c, facts)
	}
	ack := false
	for _, object := range c.NativeObjects {
		if object.Kind == "cursor_user_stop" {
			ack = object.CursorReceipt == facts.PlannedReceipt
		}
	}
	if !ack || c.SelectedDelivery.ValidateCursorObjects(c.NativeObjects) != nil {
		t.Fatal("real selected native object acknowledgement absent")
	}
	// Read actual no-follow hook bytes; a repair-capable Prepare is not proof
	// that the acknowledged entry is still installed. No Run/Stop is invoked.
	hooks, err := nativeconfig.New().ReadExactFile(facts.HooksPath)
	if err != nil || !hooks.Exists {
		t.Fatal("actual selected hook bytes missing", err)
	}
	r := facts.PlannedReceipt
	if err := cursorhooks.VerifyOwned(hooks.Body, &cursorhooks.Receipt{
		Version: r.Version, Event: r.Event, Shell: cursorhooks.ShellContract(r.Shell),
		Spec:        cursorhooks.HookSpec{Executable: r.Executable, Selector: r.Selector},
		EntryDigest: r.EntryDigest, RemainderDigest: r.RemainderDigest,
	}); err != nil {
		t.Fatal("actual selected native object readback", err)
	}
	receipt, ok := i.DataReceipts[c.DataReceiptID]
	if !ok || receipt.DataReceiptID == "" || receipt.State != domain.DataReceiptOwned || receipt.Locator != expected.DataRoot || receipt.OwnershipDigest == "" {
		t.Fatalf("real data receipt not bound: %+v", receipt)
	}
	if err := (providers.PluginDataManager{Base: mat.Roots.PluginDataBase}).ValidateData(ctx, receipt); err != nil {
		t.Fatal("actual data ownership readback", err)
	}
	var mcp struct {
		Servers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	projected, err := os.ReadFile(filepath.Join(c.TargetLocator, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(projected, &mcp); err != nil || !reflect.DeepEqual(mcp.Servers[portableServerName].Args, []string{"portable-launch", "--locator", name}) {
		t.Fatalf("actual ProjectArgs locator argv absent: %s %v", projected, err)
	}
	stager := providers.Stager{SnapshotBuilder: packagesnapshot.Builder{TempRoot: filepath.Join(filepath.Dir(mat.Roots.StateFile), "tmp")}}
	if err := stager.Verify(ctx, c.TargetLocator, facts.ProjectionDigest); err != nil {
		t.Fatal("projected byte digest readback", err)
	}
	if !reflect.DeepEqual(beforeSource, registryFixtureFiles(t, req.PackageRoot)) {
		t.Fatal("projection changed canonical source bytes or modes")
	}
	installed, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || !portable.ExactCommittedBinding(installed.Ledger, binding) || installed.Ledger.PendingMutation != nil {
		t.Fatal("real AN binding not acknowledged or pending survived", err)
	}
	if err := eng.VerifyProfileAuthority(ctx, i.InstallationID, c.ClientBindingID); err != nil {
		t.Fatal("original physical token no longer validates", err)
	}
	view, err := eng.Inspect(ctx)
	if err != nil || view.Recovery.Required || len(view.Installations) != 1 || len(view.Installations[0].Bindings) != 1 || view.Installations[0].Bindings[0].Verification != string(domain.VerificationPackageValid) {
		t.Fatalf("public installed readback: %+v %v", view, err)
	}
	t.Log("QUALIFIED_TEST_MATERIALIZER_SELECTED_PROJECTION_ACK_DATA_ORIGINAL_TOKEN=true; TEST filesystem source proof; installed/native E NOT_RUN")
}

func registryMaterializerFixture(t *testing.T) (Materializer, MaterializeRequest, string) {
	t.Helper()
	b, ledger := bindingFixture(t)
	root := filepath.Dir(b.ControlRoot)
	probe := filepath.Join(b.RuntimeRoot, b.Primary)
	pkg := filepath.Join(root, "TEST-package")
	writePackage(t, pkg, probe)
	config := filepath.Join(root, "TEST-profile")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	uapRoot := filepath.Join(root, "TEST-uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:      filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:       filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:  filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mat, MaterializeRequest{
		Identity: Identity{InstallationID: "00000000-0000-4000-8000-000000000239",
			ComponentID: b.ComponentID, Owner: b.Owner, ScopeRoot: b.ScopeRoot,
			ControlRoot: b.ControlRoot, GlobalConfig: b.GlobalConfig, RuntimeRoot: b.RuntimeRoot, Primary: b.Primary},
		Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe, OperationID: "TEST-registry",
	}, root
}

// Snapshot the fresh TEST fixture, including directory creation and file modes.
func registryFixtureFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if !entry.IsDir() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(" sha256:%x", sha256.Sum256(body))
		}
		out[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Historical vscode.New and default registries cannot impersonate the selected Local owner.
func TestMaterializerLocalInvalidRegistryHasNoMutations(t *testing.T) {
	for _, kind := range []string{"nil", "empty", "historical-vscode", "codex-only"} {
		t.Run(kind, func(t *testing.T) {
			mat, req, root := registryMaterializerFixture(t)
			req.Integration = portable.CopilotVSCode
			req.LocalConfig = &vscode.LocalConfig{ProfileSettingsPath: filepath.Join(req.ClientConfigRoot, "settings.json")}
			var err error
			switch kind {
			case "empty":
				mat.Registry, err = clients.NewRegistry()
			case "historical-vscode":
				mat.Registry, err = clients.NewRegistry(vscode.New())
			case "codex-only":
				mat.Registry, err = clients.NewRegistry(codex.New())
			}
			if err != nil {
				t.Fatal(err)
			}
			before := registryFixtureFiles(t, root)
			if _, err := mat.Install(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatal("Local install registry", err)
			}
			if _, err := mat.PreviewPlan(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatal("Local preview registry", err)
			}
			if err := mat.Remove(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatal("Local remove registry", err)
			}
			if err := mat.RecoverJournals(testCtx(t), req); !errors.Is(err, ErrPreflight) {
				t.Fatal("Local recovery registry", err)
			}
			if after := registryFixtureFiles(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid Local registry mutated TEST state")
			}
		})
	}
}

// Exercise the actual public selected projection in read-only Prepare. Linux
// source preparation is not Darwin installed authority or native activation.
func TestMaterializerLocalIndependentPublicPrepare(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("source-only Linux projection check")
	}
	for bits := 1; bits < 8; bits++ {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			mat, req, _ := registryMaterializerFixture(t)
			req.Integration = portable.CopilotVSCode
			req.Identity.ScopeRoot = req.ClientConfigRoot
			settings := filepath.Join(req.ClientConfigRoot, "settings.json")
			foreign := []byte("{ // retained TEST JSONC\n \"foreign\": true\n}\n")
			if err := os.WriteFile(settings, foreign, 0600); err != nil {
				t.Fatal(err)
			}
			tuple := vscode.SourceQualifiedTESTTuple("linux")
			cfg := &vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.LinuxSH}, NativeStop: bits&1 != 0}
			if bits&2 != 0 {
				cfg.MCPServers = []string{"agent-notify"}
			}
			if bits&4 != 0 {
				cfg.Skills = []string{"agent-notifications"}
			}
			if cfg.NativeStop {
				cfg.HookSpecs = copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: req.Identity.ControlRoot, BindingID: "TEST-local-binding"}, mat.Roots.HelperExecutable)
				body, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
				if err != nil {
					t.Fatal(err)
				}
				cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
			}
			adapter, err := vscode.NewLocal(*cfg)
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(adapter)
			if err != nil {
				t.Fatal(err)
			}
			req.LocalConfig = cfg
			// Unselected components need not exist in the canonical package.
			if bits&2 == 0 {
				if err := os.Remove(filepath.Join(req.PackageRoot, "mcp.json")); err != nil {
					t.Fatal(err)
				}
			}
			if bits&4 == 0 {
				if err := os.RemoveAll(filepath.Join(req.PackageRoot, "skills")); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string]map[string]string{}
			for _, root := range []string{req.PackageRoot, req.ClientConfigRoot, req.Identity.ControlRoot} {
				before[root] = registryFixtureFiles(t, root)
			}
			plan, err := mat.PreviewPlan(testCtx(t), req)
			if err != nil {
				t.Fatal("actual public Local Prepare", err)
			}
			facts, ok := plan.SelectedDelivery.LocalFacts()
			if !ok || plan.ClientID != "vscode" || facts.NativeStop != cfg.NativeStop || !reflect.DeepEqual(facts.MCPServers, cfg.MCPServers) || !reflect.DeepEqual(facts.Skills, cfg.Skills) {
				t.Fatalf("selection lost: %+v", plan)
			}
			if plan.BindingID != domain.ComputeClientBindingID(req.Identity.InstallationID, "vscode", "user", plan.TargetPath) {
				t.Fatal("BindingID uses portable registration ID")
			}
			binding, err := Complete(req.Identity, portable.CopilotVSCode, plan.ClientID, "user", plan.TargetPath, req.Identity.ControlRoot)
			if err != nil || binding.Integration != portable.CopilotVSCode || binding.BindingID != plan.BindingID {
				t.Fatal("public to portable binding", err)
			}
			for root, files := range before {
				if after := registryFixtureFiles(t, root); !reflect.DeepEqual(files, after) {
					t.Fatal("read-only Prepare mutated source, profile or kernel")
				}
			}
			for _, path := range []string{mat.Roots.StateFile, mat.Roots.ManagedRoot, mat.Roots.PluginDataBase} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("Prepare wrote installed state", path, err)
				}
			}
			// Stage the actual selected public projection, without activation/profile effects.
			ctx := testCtx(t)
			projectionMat, projectionReq, closePackage, err := mat.composeLocalPackage(ctx, req, false)
			if err != nil {
				t.Fatal(err)
			}
			defer closePackage()
			mat, req = projectionMat, projectionReq
			source, err := (packagedigest.Builder{TempRoot: t.TempDir()}).SnapshotWithExecutables(ctx, req.PackageRoot, domain.SourceIdentity{CanonicalSource: req.PackageRoot}, []string{"bin/probe"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = packagedigest.Remove(source) }()
			schema, err := specregistry.New()
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := (loader.Loader{Registry: schema}).LoadSnapshot(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			if envelope.TreeDigest != plan.TreeDigest {
				t.Fatal("projection input differs from real public Prepare")
			}
			deliveryPlan := domain.DeliveryPlan{ClientID: domain.ClientVSCode, Scope: domain.ScopeUser, Status: domain.PlanStatus(plan.Delivery.Status), PackageMode: domain.PackageMode(plan.Delivery.PackageMode), ActivePath: plan.TargetPath, TargetAnchor: mat.Roots.ManagedRoot, TargetRoot: filepath.Dir(plan.TargetPath), SelectedDelivery: plan.SelectedDelivery}
			for _, component := range plan.Delivery.Components {
				deliveryPlan.Components = append(deliveryPlan.Components, domain.ComponentDecision{Kind: domain.ComponentKind(component.Kind), Name: component.Name, Support: domain.SupportLevel(component.Support), Reason: component.Reason})
			}
			launcher, err := managedstdio.NewSource(mat.Roots.HelperExecutable, "TEST")
			if err != nil {
				t.Fatal(err)
			}
			stager := providers.Stager{Registry: mat.Registry, Paths: pathpolicy.Policy{}, SnapshotBuilder: packagesnapshot.Builder{TempRoot: t.TempDir()}, LauncherSource: launcher}
			staged, err := stager.StageWithPluginData(ctx, envelope, deliveryPlan, "TEST-local-projection", domain.CompatibilityHints{}, filepath.Join(req.Identity.ControlRoot, "TEST-data"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stager.Discard(ctx, staged); err != nil {
					t.Error(err)
				}
			}()
			if err := stager.Verify(ctx, staged.StagingPath, staged.ArtifactDigest); err != nil {
				t.Fatal("projected bytes digest", err)
			}
			hookPath := filepath.Join(staged.StagingPath, filepath.FromSlash(vscodelocalhooks.PluginPath))
			if cfg.NativeStop {
				body, err := os.ReadFile(hookPath)
				if err != nil {
					t.Fatal(err)
				}
				expected := []vscodelocalhooks.Spec{{Event: vscodelocalhooks.Stop, Executable: mat.Roots.HelperExecutable, Args: []string{"copilot-vscode-event", "--event", "Stop", "--control-root", req.Identity.ControlRoot, "--binding", plan.BindingID}, TimeoutSeconds: 5}}
				if err := vscodelocalhooks.VerifyOwned(body, cfg.TargetShell, expected); err != nil {
					t.Fatal("canonical Stop argv/timeout", err)
				}
			} else if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
				t.Fatal("unselected native hook exists", err)
			}
			if bits&2 != 0 {
				if _, err := os.Stat(filepath.Join(staged.StagingPath, "mcp.json")); err != nil {
					t.Fatal(err)
				}
			} else if _, err := os.Stat(filepath.Join(staged.StagingPath, "mcp.json")); !os.IsNotExist(err) {
				t.Fatal("unselected MCP projected", err)
			}
			if bits&4 != 0 {
				if _, err := os.Stat(filepath.Join(staged.StagingPath, "skills", "agent-notifications", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			} else if _, err := os.Stat(filepath.Join(staged.StagingPath, "skills", "agent-notifications", "SKILL.md")); !os.IsNotExist(err) {
				t.Fatal("unselected skill projected", err)
			}
			if after, err := os.ReadFile(settings); err != nil || !bytes.Equal(after, foreign) {
				t.Fatal("staging changed foreign profile", err)
			}
			t.Log("PASS public Prepare and real public staged projection/digest; installed selector/data/locator and Darwin authority NOT_RUN")
		})
	}
}

// Hold the actual public mutation lock so Apply fails only after the host
// reservation and both revocations; retry must recover the same frozen owners.
func TestLocalRemoveGroupFailedApplyExactOwnerRetry(t *testing.T) {
	mat, local, root := registryMaterializerFixture(t)
	local.Integration = portable.CopilotVSCode
	local.Identity.ScopeRoot = local.ClientConfigRoot
	settings := filepath.Join(local.ClientConfigRoot, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"foreign":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
	if runtime.GOOS == "darwin" {
		tuple = vscode.QualifiedDarwinTESTTuple()
	}
	cfg := &vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, MCPServers: []string{"agent-notify"}, Skills: []string{"agent-notifications"}}
	adapter, err := vscode.NewLocal(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	mat.Registry, err = clients.NewRegistry(adapter, codex.New())
	if err != nil {
		t.Fatal(err)
	}
	local.LocalConfig = cfg
	b, err := mat.Install(testCtx(t), local)
	if err != nil {
		t.Fatal("Local public install", err)
	}
	sibling := local
	sibling.Integration, sibling.LocalConfig = portable.Codex, nil
	sibling.ClientConfigRoot = filepath.Join(root, "TEST-codex")
	if err := os.MkdirAll(sibling.ClientConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	sibling.ExpectedGeneration = snap.Ledger.Generation
	sibling.OperationID = "TEST-sibling-install"
	sibling.ExternalUninstalled = true
	peer, err := mat.Install(testCtx(t), sibling)
	if err != nil {
		t.Fatal("sibling public install", err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	local.ExpectedGeneration, sibling.ExpectedGeneration = snap.Ledger.Generation, snap.Ledger.Generation
	local.OperationID, sibling.OperationID = "TEST-group-remove", "TEST-group-remove"
	release, err := installruntime.LockExisting(testCtx(t), mat.Roots.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	_, err = mat.RemoveGroup(ctx, []MaterializeRequest{local, sibling})
	cancel()
	release()
	if err == nil {
		t.Fatal("Apply ignored held public lock")
	}
	snap, err = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, exact := range []portable.Binding{b, peer} {
		key, _, _, _ := exact.Registration()
		if _, present := snap.Ledger.Consumers[key]; present {
			t.Fatal("failure preceded revocation")
		}
		present, e := portable.ExactLocator(exact)
		if e != nil || present {
			t.Fatal("revoked locator remains", e)
		}
	}
	intent, err := ReadIntent(b.ControlRoot)
	if err != nil || snap.Ledger.PendingMutation == nil {
		t.Fatal("failed Apply lost intent", err)
	}
	found := false
	for _, target := range intent.Targets {
		if target.BindingID == b.BindingID {
			found = true
			if target.Client != "copilot-vscode" || target.OldBinding == nil || *target.OldBinding != b {
				t.Fatalf("Local durable owner: %+v", target)
			}
		}
	}
	if !found {
		t.Fatal("Local frozen target absent")
	}
	local.ExpectedGeneration, sibling.ExpectedGeneration = snap.Ledger.Generation, snap.Ledger.Generation
	if _, err := mat.RemoveGroup(testCtx(t), []MaterializeRequest{local, sibling}); err != nil {
		t.Fatal("exact-owner retry", err)
	}
	state, err := mat.Store.Load()
	if err != nil || len(state.Installations) != 1 || len(state.Installations[0].Clients) != 0 || !state.Installations[0].DataRetained {
		t.Fatal("retry did not clean same public owners", err)
	}
	body, err := os.ReadFile(settings)
	if err != nil || !bytes.Contains(body, []byte(`"foreign":true`)) {
		t.Fatal("foreign settings lost", err)
	}
}

func TestLocalOwnedEmptyPublicUpdateAndUninstall(t *testing.T) {
	for _, action := range []string{"update", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			mat, req, _ := registryMaterializerFixture(t)
			req.Integration = portable.CopilotVSCode
			req.Identity.ScopeRoot = req.ClientConfigRoot
			settings := filepath.Join(req.ClientConfigRoot, "settings.json")
			if err := os.WriteFile(settings, []byte(`{"foreign":true}`), 0600); err != nil {
				t.Fatal(err)
			}
			tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
			if runtime.GOOS == "darwin" {
				tuple = vscode.QualifiedDarwinTESTTuple()
			}
			cfg := vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, NativeStop: true, HookSpecs: copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: req.Identity.ControlRoot, BindingID: "reserved"}, mat.Roots.HelperExecutable)}
			hook, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
			if err != nil {
				t.Fatal(err)
			}
			cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(hook))
			adapter, err := vscode.NewLocal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(adapter)
			if err != nil {
				t.Fatal(err)
			}
			req.LocalConfig = &cfg
			b, err := mat.Install(testCtx(t), req)
			if err != nil {
				t.Fatal("public initial Local", err)
			}
			state, err := mat.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			recorded := state.Installations[0].Clients[b.BindingID]
			facts, _ := recorded.SelectedDelivery.LocalFacts()
			if !facts.NativeStop {
				t.Fatal("initial Stop absent")
			}
			cfg.NativeStop, cfg.HookSpecs, cfg.DeclaredHookDigest = false, nil, ""
			adapter, err = vscode.NewLocal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(adapter)
			if err != nil {
				t.Fatal(err)
			}
			req.LocalConfig = &cfg
			snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			req.ExpectedGeneration = snap.Ledger.Generation
			req.OperationID = "TEST-empty-" + action
			if action == "update" {
				req.Operation = uapinstaller.OpUpdate
				if _, err := mat.Install(testCtx(t), req); err != nil {
					t.Fatal("owned empty update", err)
				}
				state, err = mat.Store.Load()
				if err != nil {
					t.Fatal(err)
				}
				current := state.Installations[0].Clients[b.BindingID]
				empty, _ := current.SelectedDelivery.LocalFacts()
				if empty.NativeStop || len(empty.MCPServers) != 0 || len(empty.Skills) != 0 {
					t.Fatal("empty transition retained components")
				}
				if _, err := os.Stat(filepath.Join(current.TargetLocator, filepath.FromSlash(vscodelocalhooks.PluginPath))); !os.IsNotExist(err) {
					t.Fatal("old Stop file remains", err)
				}

				snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
				if err != nil {
					t.Fatal(err)
				}
				req.ExpectedGeneration = snap.Ledger.Generation
				req.OperationID += "-repeat"
				if _, err := mat.Install(testCtx(t), req); err != nil {
					t.Fatal("repeated owned empty update", err)
				}
			} else {
				if err := mat.Remove(testCtx(t), req); err != nil {
					t.Fatal("empty selected uninstall", err)
				}
				state, err = mat.Store.Load()
				if err != nil || len(state.Installations[0].Clients) != 0 {
					t.Fatal("uninstall kept binding", err)
				}
			}
			body, err := os.ReadFile(settings)
			if err != nil || !bytes.Contains(body, []byte(`"foreign":true`)) {
				t.Fatal("empty cleanup lost foreign JSON", err)
			}
		})
	}
}

func TestLocalStandardPortablePackagePublicPrepareAndFrozenRetry(t *testing.T) {
	mat, req, root := registryMaterializerFixture(t)
	built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.0.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: mat.Roots.HelperExecutable, OutputRoot: filepath.Join(root, "TEST-standard-portable")})
	if err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = built.Root
	req.Integration = portable.CopilotVSCode
	req.Identity.ScopeRoot = req.ClientConfigRoot
	tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
	if runtime.GOOS == "darwin" {
		tuple = vscode.QualifiedDarwinTESTTuple()
	}
	cfg := vscode.LocalConfig{ProfileSettingsPath: filepath.Join(req.ClientConfigRoot, "settings.json"), QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, NativeStop: true, HookSpecs: copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: req.Identity.ControlRoot, BindingID: "reserved"}, mat.Roots.HelperExecutable)}
	if err := os.WriteFile(cfg.ProfileSettingsPath, []byte(`{"foreign":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	hook, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(hook))
	adapter, err := vscode.NewLocal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mat.Registry, err = clients.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	req.LocalConfig = &cfg
	before := registryFixtureFiles(t, built.Root)
	plan, err := mat.PreviewPlan(testCtx(t), req)
	if err != nil {
		t.Fatal("standard portable public Prepare", err)
	}
	retry, err := mat.PreviewPlan(testCtx(t), req)
	if err != nil || retry.TreeDigest != plan.TreeDigest || retry.BindingID != plan.BindingID {
		t.Fatal("recomposed retry changed identity", err)
	}
	if after := registryFixtureFiles(t, built.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("composition changed shared standard package")
	}
	if _, err := os.Stat(filepath.Join(built.Root, filepath.FromSlash(vscodelocalhooks.PluginPath))); !os.IsNotExist(err) {
		t.Fatal("test supplied canonical hook", err)
	}
	req.TreeDigest = plan.TreeDigest
	manifest := filepath.Join(built.Root, "plugin.json")
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := mat.Install(testCtx(t), req); !errors.Is(err, ErrSourceIdentityDrift) {
		t.Fatal("frozen retry adopted changed source", err)
	}
}

// Regression: the standard shared package has no Local Stop hook. ApplyGroup
// must compose it from the reserved Local binding before public Prepare.
func TestLocalNativeGroupComposesSharedCanonicalPackage(t *testing.T) {
	for _, localFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(localFirst), func(t *testing.T) {
			mat, local, root := registryMaterializerFixture(t)
			local.Integration = portable.CopilotVSCode
			local.Identity.ScopeRoot = local.ClientConfigRoot
			settings := filepath.Join(local.ClientConfigRoot, "settings.json")
			if err := os.WriteFile(settings, []byte(`{"foreign":true}`), 0600); err != nil {
				t.Fatal(err)
			}
			tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
			if runtime.GOOS == "darwin" {
				tuple = vscode.QualifiedDarwinTESTTuple()
			}
			cfg := &vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, NativeStop: true, MCPServers: []string{"agent-notify"}, Skills: []string{"agent-notifications"}}
			cfg.HookSpecs = copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: local.Identity.ControlRoot, BindingID: "TEST-placeholder"}, mat.Roots.HelperExecutable)
			hook, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
			if err != nil {
				t.Fatal(err)
			}
			cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(hook))
			adapter, err := vscode.NewLocal(*cfg)
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(adapter, codex.New())
			if err != nil {
				t.Fatal(err)
			}
			local.LocalConfig = cfg
			sibling := local
			sibling.Integration, sibling.LocalConfig = portable.Codex, nil
			sibling.ClientConfigRoot = filepath.Join(root, "TEST-codex-group")
			sibling.ClientExecutable = buildProbe(t)
			if err := os.MkdirAll(sibling.ClientConfigRoot, 0700); err != nil {
				t.Fatal(err)
			}
			before := registryFixtureFiles(t, local.PackageRoot)
			reqs := []MaterializeRequest{local, sibling}
			if !localFirst {
				reqs[0], reqs[1] = reqs[1], reqs[0]
			}
			bindings, err := mat.ApplyGroup(testCtx(t), reqs)
			if err != nil {
				t.Fatal("actual public group apply", err)
			}
			if len(bindings) != 2 {
				t.Fatalf("bindings: %+v", bindings)
			}
			state, err := mat.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			installation, ok := findInstallation(state, local.Identity.InstallationID)
			if !ok || len(installation.Clients) != 2 {
				t.Fatal("missing shared installation")
			}
			var tree string
			for _, b := range bindings {
				record := installation.Clients[b.BindingID]
				if record.PackageRevision == nil {
					t.Fatal("missing selected package revision")
				}
				if tree == "" {
					tree = record.PackageRevision.TreeDigest
				} else if record.PackageRevision.TreeDigest != tree {
					t.Fatal("group selected different canonical trees")
				}
				if b.Integration == portable.CopilotVSCode {
					facts, ok := record.SelectedDelivery.LocalFacts()
					if !ok || !facts.NativeStop || facts.CanonicalDigest != tree {
						t.Fatal("missing selected Local identity")
					}
					body, err := os.ReadFile(filepath.Join(record.TargetLocator, filepath.FromSlash(vscodelocalhooks.PluginPath)))
					if err != nil {
						t.Fatal(err)
					}
					expected := []vscodelocalhooks.Spec{{Event: vscodelocalhooks.Stop, Executable: mat.Roots.HelperExecutable, Args: []string{"copilot-vscode-event", "--event", "Stop", "--control-root", b.ControlRoot, "--binding", b.BindingID}, TimeoutSeconds: 5}}
					if err := vscodelocalhooks.VerifyOwned(body, cfg.TargetShell, expected); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !reflect.DeepEqual(before, registryFixtureFiles(t, local.PackageRoot)) {
				t.Fatal("composition changed shared release bytes")
			}

			// Advance only Codex, then repair each client from its retained
			// canonical revision. Local composition must not replace Codex's root.
			retainedLocal := installation.Source.CanonicalSource
			for _, b := range bindings {
				if b.Integration == portable.CopilotVSCode {
					cfg.HookSpecs = copilotvscodeinstall.LocalHookSpecs(b, mat.Roots.HelperExecutable)
				}
			}
			reservedHook, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
			if err != nil {
				t.Fatal(err)
			}
			cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(reservedHook))
			selectedAdapter, err := vscode.NewLocal(*cfg)
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(selectedAdapter, codex.New())
			if err != nil {
				t.Fatal(err)
			}
			newPackage := filepath.Join(root, "TEST-codex-revision-two")
			copyPackage(t, retainedLocal, newPackage)
			if err := os.Chmod(newPackage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(newPackage, "plugin.json"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(newPackage, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
				t.Fatal(err)
			}
			sibling.PackageRoot, sibling.ExpectedGeneration = newPackage, 0
			sibling.OperationID = "TEST-codex-revision-update"
			if _, err := mat.Update(testCtx(t), sibling); err != nil {
				t.Fatal("independent sibling update", err)
			}
			mixed, err := mat.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			mixedInstall, ok := findInstallation(mixed, local.Identity.InstallationID)
			if !ok {
				t.Fatal("missing mixed installation")
			}
			if mixedInstall.Clients[bindings[0].BindingID].PackageRevision.TreeDigest == mixedInstall.Clients[bindings[1].BindingID].PackageRevision.TreeDigest {
				t.Fatal("fixture did not retain distinct revisions")
			}
			local.PackageRoot, local.Operation = retainedLocal, uapinstaller.OpRepair
			currentSnapshot, err := installruntime.ReadInstalledSnapshot(local.Identity.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			local.ExpectedGeneration, sibling.ExpectedGeneration = currentSnapshot.Ledger.Generation, currentSnapshot.Ledger.Generation
			sibling.Operation = uapinstaller.OpRepair
			local.OperationID, sibling.OperationID = "TEST-local-mixed-repair", "TEST-local-mixed-repair"
			// Public repair uses the current desired revision as its first source.
			repairs := []MaterializeRequest{sibling, local}
			// Current public Local activation remains Prepared. Repair must pass
			// revision preflight and preserve that truthful incomplete outcome,
			// rather than attempt to rewrite Codex to Local's older tree.
			_, repairErr := mat.ApplyGroup(testCtx(t), repairs)
			var incomplete ResultError
			if !errors.As(repairErr, &incomplete) || incomplete.Result.Outcome != uapinstaller.OutcomeIncomplete || incomplete.Result.Reason != "committed binding is not activated" {
				t.Fatal("mixed retained repair must reach public activation boundary", repairErr)
			}
			afterRepair, err := mat.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			repaired, ok := findInstallation(afterRepair, local.Identity.InstallationID)
			if !ok {
				t.Fatal("repair lost installation")
			}
			for id, previous := range mixedInstall.Clients {
				current, ok := repaired.Clients[id]
				if !ok || !reflect.DeepEqual(current.PackageRevision, previous.PackageRevision) || !reflect.DeepEqual(current.SelectedDelivery, previous.SelectedDelivery) || current.TargetLocator != previous.TargetLocator {
					t.Fatal("repair rewrote retained client revision", id)
				}
			}
		})
	}
}
