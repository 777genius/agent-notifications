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
	if got.Status != "unknown" || got.Reason != "handoff_unconfirmed" {
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
