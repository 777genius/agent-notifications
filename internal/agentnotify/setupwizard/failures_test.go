package setupwizard

import (
	"errors"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func TestPortableFinalizationFailureKeepsSuccessfulActivation(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		for _, group := range []bool{false, true} {
			result := uapinstaller.Result{Outcome: uapinstaller.OutcomeCompleted, Client: uapinstaller.ClientResult{
				ClientID: "claude", BindingID: "bound-claude", Materialization: string(domain.MaterializationMaterialized), Activation: string(domain.ActivationActive), GroupPhase: "external_completed",
			}}
			if unchanged {
				result.Outcome = uapinstaller.OutcomeUnchanged
				result.Client.GroupPhase = ""
			}
			agents := []portable.Integration{portable.Claude}
			if group {
				agents = append(agents, portable.Codex)
				other := result.Client
				other.ClientID, other.BindingID = "codex", "bound-codex"
				result.Targets = []uapinstaller.ClientResult{result.Client, other}
			}
			err := portablesetup.ResultError{Result: result, Err: errors.New("client facts publication refused"), Finalization: true}
			req := Request{Action: ActionInstall, Agents: []string{"claude"}}
			var got Result
			if group {
				got = groupNotifyFailed(agents, req, Result{}, err)
			} else {
				got = portableInstallFailed(portable.Claude, req, Result{}, err)
			}
			got = attachWizardCommand(req, got)
			if got.Outcome != "incomplete" || got.Reason != "portable_finalization_incomplete" {
				t.Fatalf("group=%v unchanged=%v outcome: %+v", group, unchanged, got)
			}
			if len(got.Targets) != len(agents) {
				t.Fatalf("committed clients lost: %+v", got.Targets)
			}
			for _, target := range got.Targets {
				if target.Outcome != "incomplete" || target.Reason != "finalization_incomplete" || target.Materialization != string(domain.MaterializationMaterialized) || target.Activation != string(domain.ActivationActive) {
					t.Fatalf("activation relabeled after finalization failure: %+v", target)
				}
			}
			if len(got.NextActions) != 1 || got.NextActions[0].Kind != "resume" || len(got.NextActions[0].Command) != 0 || got.NextActions[0].Reason != "recovery_unavailable" {
				t.Fatalf("unverified recovery or activation retry: %+v", got.NextActions)
			}
			if len(got.Command) != 0 {
				t.Fatalf("generic retry bypassed unavailable recovery: %v", got.Command)
			}
		}
	}
}

func TestFinalizationResumeKeepsOnlyCheckedCommand(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude"}, PackageRoot: "/disposable/staging.zip"}
	// Recovery supplies the acquired directory after validating the owned
	// intent. Presentation must not also suggest the original staging input.
	checked := []string{"setup-notifications", "wizard", "--action", "install", "--agents", "claude", "--package", "/managed/acquired-source", "--yes"}
	next := NextAction{Kind: "resume", Agents: req.Agents, Reason: "portable_finalization_incomplete", Command: checked}
	got := attachWizardCommand(req, Result{Outcome: "incomplete", Reason: "portable_finalization_incomplete", NextActions: []NextAction{next}})
	if len(got.Command) != 0 || len(got.NextActions) != 1 || !reflect.DeepEqual(got.NextActions[0].Command, checked) {
		t.Fatalf("finalization replaced or duplicated checked recovery: %+v", got)
	}
}

func TestPortableRemoveFinalizationFailureKeepsCompletedAbsence(t *testing.T) {
	for _, group := range []bool{false, true} {
		result := uapinstaller.Result{Operation: uapinstaller.OpRemove, Outcome: uapinstaller.OutcomeCompleted, DataRetained: true,
			Binding: uapinstaller.BindingFacts{ClientID: "claude", BindingID: "bound-claude"}}
		agents := []portable.Integration{portable.Claude}
		if group {
			agents = append(agents, portable.Codex)
			// The pinned SDK leaves Materialization empty for targets removed
			// by a completed group operation, just as for a single removal.
			result.Targets = []uapinstaller.ClientResult{{ClientID: "claude"}, {ClientID: "codex"}}
		}
		err := portablesetup.ResultError{Result: result, Err: errors.New("handoff cleanup refused"), Finalization: true}
		var got Result
		if group {
			got = groupRemoveFailed(agents, Request{Action: ActionUninstall}, Result{}, err)
		} else {
			got = portableRemoveFailed(portable.Claude, Request{Action: ActionUninstall}, Result{}, err)
		}
		if got.Outcome != "incomplete" || got.Reason != "portable_finalization_incomplete" || !got.DataRetained || len(got.Targets) != len(agents) {
			t.Fatalf("removal finalization: %+v", got)
		}
		for _, target := range got.Targets {
			if target.Outcome != "completed" || target.Reason != "" || target.Materialization != string(domain.MaterializationAbsent) {
				t.Fatalf("completed removal lost: %+v", target)
			}
		}
		unrelated := portableRemoveFailed(portable.Pi, Request{Action: ActionUninstall}, Result{}, err)
		if len(unrelated.Targets) != 1 || unrelated.Targets[0].Outcome != "incomplete" || unrelated.Targets[0].Materialization != "" {
			t.Fatalf("removal inferred absence for an unreported client: %+v", unrelated)
		}
	}
}

func TestPortableRemoveFinalizationDistinguishesPriorAbsence(t *testing.T) {
	for _, unchanged := range []bool{false, true} {
		outcome := uapinstaller.OutcomeCompleted
		if unchanged {
			outcome = uapinstaller.OutcomeUnchanged
		}
		result := uapinstaller.Result{Operation: uapinstaller.OpRemove, Outcome: outcome,
			Binding: uapinstaller.BindingFacts{ClientID: "claude", BindingID: "bound-claude"},
			Targets: []uapinstaller.ClientResult{
				{ClientID: "claude", BindingID: "bound-claude"},
				{ClientID: "codex", Materialization: string(domain.MaterializationAbsent)},
			}}
		// RemoveGroup reports a previously absent client explicitly, while
		// leaving Materialization empty for a binding removed by this operation.
		err := portablesetup.ResultError{Result: result, Err: errors.New("handoff cleanup refused"), Finalization: true}
		got := groupRemoveFailed([]portable.Integration{portable.Claude, portable.Codex, portable.Pi}, Request{Action: ActionUninstall}, Result{}, err)
		if got.Outcome != "incomplete" || got.Reason != "portable_finalization_incomplete" || len(got.Targets) != 3 {
			t.Fatalf("removal finalization: %+v", got)
		}
		wantOutcome, wantReason := "completed", ""
		if outcome == uapinstaller.OutcomeUnchanged {
			wantOutcome, wantReason = "unchanged", "already_absent"
		}
		if target := got.Targets[0]; target.Outcome != wantOutcome || target.Reason != wantReason || target.Materialization != string(domain.MaterializationAbsent) {
			t.Fatalf("current operation %s relabeled: %+v", outcome, target)
		}
		if target := got.Targets[1]; target.Outcome != "unchanged" || target.Reason != "already_absent" || target.Materialization != string(domain.MaterializationAbsent) {
			t.Fatalf("prior absence relabeled: %+v", target)
		}
		if target := got.Targets[2]; target.Outcome != "incomplete" || target.Materialization != "" {
			t.Fatalf("unreported target gained removal facts: %+v", target)
		}
	}
}

func TestRetainedSwitchFinalizationCannotReturnSuccessfulOutcome(t *testing.T) {
	cause := errors.New("installed state read failed")
	err := portablesetup.ResultError{Result: uapinstaller.Result{Outcome: uapinstaller.OutcomeCompleted, DataRetained: true}, Err: cause, Finalization: true}
	got, returned := mapSwitchRetainedFailure(err, Result{})
	if !errors.Is(returned, cause) || got.Outcome != "incomplete" || got.Reason != "portable_finalization_incomplete" || !got.DataRetained {
		t.Fatalf("retained switch: %+v %v", got, returned)
	}
}
