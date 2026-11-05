package setupwizard

import (
	"context"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
)

func TestSiblingCompatibilityFailureNeverProposesUnselectedOtherProduct(t *testing.T) {
	for _, agent := range []portable.Integration{portable.Pi, portable.Cursor, portable.CopilotVSCode, ""} {
		t.Run(string(agent), func(t *testing.T) {
			req := Request{Action: ActionUpdate, Agents: []string{string(agent)}}
			got := siblingCompatibilityUnavailable(agent, req, Result{Action: string(req.Action), NextActions: []NextAction{{Kind: "update", Agents: []string{"claude", "codex"}}}})
			if got.Outcome != "incomplete" || got.Reason != "sibling_compatibility_unavailable" ||
				len(got.NextActions) != 0 {
				t.Fatalf("unsupported sibling recovery must refuse without expanding selection: %+v", got)
			}
			if agent != "" && (len(got.Targets) != 1 || got.Targets[0].Client != string(agent) ||
				got.Targets[0].Outcome != "incomplete") {
				t.Fatalf("failed client lost from diagnostics: %+v", got.Targets)
			}
			// Exercise the same mapper used by Plan: changing only a failing SDK
			// error type must not fabricate Claude/Codex update authority.
			mapped, err := mapPreviewFailure(context.Background(), req, agent, portablesetup.Materializer{}, portablesetup.Identity{},
				uapinstaller.ErrTargetFactsUnavailable, Result{Action: string(req.Action), NextActions: []NextAction{{Kind: "update", Agents: []string{"claude", "codex"}}}})
			if err == nil || len(mapped.NextActions) != 0 || mapped.Reason != "sibling_compatibility_unavailable" {
				t.Fatalf("preview fabricated sibling mutation: %+v / %v", mapped, err)
			}
		})
	}
}

func TestSiblingCompatibilityKnownClaudeCodexRecommendationUnchanged(t *testing.T) {
	for _, agent := range []portable.Integration{portable.Claude, portable.Codex} {
		t.Run(string(agent), func(t *testing.T) {
			req := Request{Action: ActionUpdate, Agents: []string{string(agent)}}
			got := siblingCompatibilityUnavailable(agent, req, Result{Action: string(req.Action)})
			if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" ||
				!reflect.DeepEqual(got.NextActions[0].Agents, []string{"claude", "codex"}) {
				t.Fatalf("known verified recovery changed: %+v", got)
			}
		})
	}
}
