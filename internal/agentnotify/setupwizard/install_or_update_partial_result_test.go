package setupwizard

import (
	"reflect"
	"testing"
)

func TestRetainCommittedPhaseTargetsPreservesFailure(t *testing.T) {
	prior := []TargetResult{{
		Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized",
		Warnings: []string{"committed-warning"},
	}}
	failure := []TargetResult{
		{Client: "codex", Unit: "agent-notify", Outcome: "incomplete", Reason: "source_mismatch", FailureStage: "preflight", Warnings: []string{"failed-warning"}},
		{Client: "pi", Unit: "agent-notify", Outcome: "absent", Reason: "preserved_existing_opt_out"},
	}
	got := retainCommittedPhaseTargets(prior, failure)
	if len(got) != 3 || got[0].Client != "claude" || got[1].Reason != "source_mismatch" ||
		got[1].FailureStage != "preflight" || got[2].Reason != "preserved_existing_opt_out" {
		t.Fatalf("lost phase facts: %+v", got)
	}
	got[0].Outcome = "mutated"
	got[1].Reason = "mutated"
	got[0].Warnings[0] = "changed-prior"
	got[1].Warnings[0] = "changed-later"
	if prior[0].Outcome != "completed" || prior[0].Warnings[0] != "committed-warning" ||
		failure[0].Reason != "source_mismatch" || failure[0].Warnings[0] != "failed-warning" {
		t.Fatal("aliased target or warning arrays")
	}
}

func TestRetainCommittedPhaseTargetsSameClientTransition(t *testing.T) {
	prior := []TargetResult{
		{Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized", Reason: "prior-verified-binding", TreeDigest: "digest-1"},
		{Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized", Reason: "prior-verified-binding", TreeDigest: "digest-1"},
	}
	failure := []TargetResult{{Client: "claude", Unit: "agent-notify", Outcome: "incomplete", Reason: "failed_add_after_update"}}
	got := retainCommittedPhaseTargets(prior, failure)
	if len(got) != 2 || got[0].Outcome != "completed" || got[0].Reason != "prior-verified-binding" ||
		got[0].TreeDigest != "digest-1" || got[1].Outcome != "incomplete" ||
		got[1].Reason != "failed_add_after_update" {
		t.Fatalf("conflated phases: %+v", got)
	}
	equal := []TargetResult{{Client: "claude", Unit: "agent-notify", Outcome: "completed", Materialization: "materialized", Reason: "prior-verified-binding", TreeDigest: "digest-1"}}
	if copied := retainCommittedPhaseTargets(prior, equal); !reflect.DeepEqual(copied, equal) {
		t.Fatalf("duplicate completed: %+v", copied)
	}
}
