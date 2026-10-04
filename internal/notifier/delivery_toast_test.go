package notifier

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
)

type fakeToastSession struct {
	ready   error
	submit  func(context.Context, notification.Request) error
	opens   atomic.Int32
	closes  atomic.Int32
	submits atomic.Int32
}

func (s *fakeToastSession) Ready(context.Context) error { return s.ready }
func (s *fakeToastSession) Submit(ctx context.Context, r notification.Request) error {
	s.submits.Add(1)
	if s.submit != nil {
		return s.submit(ctx, r)
	}
	return nil
}
func (s *fakeToastSession) Close() error {
	s.closes.Add(1)
	return nil
}

func windowsNoneRequest(clock *pr3Clock) notification.Request {
	return notification.Request{
		Content:       notification.Content{Title: "done", Body: "literal", Category: "info"},
		CorrelationID: pr3Correlation,
		Deadline:      notification.Deadline{BootID: "test-boot", NotAfter: clock.now + 10},
		Policy:        notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, SoundEnabled: true},
		Navigation:    notification.None,
	}
}

func windowsDelivery(t *testing.T, session *fakeToastSession) (*WindowsToastDelivery, *pr3Clock) {
	t.Helper()
	clock := &pr3Clock{now: 100}
	d := NewWindowsToastDelivery(clock)
	d.Open = func(context.Context) (windowsToastSession, error) {
		session.opens.Add(1)
		return session, nil
	}
	return d, clock
}

func withFatalBeeep(t *testing.T) {
	t.Helper()
	previous := beeepNotify
	beeepNotify = func(string, string, any) error {
		t.Fatal("beeep fallback after toast")
		return nil
	}
	t.Cleanup(func() { beeepNotify = previous })
}

func TestWindowsToastNavigationNoneSubmitsWithoutBeeep(t *testing.T) {
	withFatalBeeep(t)
	session := &fakeToastSession{}
	d, clock := windowsDelivery(t, session)
	ctx := context.Background()
	req := windowsNoneRequest(clock)
	ready := d.CheckReadiness(ctx, req)
	if ready.Status != "ready" || ready.Reason != "permission_authorized" || ready.Backend != windowsToastBackend || ready.Navigation.Capability != "disabled" {
		t.Fatal(ready)
	}
	got := d.Deliver(ctx, req)
	if got.Status != "submitted" || got.Reason != "session_notification" || session.submits.Load() != 1 || session.closes.Load() != 2 {
		t.Fatal(got, session.submits.Load(), session.closes.Load())
	}
}

func TestWindowsToastRequiredNavigationDoesNotOpenSession(t *testing.T) {
	withFatalBeeep(t)
	session := &fakeToastSession{}
	d, clock := windowsDelivery(t, session)
	req := windowsNoneRequest(clock)
	req.Navigation = notification.Required
	req.Policy.ClickToFocus = true
	got := d.CheckReadiness(context.Background(), req)
	if got.Status != "rejected" || got.Reason != "navigation_unavailable" || session.opens.Load() != 0 {
		t.Fatal(got, session.opens.Load())
	}
}

func TestWindowsToastSubmitTimeoutIsUnknownWithoutBeeep(t *testing.T) {
	withFatalBeeep(t)
	started := make(chan struct{})
	session := &fakeToastSession{submit: func(ctx context.Context, _ notification.Request) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	d, clock := windowsDelivery(t, session)
	clock.set(100)
	req := windowsNoneRequest(clock)
	req.Deadline.NotAfter = 100.2
	got := d.Deliver(context.Background(), req)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("submit not started")
	}
	if got.Status != "unknown" || got.Reason != "native_submission_deadline" || got.RetrySafe || session.submits.Load() != 1 || session.closes.Load() != 1 {
		t.Fatal(got)
	}
}

func TestWindowsToastMissingSessionDoesNotCallBeeep(t *testing.T) {
	withFatalBeeep(t)
	clock := &pr3Clock{now: 100}
	d := NewWindowsToastDelivery(clock)
	d.Open = func(context.Context) (windowsToastSession, error) {
		return nil, errors.New("no toast session")
	}
	got := d.Deliver(context.Background(), windowsNoneRequest(clock))
	if got.Status != "rejected" || got.Reason != "unsupported_notifier" {
		t.Fatal(got)
	}
}

func TestWindowsToastReadyFailureDoesNotSubmit(t *testing.T) {
	withFatalBeeep(t)
	session := &fakeToastSession{ready: errors.New("notifications unavailable")}
	d, clock := windowsDelivery(t, session)
	got := d.Deliver(context.Background(), windowsNoneRequest(clock))
	if got.Status != "rejected" || got.Reason != "unsupported_notifier" || session.submits.Load() != 0 {
		t.Fatal(got, session.submits.Load())
	}
}

// Actual standard context cancellation and returned error chains retain unknown
// effect semantics. Private error text is never used as a reason; no fallback.
func TestWindowsToastSubmitCancellationClassificationPreservesUnknown(t *testing.T) {
	withFatalBeeep(t)
	for _, tc := range []struct {
		name, reason string
		err          error
		cancelParent bool
	}{
		{"returned deadline", "native_submission_deadline", context.DeadlineExceeded, false},
		{"returned cancellation", "native_submission_cancelled", context.Canceled, false},
		{"parent cancellation takes precedence", "native_submission_cancelled", context.DeadlineExceeded, true},
		{"joined errors prefer deadline", "native_submission_deadline", errors.Join(context.Canceled, context.DeadlineExceeded), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			session := &fakeToastSession{submit: func(ctx context.Context, _ notification.Request) error {
				if tc.cancelParent {
					cancel()
					<-ctx.Done()
				} else if ctx.Err() != nil {
					t.Fatal("submission context already expired")
				}
				return errors.Join(errors.New("PRIVATE_SUBMIT_ERROR_SENTINEL"), tc.err)
			}}
			d, clock := windowsDelivery(t, session)
			got := d.Deliver(parent, windowsNoneRequest(clock))
			if got.Status != "unknown" || got.Reason != tc.reason || got.RetrySafe || session.opens.Load() != 1 || session.submits.Load() != 1 || session.closes.Load() != 1 {
				t.Fatal("classification changed effect/lifetime or lost finite reason", got)
			}
		})
	}
}
