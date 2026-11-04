//go:build linux || darwin

package setupwizard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestGroupHandoffFailureReportsLaterClient(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Action, req.ExternalUninstalled = ActionUninstall, true
	if got, err := Run(testCtx(t), req); err != nil || !got.DataRetained {
		t.Fatalf("retain empty group fixture: %+v %v", got, err)
	}
	req.Action, req.ExternalUninstalled = ActionInstall, false
	req.ReleaseVersion = "v1.0.0"
	req.Agents, req.CodexHome = []string{"claude", "codex"}, t.TempDir()
	config := filepath.Join(req.CodexHome, "config.toml")
	if err := os.WriteFile(config, nil, 0600); err != nil {
		t.Fatal(err)
	}
	req.MCPConfig = map[string]string{"codex": config}
	injected := false
	req.Progress = func(phase string) {
		if phase == "agent-notify" {
			// Admission has completed. A later client's discovery read fails
			// after the first selected client's handoff succeeds.
			if err := os.WriteFile(config, []byte("[invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			injected = true
		}
	}
	got, err := Run(testCtx(t), req)
	if !injected || !errors.Is(err, portablesetup.ErrPreflight) || got.Outcome != "incomplete" {
		t.Fatalf("later discovery failure not reached: %+v %v", got, err)
	}
	if len(got.Targets) != 1 || got.Targets[0].Client != "codex" || got.Targets[0].Outcome != "incomplete" {
		t.Errorf("later handoff error attributed to the wrong client: %+v", got.Targets)
	}
	intent, err := portablesetup.ReadIntent(req.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	ledger, recovery, err := installruntime.ReadOwnership(req.ControlRoot)
	if err != nil || recovery || ledger.PendingMutation == nil || ledger.PendingMutation.ID != intent.SetupIntentID {
		t.Fatalf("confirmed group intent was lost: %+v %v", ledger.PendingMutation, err)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "resume" || len(got.NextActions[0].Command) == 0 {
		t.Errorf("confirmed pending intent has no verified resume action: %+v", got.NextActions)
	}
}
