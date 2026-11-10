package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

// The public CLI output must expose both a previously committed client and
// the later failure, without emitting an exit-zero success or losing recovery.
func TestSetupWizardPartialCommittedClientJSONExit(t *testing.T) {
	result := setupwizard.Result{
		Action: "install", Outcome: "incomplete", Reason: "client_config_invalid",
		Targets: []setupwizard.TargetResult{
			{Client: "claude", Unit: "agent-notify", Outcome: "completed", Reason: "verified-claude-binding", TreeDigest: "verified-digest"},
			{Client: "codex", Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_invalid", FailureStage: "preflight"},
			{Client: "pi", Unit: "agent-notify", Outcome: "absent", Reason: "preserved_existing_opt_out"},
		},
		NextActions: []setupwizard.NextAction{{Kind: "recover", Agents: []string{"codex"}, Reason: "pending_install"}},
	}
	var jsonOut bytes.Buffer
	if code := writeSetupWizardResult(&jsonOut, true, result, errors.New("late second-phase failure")); code != 1 {
		t.Fatalf("partial commit returned exit %d: %s", code, jsonOut.String())
	}
	var parsed setupwizard.Result
	if err := json.Unmarshal(jsonOut.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Outcome != "incomplete" || parsed.Reason != "client_config_invalid" ||
		len(parsed.Targets) != 3 || parsed.Targets[0].Client != "claude" ||
		parsed.Targets[0].Outcome != "completed" || parsed.Targets[0].Reason != "verified-claude-binding" ||
		parsed.Targets[1].FailureStage != "preflight" || parsed.Targets[1].Reason != "client_config_invalid" ||
		parsed.Targets[2].Reason != "preserved_existing_opt_out" ||
		len(parsed.NextActions) != 1 || parsed.NextActions[0].Reason != "pending_install" {
		t.Fatalf("public JSON lost committed/failure/recovery facts: %+v", parsed)
	}
	var human bytes.Buffer
	if code := writeSetupWizardResult(&human, false, result, errors.New("late second-phase failure")); code != 1 {
		t.Fatalf("human partial commit exit %d", code)
	}
	if strings.Contains(human.String(), "Installation complete") || !strings.Contains(human.String(), "Setup incomplete:") {
		t.Fatalf("human output claimed completed installation: %s", human.String())
	}
}
