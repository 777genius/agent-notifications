//go:build linux || darwin

package agentnotify

import (
	"context"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/notification"
)

func TestServiceConfiguredRatesPreserveAccruedQuota(t *testing.T) {
	f := setup(t, journal.Limits{})
	f.policy.Rates = journal.RatePolicy{SessionPerMinute: 1, RuntimePerMinute: 1, Burst: 1}
	first := f.notify(payload("first"), caller())
	requireStatus(t, first, "submitted")
	limited := f.notify(payload("second"), caller())
	if limited.Status != "suppressed" || limited.Reason != "rate_limited" || f.effects.Load() != 1 {
		t.Fatal(limited, f.effects.Load())
	}
	// Raising the policy permits the second request without forgetting the first.
	f.policy.Rates = journal.RatePolicy{SessionPerMinute: 2, RuntimePerMinute: 2, Burst: 2}
	requireStatus(t, f.notify(payload("second"), caller()), "submitted")
	limited = f.notify(payload("third"), caller())
	if limited.Reason != "rate_limited" || f.effects.Load() != 2 {
		t.Fatal(limited, f.effects.Load())
	}
	f.policy.Rates = journal.RatePolicy{SessionPerMinute: 1, RuntimePerMinute: 1, Burst: 1}
	limited = f.notify(payload("third"), caller())
	if limited.Reason != "rate_limited" || f.effects.Load() != 2 {
		t.Fatal(limited, f.effects.Load())
	}
	f.advance()
	requireStatus(t, f.notify(payload("third"), caller()), "submitted")
}

func TestServiceInvalidRatePolicyDoesNotProbeAndReplaySkipsPolicy(t *testing.T) {
	f := setup(t, journal.Limits{})
	first := f.notify(payload("first"), caller())
	requireStatus(t, first, "submitted")
	f.policy.Rates = journal.RatePolicy{SessionPerMinute: 1, Burst: 1}
	f.s.deps.Readiness = readyFunc(func(context.Context, notification.Request) notification.Readiness {
		t.Fatal("invalid rate policy reached native readiness")
		return notification.Readiness{}
	})
	r := f.notify(payload("new"), caller())
	if r.Status != "rejected" || r.Reason != "configuration_invalid" || f.effects.Load() != 1 {
		t.Fatal(r, f.effects.Load())
	}
	loads := f.loads.Load()
	r = f.notify(payload("first"), caller())
	if !r.Replayed || r.TrackingID != first.TrackingID || f.loads.Load() != loads || f.effects.Load() != 1 {
		t.Fatal(r)
	}
}
