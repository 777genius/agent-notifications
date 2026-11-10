package setupwizard

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
)

func TestPostSetupActionsOnlyOfferDeliveryToReadySelectedClients(t *testing.T) {
	for _, outcome := range []string{"completed", "installed", "absent"} {
		t.Run(outcome, func(t *testing.T) {
			req := Request{Action: ActionInstall, ControlRoot: filepath.Join(t.TempDir(), "control"), Agents: []string{"claude", "codex"}}
			got := attachReadiness(req, []portable.Integration{portable.Claude, portable.Codex}, Result{
				Outcome: "completed", Generation: 7,
				Targets: []TargetResult{
					{Client: "claude", Unit: "hooks", Outcome: "completed"},
					{Client: "claude", Unit: "agent-notify", Outcome: "absent"},
					{Client: "codex", Unit: "agent-notify", Outcome: outcome},
					{Client: "codex", Unit: "agent-notify", Outcome: outcome},
					{Client: "pi", Unit: "agent-notify", Outcome: "installed"},
				},
			}, true)
			if outcome == "absent" {
				if len(got.NextActions) != 0 {
					t.Fatalf("unselected ready target enabled follow-ups: %+v", got.NextActions)
				}
				return
			}
			if len(got.NextActions) != 3 {
				t.Fatalf("missing follow-ups: %+v", got.NextActions)
			}
			for _, next := range got.NextActions {
				wantAgents := []string{"codex"}
				switch next.Kind {
				case "restart-client":
					wantAgents = req.Agents
				case "request-permission":
					wantCommand := []string{"setup-notifications", "request-permission", "--control-root", req.ControlRoot, "--expected-generation", "7"}
					if !reflect.DeepEqual(next.Command, wantCommand) {
						t.Fatalf("global permission command changed: %v", next.Command)
					}
				case "test-notification":
					if !reflect.DeepEqual(next.Command, []string{"notify"}) {
						t.Fatalf("notification tool command changed: %v", next.Command)
					}
				default:
					t.Fatalf("unexpected follow-up: %+v", next)
				}
				if !reflect.DeepEqual(next.Agents, wantAgents) {
					t.Fatalf("%s agents = %v, want %v", next.Kind, next.Agents, wantAgents)
				}
			}
		})
	}
}
