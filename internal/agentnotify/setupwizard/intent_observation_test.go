//go:build linux || darwin

package setupwizard

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
)

func TestInspectUntrustedPendingIntentIsIncomplete(t *testing.T) {
	for _, mode := range []string{"unreadable", "mismatched", "tampered", "valid"} {
		t.Run(mode, func(t *testing.T) {
			ctx := testCtx(t)
			control, runtime, global, primary, generation := managedRuntime(t)
			intent := portablesetup.Intent{Version: 1, SetupIntentID: "pending-observation", Action: "install", Stage: "confirmed",
				ExpectedGeneration: generation, Targets: []portablesetup.IntentTarget{{Client: "claude", Units: []string{"agent-notify"}}}}
			if mode == "mismatched" {
				intent.SetupIntentID = "different-intent"
			}
			body, err := json.Marshal(intent)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unreadable" {
				body = []byte("{")
			}
			// The kernel owns the reservation and file identity, while the host
			// validates the opaque intent payload during read-only inspection.
			path := portablesetup.IntentPath(control)
			reservation := installruntime.PendingMutation{ID: "pending-observation", Owner: "existing-installer", IntentRef: path}
			if _, err := installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: control, Owner: "existing-installer", RuntimeRoot: runtime, ConsumerID: "existing",
				RefreshOnly: true, ExpectedGeneration: &generation, Reservation: &reservation,
				Files: []installruntime.File{{Path: path, Data: body, Mode: 0600}},
			}); err != nil {
				t.Fatal(err)
			}
			var observed installruntime.InstalledSnapshot
			if mode == "tampered" {
				// Freeze a valid read-only snapshot before a different process
				// changes the file; a new Run would reject it even earlier.
				observed, err = installruntime.ReadInstalledSnapshot(control)
				if err != nil {
					t.Fatal(err)
				}
				// A parseable replacement with the same pending ID must not supply
				// profile paths for the suggested resume command.
				intent.Targets[0].Profile = filepath.Join(filepath.Dir(control), "TEST-forged-profile")
				tampered, marshalErr := json.Marshal(intent)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if writeErr := os.WriteFile(path, tampered, 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
			before := policyConflictFiles(t, filepath.Dir(control))
			var got Result
			if mode == "tampered" {
				got = reportPendingWizardIntent(Request{ControlRoot: control}, observed, Result{Action: ActionInspect, Outcome: "completed"})
			} else {
				got, err = Run(ctx, Request{Action: ActionInspect, Agents: []string{"claude"}, ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary})
				if err != nil {
					t.Fatal(err)
				}
			}
			wantOutcome, wantReason, wantAction := "incomplete", "pending_intent_unreadable", "recover"
			if mode == "valid" {
				wantOutcome, wantReason, wantAction = "completed", "", "resume"
			}
			if got.Outcome != wantOutcome || got.Reason != wantReason || len(got.NextActions) != 1 || got.NextActions[0].Kind != wantAction {
				t.Errorf("pending inspection reported the wrong completion/recovery facts: %+v", got)
			}
			if mode != "valid" && (len(got.NextActions) != 1 || got.NextActions[0].Reason != "pending_intent_unreadable") {
				t.Errorf("lost unreadable intent recovery reason: %+v", got.NextActions)
			}
			if after := policyConflictFiles(t, filepath.Dir(control)); !reflect.DeepEqual(before, after) {
				t.Fatal("inspection changed the pending intent or installed state")
			}
		})
	}
}

func TestPendingIntentPresentationPreservesCallerBindings(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, global, primary, generation := managedRuntime(t)
	profile := t.TempDir()
	plantPendingIntent(t, ctx, control, runtime, generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-binding-ownership", Action: "install", Stage: "confirmed", ExpectedGeneration: generation,
		Targets: []portablesetup.IntentTarget{
			{Client: "codex", BindingID: "pending-codex", DataReceiptID: "pending-codex-data", Profile: profile, Units: []string{"agent-notify"}},
			{Client: "claude", BindingID: "pending-claude", DataReceiptID: "pending-claude-data", Units: []string{"agent-notify"}},
		},
	})
	for _, tc := range []struct {
		name   string
		action Action
		plan   bool
	}{{"inspect", ActionInspect, false}, {"plan-action-conflict", ActionRepair, true}, {"run-action-conflict", ActionRepair, false}, {"plan-restoration-conflict", ActionInstall, true}, {"run-restoration-conflict", ActionInstall, false}} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Action: tc.action, Agents: []string{"codex", "claude"}, Yes: true,
				ControlRoot: control, RuntimeRoot: runtime, GlobalConfig: global, Helper: primary,
				BindingIDs:     map[string]string{"claude": "caller-claude", "pi": "caller-pi"},
				DataReceiptIDs: map[string]string{"claude": "caller-claude-data", "pi": "caller-pi-data"}}
			bindings, receipts := maps.Clone(req.BindingIDs), maps.Clone(req.DataReceiptIDs)
			before := policyConflictFiles(t, filepath.Dir(control))
			var got Result
			var err error
			if tc.plan {
				var plan SetupPlan
				plan, err = Plan(ctx, req)
				got = plan.Result
			} else {
				got, err = Run(ctx, req)
			}
			command := got.Command
			if tc.action == ActionInspect {
				if err != nil || len(got.NextActions) != 1 || got.NextActions[0].Kind != "resume" {
					t.Fatalf("pending inspection did not render retry: %+v %v", got, err)
				}
				command = got.NextActions[0].Command
			} else if !errors.Is(err, ErrRefused) || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
				t.Fatalf("pending conflict did not render retry: %+v %v", got, err)
			}
			if !slices.Contains(command, profile) {
				t.Errorf("retry did not restore the frozen profile: %v", command)
			}
			if !maps.Equal(req.BindingIDs, bindings) || !maps.Equal(req.DataReceiptIDs, receipts) {
				t.Errorf("request presentation changed caller identities: bindings=%v receipts=%v", req.BindingIDs, req.DataReceiptIDs)
			}
			if after := policyConflictFiles(t, filepath.Dir(control)); !reflect.DeepEqual(before, after) {
				t.Fatal("request presentation changed installed files")
			}
		})
	}
}

// A conflicting CLI MCP config must not be copied into a suggested retry:
// the pending intent already froze the only path that can be resumed safely.
func TestPendingIntentMCPConflictRetryUsesFrozenPath(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, primary, generation := managedRuntime(t)
	profile := filepath.Join(filepath.Dir(control), "TEST-codex-profile")
	frozen := filepath.Join(profile, "config.toml")
	conflicting := filepath.Join(filepath.Dir(control), "TEST-unrelated", "config.toml")
	intent := portablesetup.Intent{Version: 1, SetupIntentID: "TEST-frozen-mcp-conflict", Action: "install",
		Stage: "confirmed", ExpectedGeneration: generation, Targets: []portablesetup.IntentTarget{{
			Client: "codex", Profile: profile, MCPConfig: frozen, Units: []string{"agent-notify"},
		}}}
	plantPendingIntent(t, ctx, control, runtimeRoot, generation, intent)
	req := Request{Action: ActionInstall, Agents: []string{"codex"}, Yes: true,
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, Helper: primary,
		CodexHome: profile, MCPConfig: map[string]string{"codex": conflicting, "claude": "TEST-caller-claude"}}
	before := policyConflictFiles(t, filepath.Dir(control))
	conflict, handled := pendingIntentConflict(req, portablesetup.ErrIntentConflict, Result{Action: "install"})
	if !handled || conflict.Outcome != "conflict" || conflict.Reason != "pending_intent_conflict" {
		t.Fatalf("pending MCP conflict lost: %+v", conflict)
	}
	index := slices.Index(conflict.Command, "--mcp-config")
	if index < 0 || index+1 >= len(conflict.Command) || conflict.Command[index+1] != frozen ||
		slices.Contains(conflict.Command, conflicting) {
		t.Fatalf("retry did not use frozen MCP path: %v", conflict.Command)
	}
	retry := retryRequestFromIntent(req, intent)
	if retry.MCPConfig["codex"] != frozen || retry.MCPConfig["claude"] != req.MCPConfig["claude"] {
		t.Fatalf("retry changed unrelated MCP choice: %+v", retry.MCPConfig)
	}
	if req.MCPConfig["codex"] != conflicting {
		t.Fatal("retry mutated caller-owned MCP choices")
	}
	if restored, _, err := restoreOmittedFromIntent(retry, []portable.Integration{portable.Codex}, intent); err != nil || restored.MCPConfig["codex"] != frozen {
		t.Fatalf("offered retry still conflicts with frozen intent: %+v %v", restored.MCPConfig, err)
	}
	if after := policyConflictFiles(t, filepath.Dir(control)); !reflect.DeepEqual(before, after) {
		t.Fatal("retry presentation changed owned or pending files")
	}
}

func TestRestoreLocalIntentScopeOwnsRestoredBindingMaps(t *testing.T) {
	root := t.TempDir()
	old := portable.Binding{Version: 1, Integration: portable.CopilotVSCode, InstallationID: "pending-installation", BindingID: "pending-local", ScopeID: "user", ComponentID: "component", Owner: "existing-installer",
		ScopeRoot: filepath.Join(root, "profile"), DataRoot: filepath.Join(root, "data"), ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global.json"), RuntimeRoot: filepath.Join(root, "runtime"), Primary: "primary"}
	next := old
	next.Primary = "next-primary"
	oldKey, _, _, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	newKey, _, _, err := next.Registration()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{ActionInstall, ActionUninstall} {
		t.Run(string(action), func(t *testing.T) {
			req := Request{Action: action, Agents: []string{"copilot-vscode"},
				BindingIDs: map[string]string{"claude": "caller-claude"}, DataReceiptIDs: map[string]string{"claude": "caller-data"},
				MigrationBindings: map[string]MigrationBinding{"claude": {OldConsumerKey: "caller-migration"}},
				RemovalBindings:   map[string]RemovalBinding{"claude": {OldConsumerKey: "caller-removal"}}, MCPConfig: map[string]string{"claude": filepath.Join(root, "caller-mcp")},
				LocalConfig: &vscode.LocalConfig{ProfileSettingsPath: filepath.Join(old.ScopeRoot, "settings.json"), QualifiedTuple: vscode.QualifiedDarwinTESTTuple(), MCPServers: []string{"caller-mcp"}, Skills: []string{"caller-skill"}}}
			before := req
			before.BindingIDs, before.DataReceiptIDs = maps.Clone(req.BindingIDs), maps.Clone(req.DataReceiptIDs)
			before.MigrationBindings, before.RemovalBindings, before.MCPConfig = maps.Clone(req.MigrationBindings), maps.Clone(req.RemovalBindings), maps.Clone(req.MCPConfig)
			before.LocalConfig = copyLocalConfig(req.LocalConfig)
			target := portablesetup.IntentTarget{Client: "copilot-vscode", InstallationID: old.InstallationID, BindingID: old.BindingID, DataReceiptID: "pending-local-data", Profile: old.ScopeRoot, MCPConfig: filepath.Join(root, "pending-mcp"), OldBinding: &old, OldConsumerKey: oldKey}
			if action == ActionInstall {
				target.NewBinding, target.NewConsumerKey = &next, newKey
			}
			got, err := RestoreLocalIntentScope(req, portablesetup.Intent{Action: string(action), Targets: []portablesetup.IntentTarget{target}})
			if err != nil || got.BindingIDs["copilot-vscode"] != old.BindingID || got.DataReceiptIDs["copilot-vscode"] != target.DataReceiptID || got.MCPConfig["copilot-vscode"] != target.MCPConfig || string(got.LocalConfig.TargetShell.Shell) != req.LocalConfig.QualifiedTuple.TargetShell {
				t.Fatalf("Local scope did not restore pending identity/configuration: %+v %v", got, err)
			}
			if action == ActionInstall && got.MigrationBindings["copilot-vscode"].NewConsumerKey != newKey || action == ActionUninstall && got.RemovalBindings["copilot-vscode"].OldConsumerKey != oldKey {
				t.Fatalf("Local scope lost the exact frozen operation: %+v", got)
			}
			got.LocalConfig.MCPServers[0], got.LocalConfig.Skills[0] = "changed", "changed"
			if !reflect.DeepEqual(req, before) {
				t.Fatalf("Local scope restoration changed caller-owned request: got %+v want %+v", req, before)
			}
		})
	}
}
