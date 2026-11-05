//go:build linux || darwin

package portablesetup

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

	// Local adds a frozen Stop hook to the canonical package. Install Claude
	// from that same composed source before the selected Codex+Local add, so
	// the three-client fixture does not request a new revision via OpInstall.
	_, sharedLocal, closeShared, err := mat.composeLocalPackage(ctx, local, false)
	if err != nil {
		t.Fatal("compose shared initial package:", err)
	}
	defer closeShared()
	claudeReq.PackageRoot = sharedLocal.PackageRoot

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
	// Missing or stale Claude facts must be refused before a selected host
	// handoff can publish a pending reservation or modify the UAP state.
	factsPath := clientFactsPath(filepath.Dir(mat.Roots.StateFile))
	originalFacts, err := os.ReadFile(factsPath)
	if err != nil {
		t.Fatal(err)
	}
	stateBeforeUpdate, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	localProfileBefore := registryFixtureFiles(t, local.ClientConfigRoot)
	codexProfileBefore := registryFixtureFiles(t, codexReq.ClientConfigRoot)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]clientFact)
	}{
		{"missing-claude-facts", func(facts map[string]clientFact) { delete(facts, "claude") }},
		{"stale-claude-binding", func(facts map[string]clientFact) {
			fact := facts["claude"]
			fact.BindingID = "TEST-stale-claude-binding"
			facts["claude"] = fact
		}},
		{"relative-claude-executable", func(facts map[string]clientFact) {
			fact := facts["claude"]
			fact.Executable = "relative/probe"
			facts["claude"] = fact
		}},
		{"missing-claude-executable", func(facts map[string]clientFact) {
			fact := facts["claude"]
			fact.Executable = ""
			facts["claude"] = fact
		}},
		{"wrong-existing-claude-config-root", func(facts map[string]clientFact) {
			fact := facts["claude"]
			fact.ConfigRoot = codexRoot
			facts["claude"] = fact
		}},
		{"unclean-claude-config-root", func(facts map[string]clientFact) {
			fact := facts["claude"]
			fact.ConfigRoot = claudeRoot + "/../" + filepath.Base(claudeRoot)
			facts["claude"] = fact
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, readErr := readClientFacts(filepath.Dir(mat.Roots.StateFile))
			if readErr != nil {
				t.Fatal(readErr)
			}
			tc.mutate(facts)
			body, marshalErr := json.Marshal(facts)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if writeErr := os.WriteFile(factsPath, body, 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			t.Cleanup(func() {
				if writeErr := os.WriteFile(factsPath, originalFacts, 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
			})
			if _, applyErr := mat.ApplyGroup(ctx, []MaterializeRequest{codexReq, local}); applyErr == nil ||
				!strings.Contains(applyErr.Error(), "unselected live client claude") {
				t.Fatalf("unverified Claude facts did not refuse pre-handoff: %v", applyErr)
			}
			currentSnapshot, snapErr := installruntime.ReadInstalledSnapshot(base.Identity.ControlRoot)
			if snapErr != nil || !reflect.DeepEqual(snapshot.Ledger, currentSnapshot.Ledger) {
				t.Fatalf("refusal changed host reservation/ledger: %v", snapErr)
			}
			currentState, readErr := os.ReadFile(mat.Roots.StateFile)
			if readErr != nil || !reflect.DeepEqual(stateBeforeUpdate, currentState) {
				t.Fatalf("refusal changed SDK state: %v", readErr)
			}
			if !reflect.DeepEqual(localProfileBefore, registryFixtureFiles(t, local.ClientConfigRoot)) ||
				!reflect.DeepEqual(codexProfileBefore, registryFixtureFiles(t, codexReq.ClientConfigRoot)) {
				t.Fatal("refused update changed selected native client profiles")
			}
		})
	}
	updated, err := mat.ApplyGroup(ctx, []MaterializeRequest{codexReq, local})
	// The frozen Local SDK route is Prepared, not native-activation-attested.
	// A committed group update must report that remaining host handoff honestly,
	// even when the two selected managed revisions were physically written.
	var incomplete ResultError
	if len(updated) != 0 || !errors.As(err, &incomplete) || incomplete.Finalization ||
		incomplete.Result.Outcome != uapinstaller.OutcomeIncomplete ||
		incomplete.Result.Reason != "committed binding is not activated" ||
		!incomplete.Result.Mutated || len(incomplete.Result.Targets) != 2 {
		t.Fatalf("selected group update did not preserve truthful committed result: %+v %+v %v", updated, incomplete.Result, err)
	}
	for _, target := range incomplete.Result.Targets {
		if target.ClientID != ClientID(portable.Codex) && target.ClientID != ClientID(portable.CopilotVSCode) ||
			target.BindingID == claudeID {
			t.Fatalf("unselected Claude included in group update result: %+v", incomplete.Result.Targets)
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
		// The Codex and frozen Local projectors both place selected raw Skills
		// under the managed target root, not the native profile settings path.
		skill, err := os.ReadFile(filepath.Join(afterBinding.TargetLocator, "skills", "agent-notifications", "SKILL.md"))
		if err != nil || !strings.Contains(string(skill), "description: Updated selected clients") {
			t.Fatalf("selected %s native Skill did not receive update: %s %v", old.Integration, skill, err)
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
