//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func TestInspectHonorsExplicitInstallation(t *testing.T) {
	control, runtimeRoot, global, primary, generation := managedRuntime(t)
	root := filepath.Dir(control)
	first, second, retained := "00000000-0000-4000-8000-000000000301", "00000000-0000-4000-8000-000000000302", "00000000-0000-4000-8000-000000000303"
	state := domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion}
	bindings := map[string]string{}
	for _, id := range []string{first, second, retained} {
		installation := domain.Installation{
			InstallationID: id, DeclaredName: "agent-notify",
			Source:  domain.SourceBinding{SourceBindingID: "src_" + id, RequestedSource: "fixture", CanonicalSource: "https://example.invalid/fixture", ResolvedRevision: "fixture", TreeDigest: "sha256:" + id},
			Package: domain.PackageBinding{LoaderKind: domain.LoaderKindAgentPlugins, FormatID: domain.FormatIDAgentPluginsV1, SchemaURI: domain.PluginSchemaV1, DeclaredName: "agent-notify", ManifestDigest: "sha256:fixture"},
			Clients: map[string]domain.ClientBinding{},
		}
		if id == retained {
			installation.DataRetained = true
			installation.DataReceipts = map[string]domain.DataReceipt{"retained": {DataReceiptID: "retained", PhysicalBackend: "local", Scope: "user", Locator: filepath.Join(root, "retained-data"), OwnershipDigest: "sha256:fixture", State: domain.DataReceiptOwned}}
		} else {
			target := filepath.Join(root, id, "plugin")
			bindingID := domain.ComputeClientBindingID(id, "codex", "user", target)
			bindings[id] = bindingID
			installation.Clients[bindingID] = domain.ClientBinding{
				ClientBindingID: bindingID, ClientID: "codex", Scope: "user", TargetLocator: target,
				PhysicalArtifact: domain.ComputePhysicalArtifactID("agent-notify", id),
				Materialization:  domain.MaterializationMaterialized, Activation: domain.ActivationActive,
				Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed, Verification: domain.VerificationPackageValid,
			}
		}
		state.Installations = append(state.Installations, installation)
	}
	statePath := filepath.Join(root, "uap", "state", "state-v2.json")
	if err := (statev2.Store{Path: statePath}).Save(state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{first, second, retained, "00000000-0000-4000-8000-000000000399", ""} {
		t.Run(selected, func(t *testing.T) {
			got, err := Run(testCtx(t), Request{Action: ActionInspect, Agents: []string{"codex"}, InstallationID: selected,
				ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, Helper: primary})
			if err != nil || got.Outcome != "completed" {
				t.Fatalf("inspect: %+v %v", got, err)
			}
			var targets []TargetResult
			for _, target := range got.Targets {
				if target.Unit == "agent-notify" {
					targets = append(targets, target)
				}
			}
			if selected == "" {
				if len(targets) != 2 || !got.DataRetained || got.InstallationID != "" {
					t.Fatalf("unscoped inspection changed: %+v", got)
				}
				return
			}
			if len(targets) != 1 || got.InstallationID != selected || got.DataRetained != (selected == retained) {
				t.Fatalf("unrelated installation affected selection: %+v", got)
			}
			if bindingID := bindings[selected]; bindingID != "" {
				if targets[0].Outcome != "installed" || targets[0].Reason != bindingID {
					t.Fatalf("wrong selected binding: %+v", targets)
				}
			} else if targets[0].Outcome != "absent" {
				t.Fatalf("unrelated binding satisfied absent selection: %+v", targets)
			}
		})
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("inspection changed UAP state")
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snap.Ledger.Generation != generation || snap.Ledger.PendingMutation != nil {
		t.Fatalf("inspection changed managed state: %+v %v", snap.Ledger, err)
	}
}

func TestPlanRefusesUnreadableRecoveryState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action Action
		notify bool
	}{{"hooks-install", ActionInstall, false}, {"notify-install", ActionInstall, true}, {"uninstall", ActionUninstall, false}} {
		t.Run(tc.name, func(t *testing.T) {
			control, runtimeRoot, global, primary, generation := managedRuntime(t)
			fault := filepath.Join(filepath.Dir(control), "uap", "state", "operations", "TEST-unreadable.json")
			if err := os.MkdirAll(filepath.Dir(fault), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fault, []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
			fetched := 0
			req := Request{Action: tc.action, Agents: []string{"codex"}, Hooks: boolPtr(!tc.notify), AgentNotify: boolPtr(tc.notify),
				ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, Helper: primary,
				CodexHome: filepath.Join(filepath.Dir(control), "TEST-codex"), ClientExecutable: primary, ReleaseVersion: "1.43.0",
				PackageFetcher: func(context.Context, string) ([]byte, error) {
					fetched++
					return nil, errors.New("unexpected acquisition")
				},
			}
			plan, err := Plan(testCtx(t), req)
			if err == nil || plan.Ready || plan.Result.Outcome != "incomplete" || plan.Result.Reason != "recovery_required" || fetched != 0 {
				t.Fatalf("unreadable recovery state admitted: %+v err=%v fetched=%d", plan, err, fetched)
			}
			if len(plan.Result.NextActions) != 1 || plan.Result.NextActions[0].Kind != "recover" {
				t.Fatalf("missing recovery guidance: %+v", plan.Result)
			}
			if body, err := os.ReadFile(fault); err != nil || string(body) != "{" {
				t.Fatalf("Plan modified unreadable journal: %q %v", body, err)
			}
			snap, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil || snap.Ledger.Generation != generation || snap.Ledger.PendingMutation != nil {
				t.Fatalf("Plan changed managed state: %+v %v", snap.Ledger, err)
			}
		})
	}
}
