package setupwizard

import (
	"reflect"
	"testing"
)

func TestRetainCommittedPhaseTargetsPreservesFailure(t *testing.T) {
	prior := []TargetResult{{
		Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized",
	}}
	failure := []TargetResult{
		{Client: "codex", Unit: "agent-notify", Outcome: "incomplete", Reason: "source_mismatch", FailureStage: "preflight"},
		{Client: "pi", Unit: "agent-notify", Outcome: "absent", Reason: "preserved_existing_opt_out"},
	}
	got := retainCommittedPhaseTargets(prior, failure)
	if len(got) != 3 || got[0].Client != "claude" || got[1].Reason != "source_mismatch" ||
		got[1].FailureStage != "preflight" || got[2].Reason != "preserved_existing_opt_out" {
		t.Fatalf("lost phase facts: %+v", got)
	}
	got[0].Outcome = "mutated"
	got[1].Reason = "mutated"
	if prior[0].Outcome != "completed" || failure[0].Reason != "source_mismatch" {
		t.Fatal("aliased input")
	}
}

func TestRetainCommittedPhaseTargetsSameClientTransition(t *testing.T) {
	prior := []TargetResult{
		{Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized"},
		{Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized"},
	}
	failure := []TargetResult{{Client: "claude", Unit: "agent-notify", Outcome: "incomplete", Reason: "failed_add_after_update"}}
	got := retainCommittedPhaseTargets(prior, failure)
	if len(got) != 2 || got[0].Outcome != "completed" || got[0].Reason != "committed_in_prior_phase" ||
		got[1].Reason != "failed_add_after_update" {
		t.Fatalf("conflated phases: %+v", got)
	}
	equal := []TargetResult{{Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized"}}
	if copied := retainCommittedPhaseTargets(prior, equal); !reflect.DeepEqual(copied, equal) {
		t.Fatalf("duplicate completed: %+v", copied)
	}
}
