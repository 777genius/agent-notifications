//go:build linux

package notifier

import (
	"context"
	"errors"
	"time"

	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/google/uuid"
)

// TerminalDesktopDelivery is a narrow local-X11 branch of the existing native
// desktop contract. All non-qualified origins use ordinary Freedesktop delivery.
// Once a strict request reaches the daemon there is no second native attempt.
type TerminalDesktopDelivery struct {
	Clock BootClock
	Plain notification.DeliveryPort
}

var _ notification.DeliveryPort = (*TerminalDesktopDelivery)(nil)

func NewTerminalDesktopDelivery(clock BootClock) *TerminalDesktopDelivery {
	return &TerminalDesktopDelivery{Clock: clock, Plain: NewFreedesktopDelivery(clock)}
}

func (d *TerminalDesktopDelivery) informational(ctx context.Context, r notification.Request) notification.Receipt {
	r.Terminal = nil
	r.Navigation = notification.None
	r.Policy.ClickToFocus = false
	r.Target = notification.DesktopTarget{}
	if d == nil || d.Plain == nil {
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "unsupported_notifier"}
	}
	return d.Plain.Deliver(ctx, r)
}

func (d *TerminalDesktopDelivery) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	if d == nil || d.Clock == nil || ctx == nil {
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "unsupported_notifier"}
	}
	decision := notification.AssessDelivery(r)
	if decision.Status != "" {
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: decision.Status, Reason: decision.Reason}
	}
	if r.Terminal == nil || r.Navigation == notification.None || !r.Policy.ClickToFocus {
		return d.informational(ctx, r)
	}
	// Other native routes use their own installed target kind. Never turn a
	// model-supplied desktop thread into a terminal window.
	if r.Navigation != notification.BestEffort || r.Target != (notification.DesktopTarget{}) {
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "local_route_unavailable"}
	}
	duration, err := remainingBudget(d.Clock, r)
	if err != nil || ctx.Err() != nil {
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "expired"}
	}
	op, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	// Both checks precede any effect. A revoked/unknown target still gets its
	// informational banner without a click callback, under the same claim.
	if !daemon.StrictTerminalAuthorized(op, r.Terminal) {
		return d.informational(op, r)
	}
	if !daemon.StartDaemonOnDemandContext(op) {
		if op.Err() != nil {
			return notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "expired"}
		}
		return d.informational(op, r)
	}
	client, err := daemon.NewClient()
	if err != nil {
		return d.informational(op, r)
	}
	snapshot := *r.Terminal
	// An old daemon refuses notify_strict rather than interpreting it as a
	// legacy approximate focus. Do not retry after an ambiguous handoff.
	response, err := client.SendNotificationContext(op, r.Content.Title, desktopBodyWithSubtitle(r.Content), "", daemon.FocusHints{Strict: &snapshot, StrictSound: !r.Silent && r.Policy.SoundEnabled}, 30)
	if err != nil || response == nil || !response.Success || response.NotificationID == 0 {
		reason := "handoff_unconfirmed"
		if errors.Is(op.Err(), context.DeadlineExceeded) {
			reason = "expired"
		}
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "unknown", Reason: reason}
	}
	result := notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Backend: "linux_strict_terminal", Reason: "session_notification"}
	if response.FocusEnabled {
		result.Navigation = notification.NavigationResult{Capability: "available", Precision: "window_id", Scope: "verified_local_x11", Reason: "strict_origin"}
	} else {
		result.Navigation = notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "origin_revoked"}
	}
	return result
}

// sendVerifiedTerminal adapts Qoder's legacy notifier into the strict branch;
// status, sound and webhook remain owned by the existing handler.
func (n *Notifier) sendVerifiedTerminal(title, body, appIcon, sound string, opts sendOptions) error {
	clock := SystemBootClock{}
	boot, now, err := clock.Now()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r := notification.Request{
		Content:       notification.Content{Title: title, Body: body, Category: "info"},
		CorrelationID: uuid.NewString(),
		Deadline:      notification.Deadline{BootID: boot, NotAfter: now + 12},
		Policy: notification.PolicySnapshot{
			Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true,
		},
		Navigation: notification.BestEffort,
		Terminal:   opts.terminal,
		Silent:     true,
	}
	receipt := NewTerminalDesktopDelivery(clock).Deliver(ctx, r)
	if receipt.Status != "submitted" {
		return errors.New("strict_terminal_handoff_unconfirmed")
	}
	n.playSoundUnlessMuted(sound, opts)
	return nil
}
