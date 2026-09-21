//go:build linux

package notifier

import (
	"context"
	"errors"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/godbus/dbus/v5"
)

const linuxFreedesktopBackend = "linux_freedesktop"

type sessionNotifications interface {
	Ready(context.Context) error
	Submit(context.Context, notification.Request) (uint32, error)
	Close() error
}

// FreedesktopDelivery is the Linux explicit-notify adapter. It makes one
// session-bus Notify call and never falls back after a possible D-Bus effect.
type FreedesktopDelivery struct {
	Clock BootClock
	Open  func(context.Context) (sessionNotifications, error)
}

var _ notification.DeliveryPort = (*FreedesktopDelivery)(nil)
var _ notification.ReadinessPort = (*FreedesktopDelivery)(nil)

func NewFreedesktopDelivery(clock BootClock) *FreedesktopDelivery {
	return &FreedesktopDelivery{Clock: clock, Open: openSessionNotifications}
}

func (d *FreedesktopDelivery) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return d.checkAndDeliver(ctx, r, false)
}

func (d *FreedesktopDelivery) CheckReadiness(ctx context.Context, r notification.Request) notification.Readiness {
	out := d.checkAndDeliver(ctx, r, true)
	return notification.Readiness{CorrelationID: out.CorrelationID, Status: out.Status, Reason: out.Reason, Backend: out.Backend, Navigation: out.Navigation}
}

func (d *FreedesktopDelivery) checkAndDeliver(ctx context.Context, r notification.Request, readOnly bool) notification.Receipt {
	out := notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "malformed_request", Backend: linuxFreedesktopBackend, Navigation: notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"}}
	finish := func(status, reason string) notification.Receipt { out.Status, out.Reason = status, reason; return out }
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
		open = openSessionNotifications
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
	_, err = session.Submit(operation, r)
	if err != nil {
		// Notify is a one-way D-Bus handoff. Once invoked, an error cannot prove
		// that the notification daemon did not accept the request.
		return finish("unknown", "handoff_unconfirmed")
	}
	return finish("submitted", "session_notification")
}

type dbusNotifications struct{ conn *dbus.Conn }

func openSessionNotifications(ctx context.Context) (sessionNotifications, error) {
	type outcome struct {
		conn *dbus.Conn
		err  error
	}
	done := make(chan outcome, 1)
	go func() { conn, err := dbus.ConnectSessionBus(); done <- outcome{conn, err} }()
	select {
	case <-ctx.Done():
		go func() {
			o := <-done
			if o.conn != nil {
				_ = o.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case o := <-done:
		if o.err != nil {
			return nil, o.err
		}
		return &dbusNotifications{conn: o.conn}, nil
	}
}

func (n *dbusNotifications) Ready(ctx context.Context) error {
	var owned bool
	if err := n.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.Notifications").Store(&owned); err != nil {
		return err
	}
	if !owned {
		return errors.New("notifications unavailable")
	}
	return nil
}

func (n *dbusNotifications) Submit(ctx context.Context, r notification.Request) (uint32, error) {
	expire := int32(15000)
	if deadline, ok := ctx.Deadline(); ok {
		ms := time.Until(deadline).Milliseconds()
		if ms < 1 {
			return 0, context.DeadlineExceeded
		}
		if ms > 15000 {
			ms = 15000
		}
		expire = int32(ms)
	}
	hints := map[string]dbus.Variant{}
	if r.Silent {
		hints["suppress-sound"] = dbus.MakeVariant(true)
	}
	var id uint32
	err := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0, "agent-notifications", uint32(0), "", r.Content.Title, r.Content.Body, []string{}, hints, expire).Store(&id)
	return id, err
}

func (n *dbusNotifications) Close() error {
	if n == nil || n.conn == nil {
		return nil
	}
	return n.conn.Close()
}
