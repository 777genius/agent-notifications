package notifier

import (
	"context"
	"errors"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/windowscallback"
)

const windowsToastBackend = "windows_toast"

type windowsToastSession interface {
	Ready(context.Context) error
	Submit(context.Context, notification.Request) error
	Close() error
}

// WindowsToastDelivery is the Windows explicit-notify adapter. It shows one
// informational or explicitly bound native toast. A possible Show is never
// followed by alternate delivery or automatic replay.
type WindowsToastDelivery struct {
	Clock          BootClock
	Open           func(context.Context) (windowsToastSession, error)
	OpenNavigation func(context.Context, notification.Request) (windowsToastSession, error)
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
	native := false
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
	} else if r.Target.Provider == "codex" && r.Target.ThreadID != "" && r.Target.Windows.SnapshotPath != "" && r.Target.Windows.SHA256 != "" {
		native = true
		out.Navigation = notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"}
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
	if native {
		navOpen := d.OpenNavigation
		if navOpen == nil {
			navOpen = openWindowsNavigation
		}
		open = func(ctx context.Context) (windowsToastSession, error) { return navOpen(ctx, r) }
	}
	openReady := func(candidate func(context.Context) (windowsToastSession, error)) (windowsToastSession, error) {
		session, e := candidate(operation)
		if e != nil || session == nil {
			if e == nil {
				e = windowscallback.ErrUnavailable
			}
			return nil, e
		}
		if e = session.Ready(operation); e != nil {
			_ = session.Close()
			return nil, e
		}
		return session, nil
	}
	session, err := openReady(open)
	if err != nil && native && nav == notification.BestEffort && operation.Err() == nil && !errors.Is(err, windowscallback.ErrUnknown) && !errors.Is(err, windowscallback.ErrDisabled) {
		native = false
		out.Navigation = notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"}
		fallback := d.Open
		if fallback == nil {
			fallback = openWindowsToast
		}
		session, err = openReady(fallback)
	}
	if err != nil {
		if errors.Is(err, windowscallback.ErrDisabled) {
			return finish("rejected", "permission_disabled")
		}
		if operation.Err() != nil {
			return finish("rejected", "expired")
		}
		return finish("rejected", "unsupported_notifier")
	}
	defer func() { _ = session.Close() }()
	if _, err = remainingBudget(d.Clock, r); err != nil || operation.Err() != nil {
		return finish("rejected", "expired")
	}
	if native {
		out.Navigation = notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "selected_windows_generation", Reason: "configured_codex_desktop"}
	}
	if readOnly {
		reason := "permission_authorized"
		if native {
			reason = "installed_readiness_verified"
			if observed, ok := session.(interface{ ReadinessReason() string }); ok {
				reason = observed.ReadinessReason()
			}
		}
		return finish("ready", reason)
	}
	err = session.Submit(operation, r)
	if err != nil {
		operationErr := operation.Err()
		if errors.Is(err, windowscallback.ErrUnknown) || operationErr != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason := "handoff_unconfirmed"
			if errors.Is(operationErr, context.DeadlineExceeded) || operationErr == nil && errors.Is(err, context.DeadlineExceeded) {
				reason = "native_submission_deadline"
			} else if errors.Is(operationErr, context.Canceled) || operationErr == nil && errors.Is(err, context.Canceled) {
				reason = "native_submission_cancelled"
			}
			return finish("unknown", reason)
		}
		return finish("rejected", "unsupported_notifier")
	}
	return finish("submitted", "session_notification")
}
