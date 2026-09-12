//go:build linux || darwin

// Regression cases retained from independent service review.
package agentnotify

import (
	"context"
	"errors"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

func TestReviewNavigationDowngrade(t *testing.T) {
	for _, phase := range []string{"readiness", "delivery"} {
		t.Run(phase, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			p := payload("navigation-review")
			p.Navigation = notification.BestEffort
			limited := notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "application_unavailable"}
			f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
				n := origin.ResolveCodex(caller(), f.policy.Route).Navigation
				if phase == "readiness" {
					n = limited
				}
				return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "ready", Navigation: n}
			})
			f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
				f.effects.Add(1)
				return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "fake", Navigation: limited}
			})
			a := f.notify(p, caller())
			b := f.notify(p, caller())
			t.Logf("port navigation=%+v; first=%+v; replay=%+v; fake effects=%d", limited, a, b, f.effects.Load())
			if a.Status != "submitted" || !b.Replayed || f.effects.Load() != 1 {
				t.Fatal("invalid reproduction")
			}
			if a.Navigation.Capability != "unavailable" || b.Navigation.Capability != "unavailable" {
				t.Error("receipt and durable replay claim a chat action despite explicit port downgrade")
			}
		})
	}
}

func TestReviewSuppressedReceipt(t *testing.T) {
	f := setup(t, journal.Limits{})
	calls := 0
	f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
		calls++ // A port invocation is not an OS effect.
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "suppressed", Reason: "disabled", Backend: "fake", Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none"}}
	})
	a := f.notify(payload("suppressed-review"), caller())
	b := f.notify(payload("suppressed-review"), caller())
	t.Logf("first=%+v; replay=%+v; delivery calls=%d; effects=%d", a, b, calls, f.effects.Load())
	if calls != 1 || f.effects.Load() != 0 || !b.Replayed {
		t.Fatal("invalid reproduction")
	}
	if a.Status != "suppressed" || b.Status != "suppressed" || a.Reason != "disabled" {
		t.Error("valid proven-no-effect suppression is mislabeled as uncertain malformed acknowledgement")
	}
}

func TestReviewSafeTargetFailure(t *testing.T) {
	f := setup(t, journal.Limits{})
	p := payload("target-review")
	p.Navigation = notification.BestEffort
	f.s.deps.Target = TargetFunc(func(context.Context, origin.Context, origin.RoutePolicy) (origin.Target, error) {
		return origin.Target{}, errors.New("resolver failed")
	})
	a := f.notify(p, caller())
	t.Logf("resolver error=%+v; effects=%d", a, f.effects.Load())
	if a.Status != "rejected" || a.Reason != "target_unavailable" || !a.RetrySafe || f.effects.Load() != 0 {
		t.Fatal(a)
	}
	f.s.deps.Target = TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
		return origin.ResolveCodex(o, p), nil
	})
	b := f.notify(p, caller())
	t.Logf("same key after resolver repair=%+v", b)
	if b.Replayed || b.Status != "submitted" {
		t.Fatal(b)
	}
}

func TestReviewRateFailureIsPreAdmission(t *testing.T) {
	f := setup(t, journal.Limits{})
	for _, id := range []string{"rate-1", "rate-2", "rate-3"} {
		requireStatus(t, f.notify(payload(id), caller()), "submitted")
	}
	a := f.notify(payload("rate-4"), caller())
	t.Logf("rate failure=%+v; effects=%d", a, f.effects.Load())
	if a.Status != "suppressed" || a.Reason != "rate_limited" || f.effects.Load() != 3 {
		t.Fatal(a)
	}
	f.advance()
	b := f.notify(payload("rate-4"), caller())
	t.Logf("same key after rate window=%+v", b)
	if b.Replayed || b.Status != "submitted" {
		t.Fatal(b)
	}
}
