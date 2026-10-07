package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

// Regression: a completed registration used to look ready despite unobserved
// permission/delivery, and restart printed an empty shell command.
func TestSetupWizardHumanInstallationSummary(t *testing.T) {
	result := setupwizard.Result{Action: "install", Outcome: "completed", Generation: 12, InstallationID: "private-id",
		Targets:     []setupwizard.TargetResult{{Client: "codex", Unit: "agent-notify", Outcome: "completed", Profile: "profile-secret", TreeDigest: "digest-secret", Reason: "client-private-id"}},
		Readiness:   []setupwizard.ReadinessFact{{Client: "codex", Runtime: "installed", Hooks: "not_checked", MCP: "installed", Permission: "not_checked", Restart: "pending", Delivery: "not_verified"}},
		NextActions: []setupwizard.NextAction{{Kind: "restart-client", Agents: []string{"codex"}}, {Kind: "request-permission", Agents: []string{"codex"}, Command: []string{"setup-notifications", "request-permission", "--control-root", "/tmp/path with spaces"}}, {Kind: "test-notification", Agents: []string{"codex"}, Command: []string{"notify"}}},
	}
	var out bytes.Buffer
	if code := writeSetupWizardResult(&out, false, result, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Installation complete", "Codex", "Permission: not checked", "Restart: required", "Delivery: not verified", "Restart Codex (exit and reopen)", "grant it if needed", "Ask Codex to send a test notification using the notify tool."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out.String())
		}
	}
	for _, forbidden := range []string{"generation=", "private-id", "profile-secret", "digest-secret", "client-private-id", "Ready to use", "next restart-client:", "'notify'", "/hooks"} {
		if strings.Contains(out.String(), forbidden) {
			t.Errorf("misleading/technical %q: %s", forbidden, out.String())
		}
	}
	// JSON still preserves the operator contract including exact argv and identities.
	out.Reset()
	if code := writeSetupWizardResult(&out, true, result, nil); code != 0 {
		t.Fatalf("json exit %d", code)
	}
	var got setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Generation != 12 || got.InstallationID != "private-id" || got.Targets[0].TreeDigest != "digest-secret" || got.NextActions[1].Command[3] != "/tmp/path with spaces" {
		t.Fatalf("lost diagnostics: %+v", got)
	}
}

// Regression: known permission must not trigger repeated grant instructions;
// hook trust applies to hooks installation, never an MCP-only registration.
func TestSetupWizardHumanPermissionAndHookTrust(t *testing.T) {
	for _, permission := range []string{"allowed", "granted", "not_checked", "denied"} {
		t.Run(permission, func(t *testing.T) {
			result := setupwizard.Result{Action: "install", Outcome: "completed", Targets: []setupwizard.TargetResult{{Client: "codex", Unit: "hooks", Outcome: "completed"}}, Readiness: []setupwizard.ReadinessFact{{Client: "codex", Permission: permission}}, NextActions: []setupwizard.NextAction{{Kind: "request-permission", Command: []string{"grant-command"}}}}
			var out bytes.Buffer
			writeSetupWizardResult(&out, false, result, nil)
			known := permission == "allowed" || permission == "granted"
			if strings.Contains(out.String(), "grant-command") == known {
				t.Fatalf("grant instruction: %s", out.String())
			}
			if !strings.Contains(out.String(), "run /hooks") {
				t.Fatalf("missing trust: %s", out.String())
			}
		})
	}
}

// Regression: partial/conflicting installation or removal must not claim ready.
func TestSetupWizardHumanIncompleteAndRemoval(t *testing.T) {
	for _, outcome := range []string{"failed", "incomplete", "conflict", "invalid", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			var out bytes.Buffer
			r := setupwizard.Result{Action: "install", Outcome: outcome, Reason: "fixture_reason", Command: []string{"setup-notifications", "wizard", "--action", "repair"}}
			code := writeSetupWizardResult(&out, false, r, nil)
			if code != r.ExitCode() {
				t.Fatalf("exit %d", code)
			}
			if strings.Contains(out.String(), "Installation complete") || !strings.Contains(out.String(), "fixture_reason") || !strings.Contains(out.String(), "retry:") {
				t.Fatalf("lost failure: %s", out.String())
			}
		})
	}
	var out bytes.Buffer
	r := setupwizard.Result{Action: "uninstall", Outcome: "completed", DataRetained: true, Targets: []setupwizard.TargetResult{{Client: "codex", Unit: "hooks", Outcome: "completed"}}}
	writeSetupWizardResult(&out, false, r, nil)
	if !strings.Contains(out.String(), "Removal complete") || !strings.Contains(out.String(), "hooks: removed") || strings.Contains(out.String(), "/hooks") || strings.Contains(out.String(), "hooks: installed") {
		t.Fatal(out.String())
	}
}

// Maintenance failure stays visible in both CLI contracts without failing a
// committed installation or contaminating JSON with human summary lines.
func TestSetupWizardMaintenanceWarningOutput(t *testing.T) {
	warning := "runtime committed; native registration reconciliation incomplete: registration unavailable"
	var result setupwizard.Result
	if err := json.Unmarshal([]byte(`{"action":"install","outcome":"completed","targets":[{"client":"codex","unit":"hooks","outcome":"completed","warnings":["runtime committed; native registration reconciliation incomplete: registration unavailable"]}]}`), &result); err != nil {
		t.Fatal(err)
	}
	for _, jsonOut := range []bool{false, true} {
		var out bytes.Buffer
		if code := writeSetupWizardResult(&out, jsonOut, result, nil); code != 0 {
			t.Fatalf("maintenance warning failed setup: %d", code)
		}
		if jsonOut {
			var got struct {
				Targets []struct {
					Warnings []string `json:"warnings"`
				} `json:"targets"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Targets) != 1 || len(got.Targets[0].Warnings) != 1 || got.Targets[0].Warnings[0] != warning {
				t.Fatalf("JSON lost warning: %s", out.String())
			}
		} else if !strings.Contains(out.String(), "Warning: "+warning) || !strings.Contains(out.String(), "Installation complete") {
			t.Fatalf("human output hid committed-setup warning: %s", out.String())
		}
	}
}
