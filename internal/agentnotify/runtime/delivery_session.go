package runtime

import (
	"context"
	"math"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// sessionDelivery revalidates and pins the installed generation around each
// platform operation. Readiness releases its lease before returning; delivery
// retains its lease until the platform handoff returns.
type sessionDelivery struct {
	Delivery
	installation notifier.ManagedInstallation
	clock        notifier.BootClock
	acquire      func(context.Context, string, installruntime.InstalledSnapshot) (installruntime.InstalledSnapshot, func(), error)
}

func (d *sessionDelivery) CheckReadiness(ctx context.Context, r notification.Request) notification.Readiness {
	out := notification.Readiness{
		CorrelationID: r.CorrelationID,
		Status:        "rejected",
		Reason:        "unsupported_notifier",
		Navigation:    notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"},
	}
	operation, release, reason := d.lease(ctx, r)
	if reason != "" {
		out.Reason = reason
		return out
	}
	defer release()
	return d.Delivery.CheckReadiness(operation, r)
}

func (d *sessionDelivery) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	out := notification.Receipt{
		CorrelationID: r.CorrelationID,
		Status:        "rejected",
		Reason:        "unsupported_notifier",
		Navigation:    notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"},
	}
	operation, release, reason := d.lease(ctx, r)
	if reason != "" {
		out.Reason = reason
		return out
	}
	defer release()
	return d.Delivery.Deliver(operation, r)
}

func (d *sessionDelivery) lease(ctx context.Context, r notification.Request) (context.Context, func(), string) {
	if d == nil || d.Delivery == nil || d.clock == nil || d.acquire == nil || ctx == nil {
		return nil, nil, "unsupported_notifier"
	}
	boot, now, err := d.clock.Now()
	remaining := r.Deadline.NotAfter - now
	if err != nil || boot == "" || boot != r.Deadline.BootID || now < 0 || math.IsNaN(now) || math.IsInf(now, 0) || remaining <= 0 || remaining > 15 || math.IsNaN(remaining) || math.IsInf(remaining, 0) || ctx.Err() != nil {
		return nil, nil, "expired"
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(remaining*float64(time.Second)))
	_, release, err := d.acquire(operation, d.installation.ControlRoot, d.installation.Expected)
	if err != nil || operation.Err() != nil {
		expired := operation.Err() != nil
		cancel()
		if release != nil {
			release()
		}
		if expired {
			return nil, nil, "expired"
		}
		return nil, nil, "unsupported_notifier"
	}
	return operation, func() {
		release()
		cancel()
	}, ""
}
