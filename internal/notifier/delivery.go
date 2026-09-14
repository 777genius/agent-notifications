package notifier

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// NativeInstallation is a lease from the managed component owner. Verify must
// check its trusted offline manifest/fingerprint before returning a path; unknown
// helpers must never be executed to discover support. The lease pins the stable
// bundle against update through handoff. PR2 composition owns that lock/ledger.
type NativeInstallation interface {
	Acquire(context.Context) (NativeLease, error)
}
type NativeLease interface {
	BundlePath() string
	ExecutablePath() string
	Release()
}

// BootClock uses the same boot epoch and continuous seconds as native. Callers
// use Now at transport admission; Deliver never starts a new request budget.
type BootClock interface {
	Now() (bootID string, seconds float64, err error)
}

type NativeProcess interface {
	Probe(context.Context, string) ([]byte, error)
	// Launch returns whether a process might have handed work to LaunchServices.
	// A started launcher is an unknown outcome until a correlated native receipt.
	Launch(context.Context, string, string, string) (mayHaveHandedOff bool, err error)
}

type NativeAttempt struct{ Directory, RequestPath, ReceiptPath, Nonce string }
type NativeSpool interface {
	Prepare(context.Context, notification.Request, func(string) ([]byte, error)) (NativeAttempt, error)
	Receipt(NativeAttempt) ([]byte, error)
	// Expire removes only this owned attempt after the original boot deadline.
	// It must not be called merely because the open launcher exited.
	Expire(NativeAttempt) error
}

// StructuredDelivery is independent of Notifier's legacy best-effort policy.
// Dependencies are immutable and safe for concurrent requests. No globals,
// hook state, analyzer, bell, webhook or fallback are consulted by Deliver.
type StructuredDelivery struct {
	Installation NativeInstallation
	Clock        BootClock
	Process      NativeProcess
	Spool        NativeSpool
}

var _ notification.DeliveryPort = (*StructuredDelivery)(nil)

func (d *StructuredDelivery) remaining(r notification.Request) (time.Duration, error) {
	boot, now, err := d.Clock.Now()
	seconds := r.Deadline.NotAfter - now
	if err != nil || boot == "" || boot != r.Deadline.BootID || math.IsNaN(now) || math.IsInf(now, 0) || now < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > 15 {
		return 0, errors.New("expired")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (d *StructuredDelivery) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return d.checkAndDeliver(ctx, r, false)
}

// CheckReadiness releases its lease before returning; callers may then enter
// their journal transaction. Ready is a snapshot, never a delivery guarantee.
func (d *StructuredDelivery) CheckReadiness(ctx context.Context, r notification.Request) notification.Readiness {
	out := d.checkAndDeliver(ctx, r, true)
	return notification.Readiness{CorrelationID: out.CorrelationID, Status: out.Status, Reason: out.Reason, Backend: out.Backend, Navigation: out.Navigation}
}

func (d *StructuredDelivery) checkAndDeliver(ctx context.Context, r notification.Request, readOnly bool) notification.Receipt {
	out := notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "malformed_request", Backend: "macos_native", Navigation: notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "navigation_unavailable"}}
	finish := func(status, reason string) notification.Receipt { out.Status = status; out.Reason = reason; return out }
	// Check literal content and identity even when disabled. Nothing below uses
	// legacy bracket extraction, status synthesis, or policy defaults.
	wire := nativeprotocol.Request{SchemaVersion: 1, CorrelationID: r.CorrelationID, Nonce: "00000000-0000-4000-8000-000000000000", BootID: r.Deadline.BootID, NotAfter: r.Deadline.NotAfter, Title: r.Content.Title, Body: r.Content.Body, Subtitle: r.Content.Subtitle, Category: r.Content.Category, Silent: r.Silent || !r.Policy.SoundEnabled}
	if _, err := nativeprotocol.EncodeRequest(wire); err != nil {
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
		wire.Action = &nativeprotocol.DesktopThreadAction{Type: "desktop_thread_v1", SchemaVersion: 1, ThreadID: r.Target.ThreadID, RouteKind: "codex_thread", BundleID: "com.openai.codex", TeamID: r.Target.TeamID, ApplicationPath: r.Target.ApplicationPath, CorrelationID: r.CorrelationID}
		if _, err := nativeprotocol.EncodeRequest(wire); err != nil {
			return out
		}
	} else if nav == notification.Required {
		return finish("rejected", "navigation_unavailable")
	}
	if d.Clock == nil || d.Installation == nil || d.Process == nil || (!readOnly && d.Spool == nil) {
		return finish("rejected", "unsupported_notifier")
	}
	remaining, err := d.remaining(r)
	if err != nil || ctx.Err() != nil {
		return finish("rejected", "expired")
	}
	// The timer is also polled against the boot clock: Go's monotonic deadline
	// alone can exclude suspend on macOS. Cancellation reaches locks/probe/open.
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
				if _, err := d.remaining(r); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-stopped }()
	lease, err := d.Installation.Acquire(operation)
	if err != nil {
		if operation.Err() != nil {
			return finish("rejected", "expired")
		}
		return finish("rejected", "unsupported_notifier")
	}
	defer lease.Release()
	if _, err = d.remaining(r); err != nil || operation.Err() != nil {
		return finish("rejected", "expired")
	}
	probe, cancelProbe := context.WithTimeout(operation, time.Second)
	data, err := d.Process.Probe(probe, lease.ExecutablePath())
	probeExpired := probe.Err() != nil
	cancelProbe()
	if _, clockErr := d.remaining(r); clockErr != nil || operation.Err() != nil {
		return finish("rejected", "expired")
	}
	caps, decodeErr := nativeprotocol.DecodeCapabilities(data)
	if err != nil || probeExpired || decodeErr != nil || !caps.Supports(1, "none") {
		return finish("rejected", "unsupported_notifier")
	}
	if wire.Action != nil {
		if caps.Supports(1, "desktop_thread_v1") {
			out.Navigation = notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile", Reason: "configured_codex_desktop"}
		} else if nav == notification.Required {
			return finish("rejected", "unsupported_notifier")
		} else {
			wire.Action = nil
			out.Navigation.Reason = "unsupported_notifier"
		}
	}
	// Both paths recheck permission under a freshly acquired verified lease.
	if reason := d.permission(operation, r, lease); reason != "" {
		return finish("rejected", reason)
	}
	if readOnly {
		return finish("ready", "permission_authorized")
	}
	attempt, err := d.Spool.Prepare(operation, r, func(nonce string) ([]byte, error) { wire.Nonce = nonce; return nativeprotocol.EncodeRequest(wire) })
	if err != nil {
		if operation.Err() != nil {
			return finish("rejected", "expired")
		}
		return finish("rejected", "spool_unavailable")
	}
	if _, err = d.remaining(r); err != nil || operation.Err() != nil {
		// No launch occurred, so deletion is safe even when caller canceled early.
		_ = d.Spool.Expire(attempt)
		return finish("rejected", "expired")
	}
	handedOff, launchErr := d.Process.Launch(operation, lease.BundlePath(), attempt.RequestPath, attempt.ReceiptPath)
	if !handedOff {
		_ = d.Spool.Expire(attempt)
		return finish("rejected", "launch_failed")
	}
	// No unconditional directory defer: open's exit is not native completion.
	// Native consumes the body. Receipt/claim directories are collected on the
	// next notify/setup only after their original continuous deadline.
	read := func() bool {
		bytes, err := d.Spool.Receipt(attempt)
		if err != nil {
			return false
		}
		receipt, err := nativeprotocol.DecodeReceipt(bytes, r.CorrelationID, attempt.Nonce)
		if err != nil {
			return false
		}
		out.Status = receipt.Status
		out.Reason = receipt.Reason
		return true
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if read() {
			return out
		}
		if _, err = d.remaining(r); err != nil {
			_ = d.Spool.Expire(attempt)
			return finish("unknown", "timeout")
		}
		if operation.Err() != nil || launchErr != nil {
			return finish("unknown", "handoff_unconfirmed")
		}
		select {
		case <-operation.Done():
		case <-ticker.C:
		}
	}
}

// NewStructuredDelivery wires the real managed launcher, private spool and
// boot-continuous clock. Construction is read-only: setup must already have
// prepared spoolRoot and the trusted installation. PR4 owns policy/origin and
// durable admission before calling this port.
func NewStructuredDelivery(installation NativeInstallation, spoolRoot string) *StructuredDelivery {
	clock := SystemBootClock{}
	return &StructuredDelivery{Installation: installation, Clock: clock, Process: ManagedNativeProcess{}, Spool: &PrivateNativeSpool{Root: spoolRoot, Clock: clock}}
}
