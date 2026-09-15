package setupwizard

import (
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
)

func TestUpdateRequiredLiveMixedIsUpdateOnly(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true}
	got, err := updateRequired(req, portable.Codex, nil, []string{"codex"}, nil, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || got.Reason != "update_required" {
		t.Fatalf("live mixed: %+v %v", got, err)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "codex" || got.NextActions[0].Reason != "update_existing" {
		t.Fatalf("live mixed next: %+v", got.NextActions)
	}
	if cmd := strings.Join(got.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents codex") || strings.Contains(cmd, "2-add") {
		t.Fatalf("live mixed argv: %v", got.NextActions[0].Command)
	}
}

func TestUpdateRequiredHybridKeepsAddOfUnbound(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true}
	got, err := updateRequired(req, portable.Claude, nil, []string{"claude"}, []string{"codex"}, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || got.Reason != "update_required" {
		t.Fatalf("hybrid: %+v %v", got, err)
	}
	if len(got.NextActions) != 2 || got.NextActions[0].Kind != "update" || got.NextActions[1].Kind != "install" {
		t.Fatalf("hybrid next: %+v", got.NextActions)
	}
	if strings.Join(got.NextActions[0].Agents, ",") != "claude" || got.NextActions[0].Reason != "update_existing_before_add" {
		t.Fatalf("hybrid update: %+v", got.NextActions[0])
	}
	if strings.Join(got.NextActions[1].Agents, ",") != "codex" || got.NextActions[1].Reason != "add_after_update" {
		t.Fatalf("hybrid add: %+v", got.NextActions[1])
	}
	if cmd := strings.Join(got.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents claude") {
		t.Fatalf("hybrid update argv: %v", got.NextActions[0].Command)
	}
	if cmd := strings.Join(got.NextActions[1].Command, " "); !strings.Contains(cmd, "--action install") || !strings.Contains(cmd, "--agents codex") {
		t.Fatalf("hybrid add argv: %v", got.NextActions[1].Command)
	}
	text := annotateRequiredUpdate("plan", got)
	if !strings.Contains(text, "required-update=claude") || !strings.Contains(text, "2-add:codex") {
		t.Fatalf("hybrid plan text: %s", text)
	}
}

func TestUpdateRequiredAbsentSecondClientIsTwoPhase(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"codex"}, Yes: true}
	got, err := updateRequired(req, portable.Codex, []string{"claude"}, nil, nil, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || len(got.NextActions) != 2 {
		t.Fatalf("two-phase: %+v %v", got, err)
	}
	if got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "claude" {
		t.Fatalf("two-phase update: %+v", got.NextActions[0])
	}
	if got.NextActions[1].Kind != "install" || strings.Join(got.NextActions[1].Agents, ",") != "codex" {
		t.Fatalf("two-phase add: %+v", got.NextActions[1])
	}
}
