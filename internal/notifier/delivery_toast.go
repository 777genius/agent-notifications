package notifier

import (
	"context"
	"errors"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
)

const windowsToastBackend = "windows_toast"

type windowsToastSession interface {
	Ready(context.Context) error
	Submit(context.Context, notification.Request) error
	Close() error
}

// WindowsToastDelivery is the Windows explicit-notify adapter. It shows one
// informational toast and never starts the click-to-focus handler or falls
// back to beeep after a possible toast effect.
type WindowsToastDelivery struct {
	Clock BootClock
	Open  func(context.Context) (windowsToastSession, error)
}

var _ notification.DeliveryPort = (*WindowsToastDelivery)(nil)
var _ notification.ReadinessPort = (*WindowsToastDelivery)(nil)

func NewWindowsToastDelivery(clock BootClock) *WindowsToastDelivery {
	return &WindowsToastDelivery{Clock: clock, Open: openWindowsToast}
}

func (d *WindowsToastDelivery) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return d.checkAndDeliver(ctx, r, false)
}

func (d *WindowsToastDelivery) CheckReadiness(ctx context.Context, r notification.Request) notification.Readiness {
	out := d.checkAndDeliver(ctx, r, true)
	return notification.Readiness{CorrelationID: out.CorrelationID, Status: out.Status, Reason: out.Reason, Backend: out.Backend, Navigation: out.Navigation}
}

func (d *WindowsToastDelivery) checkAndDeliver(ctx context.Context, r notification.Request, readOnly bool) notification.Receipt {
	out := notification.Receipt{
		CorrelationID: r.CorrelationID,
		Status:        "rejected",
		Reason:        "malformed_request",
		Backend:       windowsToastBackend,
		Navigation:    notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"},
	}
	finish := func(status, reason string) notification.Receipt {
		out.Status = status
		out.Reason = reason
		return out
	}
	nav := r.Navigation
	if nav == "" {
		nav = notification.Required
	}
	if nav != notification.Required && nav != notification.BestEffort && nav != notification.None {
		return out
	}
	if !r.Policy.Valid {
		return finish("rejected", "configuration_invalid")
	}
	if !r.Policy.ExplicitEnabled || !r.Policy.DesktopEnabled {
		return finish("suppressed", "disabled")
	}
	if nav == notification.None || !r.Policy.ClickToFocus {
		out.Navigation = notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "navigation_disabled"}
		if nav == notification.Required {
			return finish("rejected", "navigation_disabled")
		}
	} else if r.Target.ThreadID != "" {
		out.Navigation = notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"}
		if nav == notification.Required {
			return finish("rejected", "navigation_unavailable")
		}
	} else if nav == notification.Required {
		return finish("rejected", "navigation_unavailable")
	}
	if d == nil || d.Clock == nil {
		return finish("rejected", "unsupported_notifier")
	}
	open := d.Open
	if open == nil {
		open = openWindowsToast
	}
	remaining, err := remainingBudget(d.Clock, r)
	if err != nil || ctx.Err() != nil {
		return finish("rejected", "expired")
	}
	operation, cancel := context.WithTimeout(ctx, remaining)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-operation.Done():
				return
			case <-ticker.C:
				if _, e := remainingBudget(d.Clock, r); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-stopped }()
	session, err := open(operation)
	if err != nil || session == nil {
		if operation.Err() != nil {
			return finish("rejected", "expired")
		}
		return finish("rejected", "unsupported_notifier")
	}
	defer func() { _ = session.Close() }()
	if err = session.Ready(operation); err != nil {
		if operation.Err() != nil {
			return finish("rejected", "expired")
		}
		return finish("rejected", "unsupported_notifier")
	}
	if _, err = remainingBudget(d.Clock, r); err != nil || operation.Err() != nil {
		return finish("rejected", "expired")
	}
	if readOnly {
		return finish("ready", "permission_authorized")
	}
	err = session.Submit(operation, r)
	if err != nil {
		if operation.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return finish("unknown", "handoff_unconfirmed")
		}
		return finish("rejected", "unsupported_notifier")
	}
	return finish("submitted", "session_notification")
}
