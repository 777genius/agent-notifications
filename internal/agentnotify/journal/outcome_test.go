//go:build linux || darwin

package journal

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestOutcomeSuppressionCASAndCompatibility(t *testing.T) {
	s, c := fixture(t, Limits{})
	a := admission("suppressed")
	first := mustAdmit(t, s, a)
	n := Navigation{Capability: "disabled", Precision: "none", Reason: "disabled"}
	ctx := testContext(t)
	if e := s.FinalizeOutcome(ctx, a.Key, strings.Repeat("a", 64), "suppressed", "disabled", "fake", &n); !errors.Is(e, ErrCAS) {
		t.Fatal(e)
	}
	if e := s.FinalizeOutcome(ctx, a.Key, first.Record.Attempt, "suppressed", "disabled", "fake", &n); e != nil {
		t.Fatal(e)
	}
	n.Reason = "mutated"
	for _, finalize := range []func() error{
		func() error { return s.Finalize(ctx, a.Key, first.Record.Attempt, "submitted", "late", "fake") },
		func() error {
			return s.FinalizeOutcome(ctx, a.Key, first.Record.Attempt, "unknown", "late", "fake", &n)
		},
	} {
		if e := finalize(); !errors.Is(e, ErrCAS) {
			t.Fatal(e)
		}
	}
	s = reopen(t, s, c)
	r, e := s.Lookup(ctx, a.Key, a.Digest)
	if e != nil || r.Record.Receipt.Status != "suppressed" || r.Record.Receipt.Reason != "disabled" || r.Record.Receipt.Backend != "fake" || r.Record.Receipt.OutcomeNavigation.Reason != "disabled" || !reflect.DeepEqual(r.Record.Receipt.Decision, a.Decision) {
		t.Fatal(r, e)
	}
	// Legacy v1 records omit the optional field and remain readable after reopen.
	old := admission("legacy")
	v := mustAdmit(t, s, old)
	if e := s.Finalize(ctx, old.Key, v.Record.Attempt, "submitted", "accepted", "old"); e != nil {
		t.Fatal(e)
	}
	s = reopen(t, s, c)
	v, e = s.Lookup(ctx, old.Key, old.Digest)
	if e != nil || v.Record.Receipt.OutcomeNavigation != nil || v.Record.Receipt.Status != "submitted" {
		t.Fatal(v, e)
	}
}

func TestOutcomeRejectsInvalidNavigation(t *testing.T) {
	s, _ := fixture(t, Limits{})
	a := admission("R")
	a.Decision.Navigation = Navigation{Capability: "unavailable", Precision: "none"}
	r := mustAdmit(t, s, a)
	for _, n := range []Navigation{{}, {Capability: "available", Precision: "chat_id", Scope: "invented"}, {Capability: "disabled", Precision: "chat_id"}} {
		if e := s.FinalizeOutcome(testContext(t), a.Key, r.Record.Attempt, "submitted", "accepted", "fake", &n); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	v, e := s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil || v.Record.Receipt.Reason != "pending_submission" || v.Record.Receipt.OutcomeNavigation != nil {
		t.Fatal(v, e)
	}
}
