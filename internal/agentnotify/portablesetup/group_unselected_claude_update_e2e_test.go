//go:build linux || darwin

package portablesetup

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

// Public host/SDK three-client lifecycle for #595: update only Codex+Local,
// retaining the exact live Claude binding and installed native profile bytes.
func TestGroupUpdateCodexLocalPreservesUnselectedClaude(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		t.Skip("Local selected delivery has no qualified Darwin Intel tuple")
	}
	ctx := testCtx(t)
	mat, base, root := registryMaterializerFixture(t)

	claudeRoot := filepath.Join(root, "TEST-unselected-claude")
	codexRoot := filepath.Join(root, "TEST-selected-codex")
	for _, dir := range []string{claudeRoot, codexRoot} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mat.Roots.ClaudeRunner = listingRunner{configRoot: claudeRoot}
	claudeReq := base
	claudeReq.Integration, claudeReq.ClientConfigRoot = portable.Claude, claudeRoot
	claudeReq.Identity.ScopeRoot = claudeRoot
	claudeReq.OperationID = "TEST-initial-claude"

	local := base
	local.Integration = portable.CopilotVSCode
	local.Identity.ScopeRoot = local.ClientConfigRoot
	settings := filepath.Join(local.ClientConfigRoot, "settings.json")
	if err := os.WriteFile(settings, []byte("{\"foreign\":true}"), 0600); err != nil {
		t.Fatal(err)
	}
	tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
	if runtime.GOOS == "darwin" {
		tuple = vscode.QualifiedDarwinTESTTuple()
	}
	cfg := &vscode.LocalConfig{
		ProfileSettingsPath: settings, QualifiedTuple: tuple,
		TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)},
		NativeStop:  true, MCPServers: []string{"agent-notifications"}, Skills: []string{"agent-notifications"},
	}
	setLocalHooks := func(binding portable.Binding) {
		t.Helper()
		cfg.HookSpecs = copilotvscodeinstall.LocalHookSpecs(binding, mat.Roots.HelperExecutable)
		rendered, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(rendered))
	}
	setLocalHooks(portable.Binding{ControlRoot: local.Identity.ControlRoot, BindingID: "TEST-placeholder"})
	newRegistry := func() {
		t.Helper()
		adapter, err := vscode.NewLocal(*cfg)
		if err != nil {
			t.Fatal(err)
		}
		mat.Registry, err = clients.NewRegistry(adapter, codex.New(), claude.New())
		if err != nil {
			t.Fatal(err)
		}
	}
	newRegistry()
	local.LocalConfig = cfg
	codexReq := base
	codexReq.Integration, codexReq.ClientConfigRoot = portable.Codex, codexRoot
	codexReq.Identity.ScopeRoot = codexRoot

	claudeBinding, err := mat.Install(ctx, claudeReq)
	if err != nil {
		t.Fatal("install initial unselected Claude:", err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(base.Identity.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	local.ExpectedGeneration, codexReq.ExpectedGeneration = snapshot.Ledger.Generation, snapshot.Ledger.Generation
	local.OperationID, codexReq.OperationID = "TEST-three-client-group-install", "TEST-three-client-group-install"
	selected, err := mat.ApplyGroup(ctx, []MaterializeRequest{codexReq, local})
	if err != nil || len(selected) != 2 {
		t.Fatalf("install selected Codex+Local: %+v %v", selected, err)
	}
	for _, binding := range selected {
		if binding.Integration == portable.CopilotVSCode {
			setLocalHooks(binding)
		}
	}
	newRegistry()

	before, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	initial, ok := findInstallation(before, base.Identity.InstallationID)
	if !ok || len(initial.Clients) != 3 {
		t.Fatalf("expected three live clients, got %+v", initial.Clients)
	}
	var claudeID string
	for id, binding := range initial.Clients {
		if binding.ClientID == "claude" {
			claudeID = id
		}
	}
	if claudeID == "" || claudeID != claudeBinding.BindingID {
		t.Fatalf("unselected Claude identity changed: %s %+v", claudeID, claudeBinding)
	}
	unchanged := initial.Clients[claudeID]
	claudeFiles := registryFixtureFiles(t, unchanged.TargetLocator)
	for _, selectedBinding := range selected {
		if _, ok := initial.Clients[selectedBinding.BindingID]; !ok {
			t.Fatalf("selected client missing: %+v", selectedBinding)
		}
	}

	next := filepath.Join(root, "TEST-codex-local-revision-two")
	copyPackage(t, base.PackageRoot, next)
	if err := os.WriteFile(filepath.Join(next, "plugin.json"), []byte("{\"$schema\":\"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json\",\"name\":\"agent-notify\",\"version\":\"1.0.1\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "skills", "agent-notifications", "SKILL.md"),
		[]byte("---\nname: agent-notifications\ndescription: Updated selected clients\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err = installruntime.ReadInstalledSnapshot(base.Identity.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []*MaterializeRequest{&local, &codexReq} {
		req.PackageRoot, req.ExpectedGeneration = next, snapshot.Ledger.Generation
		req.Operation = uapinstaller.OpUpdate
		req.OperationID = "TEST-three-client-group-update"
	}
	updated, err := mat.ApplyGroup(ctx, []MaterializeRequest{codexReq, local})
	if err != nil || len(updated) != 2 {
		t.Fatalf("update selected Codex+Local with live Claude: %+v %v", updated, err)
	}
	for _, binding := range updated {
		if binding.Integration == portable.Claude || binding.BindingID == claudeID {
			t.Fatalf("unselected Claude returned as update target: %+v", updated)
		}
	}
	after, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	current, ok := findInstallation(after, base.Identity.InstallationID)
	if !ok || len(current.Clients) != 3 {
		t.Fatalf("update changed three-client membership: %+v", current.Clients)
	}
	if !reflect.DeepEqual(unchanged, current.Clients[claudeID]) ||
		!reflect.DeepEqual(claudeFiles, registryFixtureFiles(t, unchanged.TargetLocator)) {
		t.Fatal("unselected Claude binding or installed native files changed")
	}
	for _, old := range selected {
		beforeBinding := initial.Clients[old.BindingID]
		afterBinding, ok := current.Clients[old.BindingID]
		if !ok || beforeBinding.PackageRevision == nil || afterBinding.PackageRevision == nil ||
			beforeBinding.PackageRevision.TreeDigest == afterBinding.PackageRevision.TreeDigest {
			t.Fatalf("selected client did not receive new revision: %+v", old)
		}
	}
	claudeName, err := claudeBinding.Filename()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := portable.Acquire(ctx, claudeBinding.DataRoot, claudeName)
	if err != nil {
		t.Fatal("unselected Claude locator lost:", err)
	}
	lease.Release()
}
