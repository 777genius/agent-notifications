package setupwizard

import (
	"context"
	"errors"
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
			got := siblingCompatibilityUnavailable(agent, req, Result{Action: string(req.Action), Command: []string{"setup-notifications", "wizard", "--action", "update", "--agents", "claude,codex"}, NextActions: []NextAction{{Kind: "update", Agents: []string{"claude", "codex"}}}})
			if got.Outcome != "incomplete" || got.Reason != "sibling_compatibility_unavailable" ||
				len(got.NextActions) != 0 || len(got.Command) != 0 {
				t.Fatalf("unsupported sibling recovery must refuse without expanding selection: %+v", got)
			}
			if agent != "" && (len(got.Targets) != 1 || got.Targets[0].Client != string(agent) ||
				got.Targets[0].Outcome != "incomplete") {
				t.Fatalf("failed client lost from diagnostics: %+v", got.Targets)
			}
			// Exercise the same mapper used by Plan: changing only a failing SDK
			// error type must not fabricate Claude/Codex update authority.
			mapped, err := mapPreviewFailure(context.Background(), req, agent, portablesetup.Materializer{}, portablesetup.Identity{},
				uapinstaller.ErrTargetFactsUnavailable, Result{Action: string(req.Action), Command: []string{"setup-notifications", "wizard", "--action", "update", "--agents", "claude,codex"}, NextActions: []NextAction{{Kind: "update", Agents: []string{"claude", "codex"}}}})
			if err == nil || len(mapped.NextActions) != 0 || len(mapped.Command) != 0 || mapped.Reason != "sibling_compatibility_unavailable" {
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
			if want := []string{"setup-notifications", "wizard", "--action", "update", "--agents", "claude,codex", "--yes"}; !reflect.DeepEqual(got.NextActions[0].Command, want) {
				t.Fatalf("known recovery command changed: got %v, want %v", got.NextActions[0].Command, want)
			}
		})
	}
}

// A group result must not derive a cross-client retry from agents[0] only.
// Persisted SDK error facts take the real groupNotifyFailed branch rather than
// testing siblingCompatibilityUnavailable in isolation.
func TestGroupCompatibilityMixedSelectionNeverRecommendsUnselectedClient(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agents []portable.Integration
	}{
		{"claude-pi", []portable.Integration{portable.Claude, portable.Pi}},
		{"pi-claude", []portable.Integration{portable.Pi, portable.Claude}},
		{"codex-local", []portable.Integration{portable.Codex, portable.CopilotVSCode}},
		{"local-codex", []portable.Integration{portable.CopilotVSCode, portable.Codex}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := make([]string, 0, len(tc.agents))
			for _, agent := range tc.agents {
				selected = append(selected, string(agent))
			}
			req := Request{Action: ActionUpdate, Agents: selected}
			for _, cause := range []error{uapinstaller.ErrCompatibilityUnavailable, uapinstaller.ErrTargetFactsUnavailable} {
				for _, failure := range []error{
					cause,
					portablesetup.ResultError{Err: cause},
					portablesetup.ResultError{Err: cause, Result: uapinstaller.Result{Client: uapinstaller.ClientResult{ClientID: string(tc.agents[0])}}},
					portablesetup.ResultError{Err: cause, Result: uapinstaller.Result{Client: uapinstaller.ClientResult{ClientID: string(tc.agents[1])}}},
				} {
					out := Result{Action: string(req.Action), Command: []string{"setup-notifications", "wizard", "--action", "update", "--agents", "claude,codex"},
						NextActions: []NextAction{{Kind: "update", Agents: []string{"claude", "codex"}}}}
					got := groupNotifyFailed(tc.agents, req, out, failure)
					if got.Outcome != "incomplete" || got.Reason != "sibling_compatibility_unavailable" ||
						len(got.NextActions) != 0 || len(got.Command) != 0 {
						t.Fatalf("mixed group suggested unselected update for %v: %+v", failure, got)
					}
					// Selecting a non-Claude/Codex sibling decides retry policy;
					// it cannot replace the actual failed client's diagnostic.
					failed := string(tc.agents[0])
					var persisted portablesetup.ResultError
					if errors.As(failure, &persisted) && persisted.Result.Client.ClientID != "" {
						failed = persisted.Result.Client.ClientID
					}
					if len(got.Targets) != 1 || got.Targets[0].Client != failed || got.Targets[0].Outcome != "incomplete" {
						t.Fatalf("failed client misattributed for %v: got %+v, want %s", failure, got.Targets, failed)
					}
				}
			}
		})
	}
}

func TestGroupCompatibilityKnownClaudeCodexOrderKeepsVerifiedRecovery(t *testing.T) {
	for _, order := range [][]portable.Integration{
		{portable.Claude, portable.Codex}, {portable.Codex, portable.Claude},
	} {
		req := Request{Action: ActionUpdate, Agents: []string{string(order[0]), string(order[1])}}
		persisted := portablesetup.ResultError{Err: uapinstaller.ErrCompatibilityUnavailable,
			Result: uapinstaller.Result{Client: uapinstaller.ClientResult{ClientID: string(order[0])}}}
		got := groupNotifyFailed(order, req, Result{Action: string(req.Action)}, persisted)
		if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" ||
			!reflect.DeepEqual(got.NextActions[0].Agents, []string{"claude", "codex"}) {
			t.Fatalf("verified legacy group recovery changed for %v: %+v", order, got)
		}
		if want := []string{"setup-notifications", "wizard", "--action", "update", "--agents", "claude,codex", "--yes"}; !reflect.DeepEqual(got.NextActions[0].Command, want) {
			t.Fatalf("verified group recovery command changed for %v: got %v, want %v", order, got.NextActions[0].Command, want)
		}
	}
}

// A single Plan preview can fail on Claude/Codex before a mixed group Apply
// ever reaches groupNotifyFailed. The outer confirmed selection still fences
// the legacy recovery; selecting Pi/Local never implicitly adds Codex/Claude.
func TestSiblingCompatibilityMixedPlanPreviewCannotExpandSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		agent    portable.Integration
		selected []string
		legacy   bool
	}{
		{"claude-with-pi", portable.Claude, []string{"claude", "pi"}, false},
		{"pi-before-claude", portable.Claude, []string{"pi", "claude"}, false},
		{"codex-with-local", portable.Codex, []string{"codex", "copilot-vscode"}, false},
		{"local-before-codex", portable.Codex, []string{"copilot-vscode", "codex"}, false},
		{"claude-only", portable.Claude, []string{"claude"}, true},
		{"codex-with-claude", portable.Codex, []string{"codex", "claude"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Action: ActionUpdate, Agents: tc.selected}
			out := Result{Action: string(req.Action), Command: []string{"setup-notifications", "wizard", "--action", "update", "--agents", "claude,codex"}}
			got, err := mapPreviewFailure(context.Background(), req, tc.agent, portablesetup.Materializer{}, portablesetup.Identity{},
				uapinstaller.ErrTargetFactsUnavailable, out)
			if err == nil || got.Reason != "sibling_compatibility_unavailable" {
				t.Fatalf("preview failure lost: %+v / %v", got, err)
			}
			if tc.legacy {
				if len(got.NextActions) != 1 || !reflect.DeepEqual(got.NextActions[0].Agents, []string{"claude", "codex"}) {
					t.Fatalf("known Claude/Codex advice lost: %+v", got)
				}
			} else if len(got.NextActions) != 0 || len(got.Command) != 0 {
				t.Fatalf("mixed Plan added unselected target: %+v", got)
			}
		})
	}
}
