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
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

// Public host/SDK three-client lifecycle for #595: update only Codex+Local,
// retaining the exact live Claude binding and installed native profile bytes.
func TestGroupUpdateCodexLocalCompletesAndPreservesUnselectedClaude(t *testing.T) {
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
	if err != nil {
		// Bounded failure-only evidence identifies which saved SDK proof is
		// absent without weakening native activation or the acceptance gate.
		var resultErr ResultError
		if errors.As(err, &resultErr) {
			t.Logf("SDK group outcome=%s reason=%q mutated=%t", resultErr.Result.Outcome, resultErr.Result.Reason, resultErr.Result.Mutated)
			for _, target := range resultErr.Result.Targets {
				t.Logf("SDK selected client=%s activation=%s verification=%s policy=%s phase=%s", target.ClientID, target.Activation, target.Verification, target.Policy, target.GroupPhase)
			}
		}
		if state, loadErr := mat.Store.Load(); loadErr == nil {
			if installation, ok := findInstallation(state, base.Identity.InstallationID); ok {
				for _, binding := range installation.Clients {
					receipt, hasReceipt := installation.DataReceipts[binding.DataReceiptID]
					_, hasLocalDelivery := binding.SelectedDelivery.LocalFacts()
					t.Logf("SDK saved client=%s activation=%s verification=%s receiptOwned=%t receiptDigest=%t localDelivery=%t localObservation=%t nativeOwners=%d nativeRoot=%t nativeAttempt=%t nativePending=%t",
						binding.ClientID, binding.Activation, binding.Verification,
						hasReceipt && receipt.State == domain.DataReceiptOwned, receipt.OwnershipDigest != "",
						hasLocalDelivery, binding.LocalEntryObservation != nil, len(binding.NativeObjects),
						binding.NativeProfileRoot != "", binding.NativeActivationAttempt != "", binding.PendingNativeIntent != nil)
					if binding.ClientID == "vscode" {
						observationRevisionMatches := false
						if binding.LocalEntryObservation != nil {
							observationRevisionMatches = reflect.DeepEqual(binding.LocalEntryObservation.Facts().RevisionBasis, binding.SelectedDelivery)
						}
						profileRootMatches := false
						if facts, selected := binding.SelectedDelivery.LocalFacts(); selected {
							profileRootMatches = facts.ProfileRoot == binding.NativeProfileRoot
						}
						t.Logf("SDK saved Local ownership proof: validObservation=%t matchingRevision=%t matchingProfileRoot=%t ownsNativeEntry=%t",
							binding.ValidateLocalEntryObservation() == nil, observationRevisionMatches,
							profileRootMatches, binding.SelectedDelivery.OwnsProfileEntry(binding.NativeObjects))
					}
					t.Logf("SDK saved lifecycle client=%s materialized=%t authentication=%s policy=%s nativeRootMatchesConfig=%t",
						binding.ClientID, binding.Materialization == domain.MaterializationMaterialized,
						binding.Authentication, binding.Policy, binding.NativeProfileRoot == map[string]string{
							"codex": codexReq.ClientConfigRoot, "vscode": local.ClientConfigRoot,
						}[binding.ClientID])
				}
			}
		}
	}
	if err != nil || len(updated) != 2 {
		t.Fatalf("selected group update did not complete: bindings=%+v err=%v", updated, err)
	}
	for i, binding := range updated {
		if binding.BindingID != selected[i].BindingID || binding.Integration != selected[i].Integration ||
			binding.BindingID == claudeID || binding.InstallationID != base.Identity.InstallationID {
			t.Fatalf("completed group returned wrong selected binding: %+v", updated)
		}
	}
	// Completing the managed file operation must finish the host reservation.
	// Local stays Prepared: this does not attest live VS Code activation.
	hostAfter, err := installruntime.ReadInstalledSnapshot(base.Identity.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if hostAfter.Ledger.PendingMutation != nil {
		t.Fatalf("completed group retained host reservation: %+v", hostAfter.Ledger.PendingMutation)
	}
	for _, binding := range append(append([]portable.Binding(nil), updated...), claudeBinding) {
		if !portable.ExactCommittedBinding(hostAfter.Ledger, binding) {
			t.Fatalf("completed group lost exact host consumer: %+v", binding)
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
	postFacts, err := readClientFacts(filepath.Dir(mat.Roots.StateFile))
	if err != nil {
		t.Fatal(err)
	}
	var originalClientFacts map[string]clientFact
	if err := json.Unmarshal(originalFacts, &originalClientFacts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(postFacts["claude"], originalClientFacts["claude"]) {
		t.Fatal("completed group changed unselected Claude host facts")
	}
	for _, old := range selected {
		fact := postFacts[ClientID(old.Integration)]
		if fact.BindingID != current.Clients[old.BindingID].ClientBindingID ||
			fact.ConfigRoot == "" || fact.ConfigRoot != old.ScopeRoot {
			t.Fatalf("completed group update left selected facts stale: %s %+v", old.Integration, fact)
		}
	}
	for _, old := range selected {
		beforeBinding := initial.Clients[old.BindingID]
		afterBinding, ok := current.Clients[old.BindingID]
		if old.Integration == portable.CopilotVSCode && (afterBinding.Activation != domain.ActivationPrepared ||
			afterBinding.Verification != domain.VerificationPackageValid) {
			t.Fatalf("Local was falsely activated instead of prepared: %+v", afterBinding)
		}
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
