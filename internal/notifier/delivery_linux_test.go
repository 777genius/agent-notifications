//go:build linux

package notifier

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
)

type fakeSession struct {
	ready   error
	submit  func(context.Context, notification.Request) (uint32, error)
	opens   atomic.Int32
	closes  atomic.Int32
	submits atomic.Int32
}

func (s *fakeSession) Ready(context.Context) error { return s.ready }
func (s *fakeSession) Submit(ctx context.Context, r notification.Request) (uint32, error) {
	s.submits.Add(1)
	if s.submit != nil {
		return s.submit(ctx, r)
	}
	return 1, nil
}
func (s *fakeSession) Close() error { s.closes.Add(1); return nil }

func linuxNoneRequest(clock *pr3Clock) notification.Request {
	return notification.Request{Content: notification.Content{Title: "done", Body: "literal", Category: "info"}, CorrelationID: pr3Correlation, Deadline: notification.Deadline{BootID: "test-boot", NotAfter: clock.now + 10}, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, SoundEnabled: true}, Navigation: notification.None}
}

func linuxDelivery(t *testing.T, session *fakeSession) (*FreedesktopDelivery, *pr3Clock) {
	t.Helper()
	clock := &pr3Clock{now: 100}
	d := NewFreedesktopDelivery(clock)
	d.Open = func(context.Context) (sessionNotifications, error) { session.opens.Add(1); return session, nil }
	return d, clock
}

func withFatalLinuxBeeep(t *testing.T) {
	t.Helper()
	previous := beeepNotify
	beeepNotify = func(string, string, any) error { t.Fatal("beeep fallback after D-Bus"); return nil }
	t.Cleanup(func() { beeepNotify = previous })
}

func TestFreedesktopNavigationNoneSubmitsWithoutBeeep(t *testing.T) {
	withFatalLinuxBeeep(t)
	session := &fakeSession{}
	d, clock := linuxDelivery(t, session)
	req := linuxNoneRequest(clock)
	ready := d.CheckReadiness(context.Background(), req)
	if ready.Status != "ready" || ready.Reason != "permission_authorized" || ready.Backend != linuxFreedesktopBackend || ready.Navigation.Capability != "disabled" {
		t.Fatal(ready)
	}
	got := d.Deliver(context.Background(), req)
	if got.Status != "submitted" || got.Reason != "session_notification" || session.submits.Load() != 1 || session.closes.Load() != 2 {
		t.Fatal(got, session.submits.Load(), session.closes.Load())
	}
}

func TestFreedesktopSubmitTimeoutIsUnknownWithoutBeeep(t *testing.T) {
	withFatalLinuxBeeep(t)
	started := make(chan struct{})
	session := &fakeSession{submit: func(ctx context.Context, _ notification.Request) (uint32, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	}}
	d, clock := linuxDelivery(t, session)
	req := linuxNoneRequest(clock)
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

func TestFreedesktopMissingSessionDoesNotCallBeeep(t *testing.T) {
	withFatalLinuxBeeep(t)
	clock := &pr3Clock{now: 100}
	d := NewFreedesktopDelivery(clock)
	d.Open = func(context.Context) (sessionNotifications, error) { return nil, errors.New("no session bus") }
	got := d.Deliver(context.Background(), linuxNoneRequest(clock))
	if got.Status != "rejected" || got.Reason != "unsupported_notifier" {
		t.Fatal(got)
	}
}
