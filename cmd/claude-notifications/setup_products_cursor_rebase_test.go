//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func TestCursorFreshCommonParentRuntimeAndWizardChild(t *testing.T) {
	ctx := setupCommandContext(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stage, profile, cliDir := filepath.Join(root, "stage"), filepath.Join(root, ".cursor"), filepath.Join(root, "bin")
	for _, path := range []string{stage, profile, cliDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cli := filepath.Join(cliDir, "cursor-agent")
	if err := os.WriteFile(cli, []byte("TEST discovery-only vendor; never executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := selectorSourceCommit
	selectorSourceCommit = strings.Repeat("a", 40)
	t.Cleanup(func() { selectorSourceCommit = old })
	provenance, err := currentSelectorProvenance()
	if err != nil {
		t.Fatal(err)
	}
	provenance.Stage = []byte(stage)
	a, err := parseSetupProducts([]string{"confirm", "--products", "cursor", "--desktop", "--control-root", filepath.Join(root, "control")})
	if err != nil {
		t.Fatal(err)
	}
	e := productEnvironment{Home: root, PATH: cliDir, DefaultControlRoot: a.Scopes["control-root"], Values: map[string]string{}, Config: config.EnvSnapshot{GOOS: runtime.GOOS, Vars: map[string]string{config.OverrideEnv: filepath.Join(root, "global.json")}, Lstat: os.Lstat, ReadDir: os.ReadDir}}
	parent, _, err := buildConfirmedBootstrapIntent(ctx, a, e, provenance)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stage, "confirmed-parent.json")
	if err := writeBootstrapIntent(path, parent); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := "claude-notifications-linux-amd64"
	if err := os.WriteFile(filepath.Join(stage, entry), []byte("TEST inert runtime "+installruntime.WriterProtocolMarker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := installRuntime([]string{"--stage", stage, "--target", filepath.Join(string(parent.Scopes["runtime-root"]), "bin"), "--entry", entry, "--control-root", string(parent.Scopes["control-root"]), "--seed-enabled-if-absent"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	request, child, err := admitBootstrapWizardIntent(path, intentWizardRequest(parent))
	if err != nil || child == nil {
		t.Fatalf("fresh qualified-child admission failed: %v", err)
	}
	if err := fenceCursorIntent(ctx, request, *child); err != nil {
		t.Fatal("original decisions failed child fence", err)
	}
	if child.Initial.Generation <= parent.Initial.Generation || request.CursorAuthority != nil || !child.MCP.AllowPolicySeed {
		t.Fatalf("runtime observation or qualification boundary changed: %+v", request)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("runtime/child replaced the immutable parent", err)
	}
}

func TestCursorChildRebasePreservesOriginalRelevantConsent(t *testing.T) {
	f, _, _ := configureFixture(t)
	ctx := setupCommandContext(t)
	on, off := true, false
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, Hooks: &off, AgentNotify: &on, CursorAgentNotify: &off, ControlRoot: f.control, RuntimeRoot: f.runtime, GlobalConfig: f.global, CursorConfig: filepath.Join(f.root, ".cursor"), ScopeRoot: filepath.Join(f.root, ".cursor"), ClientExecutables: map[string]string{"cursor": f.command}}
	projection, generation, policy, err := setupwizard.ObserveBootstrapMCPWithPolicy(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	channels, err := selectedBootstrapChannelPolicy([]string{"qoder", "cursor"}, snapshot.Fields["route"])
	if err != nil {
		t.Fatal(err)
	}
	parent := confirmedBootstrapIntent{Request: setupProductsArgs{Products: []string{"qoder", "cursor"}}, Scopes: map[string][]byte{"control-root": []byte(f.control), "runtime-root": []byte(f.runtime), "scope-root": []byte(r.CursorConfig), "client-executable": []byte(f.command), "global-config": []byte(f.global)}, MCP: setupwizard.BootstrapMCPSelection{Selected: []string{"cursor"}, Projection: projection, AllowPolicySeed: true, SeedEnabled: true}, Initial: bootstrapInitialObservation{LedgerID: snapshot.Installation.Ledger.ID, Owner: snapshot.Installation.Ledger.Owner, Generation: generation, Policy: policy, ChannelPolicy: channels}}
	child, err := bootstrapWizardChild(parent, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &generation, ExpectedPolicy: &policy, PolicyEnabled: &on, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"qoderNotifications":{"desktop":true,"webhook":false}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := admitCursorBootstrapChild(ctx, parent, child, r)
	if err != nil || admitted.Initial.Generation <= generation || admitted.Initial.Policy == policy {
		t.Fatalf("authorized unrelated sibling policy was not rebased: %+v %v", admitted.Initial, err)
	}
	if parent.Initial.Generation != generation || parent.Initial.Policy != policy {
		t.Fatal("rebase rewrote immutable original")
	}
	current, err := installruntime.ReadPolicySnapshot(ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &current.Installation.Ledger.Generation, ExpectedPolicy: &current.Preimage, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"cursorNotifications":{"desktop":false,"webhook":false}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admitCursorBootstrapChild(ctx, parent, child, r); err == nil {
		t.Fatal("fresh CAS observation adopted an intervening Cursor channel decision")
	}
}

func TestCursorNewInstallationRequiresExactAuthorizedCompletedSibling(t *testing.T) {
	for _, integration := range []string{"codex", "claude"} {
		t.Run(integration, func(t *testing.T) {
			f, _, _ := configureFixture(t)
			ctx := setupCommandContext(t)
			ledger, _, err := installruntime.ReadOwnership(f.control)
			if err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(f.root, integration)
			installationID, target := "TEST-sibling-install", filepath.Join(f.root, "TEST-sibling-target")
			bindingID := domain.ComputeClientBindingID(installationID, integration, "user", target)
			dataRoot := filepath.Join(f.root, "TEST-sibling-data")
			binding := portable.Binding{Version: 1, Integration: portable.Integration(integration), InstallationID: installationID, BindingID: bindingID, ScopeID: "user", ComponentID: ledger.ID, Owner: ledger.Owner, ScopeRoot: profile, DataRoot: dataRoot, ControlRoot: f.control, GlobalConfig: f.global, RuntimeRoot: f.runtime, Primary: "bin/claude-notifications"}
			key, consumer, _, err := binding.Registration()
			if err != nil {
				t.Fatal(err)
			}
			ledger, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: key, Consumer: consumer, ExpectedGeneration: &ledger.Generation})
			if err != nil {
				t.Fatal(err)
			}
			profileKey := "codex-home"
			if integration == "claude" {
				profileKey = "claude-config"
			}
			parent := confirmedBootstrapIntent{Request: setupProductsArgs{Products: []string{integration, "cursor"}}, Scopes: map[string][]byte{"control-root": []byte(f.control), "global-config": []byte(f.global), profileKey: []byte(profile)}, MCP: setupwizard.BootstrapMCPSelection{Selected: []string{integration, "cursor"}}}
			store := statev2.Store{Path: filepath.Join(filepath.Dir(f.control), "uap", "state", "state-v2.json")}
			client := domain.ClientBinding{ClientBindingID: bindingID, ClientID: integration, Scope: "user", TargetLocator: target, NativeProfileRoot: profile, DataReceiptID: "TEST-data", Materialization: domain.MaterializationMaterialized, PhysicalArtifact: "TEST-artifact", Activation: domain.ActivationActive, Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed, Verification: domain.VerificationInstalled}
			if integration == "claude" {
				client.NativeProfileRoot = ""
			}
			state := domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion, Installations: []domain.Installation{{InstallationID: installationID, DeclaredName: "agent-notify", OriginMode: domain.OriginModeDirect, Package: domain.PackageBinding{DeclaredName: "agent-notify"}, Source: domain.SourceBinding{SourceBindingID: "TEST-source"}, Clients: map[string]domain.ClientBinding{bindingID: client}, DataReceipts: map[string]domain.DataReceipt{"TEST-data": {DataReceiptID: "TEST-data", Locator: dataRoot, PhysicalBackend: "TEST-artifact", Scope: "user", OwnershipDigest: "TEST-owned-digest", State: domain.DataReceiptOwned}}}}}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			snapshot := installruntime.PolicySnapshot{Installation: installruntime.InstalledSnapshot{Ledger: ledger}}
			if err := bootstrapSiblingInstallation(parent, installationID, snapshot); err != nil {
				t.Fatal("exact completed authorized sibling refused", err)
			}
			for _, mutate := range []func(){
				func() { parent.MCP.Selected = []string{"cursor"} },
				func() { parent.Scopes[profileKey] = []byte(filepath.Join(f.root, "another-profile")) },
				func() { delete(snapshot.Installation.Ledger.Consumers, key) },
			} {
				parent.MCP.Selected = []string{integration, "cursor"}
				parent.Scopes[profileKey] = []byte(profile)
				snapshot.Installation.Ledger.Consumers[key] = consumer
				mutate()
				if err := bootstrapSiblingInstallation(parent, installationID, snapshot); err == nil {
					t.Fatal("unproved newly appeared installation adopted")
				}
			}
		})
	}
}
