package notifier

import (
	"context"
	"errors"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/windowscallback"
	"testing"
)

func boundWindowsRequest(clock *pr3Clock) notification.Request {
	r := windowsNoneRequest(clock)
	r.Navigation = notification.Required
	r.Policy.ClickToFocus = true
	r.Target = notification.DesktopTarget{Provider: "codex", ThreadID: "TEST/opaque%id", Windows: notification.WindowsBinding{SnapshotPath: `\\?\C:\TEST\generation.wne`, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	return r
}

// Breakage: None reaches bound asset/native readiness, or a failed bound
// admission reports chat_id capability despite no installed readiness proof.
func TestWindowsBoundAdmissionAndNone(t *testing.T) {
	informational := &fakeToastSession{}
	d, clock := windowsDelivery(t, informational)
	calls := 0
	d.OpenNavigation = func(context.Context, notification.Request) (windowsToastSession, error) {
		calls++
		return nil, errors.New("unavailable")
	}
	r := boundWindowsRequest(clock)
	r.Navigation = notification.None
	if got := d.Deliver(context.Background(), r); got.Status != "submitted" || calls != 0 || informational.submits.Load() != 1 {
		t.Fatal(got, calls)
	}
	r.Navigation = notification.Required
	if got := d.CheckReadiness(context.Background(), r); got.Status != "rejected" || got.Navigation.Capability != "unavailable" || got.Navigation.Precision != "none" || calls != 1 {
		t.Fatal(got, calls)
	}
}

// Breakage: possible Show uncertainty triggers a second informational effect,
// or Open captures one immutable binding but Submit receives another target.
func TestWindowsUnknownDoesNotFallbackAndCarriesBinding(t *testing.T) {
	informational := &fakeToastSession{}
	d, clock := windowsDelivery(t, informational)
	r := boundWindowsRequest(clock)
	r.Navigation = notification.BestEffort
	native := &fakeToastSession{submit: func(_ context.Context, got notification.Request) error {
		if got.Target != r.Target {
			t.Fatal("binding changed", got.Target)
		}
		return windowscallback.ErrUnknown
	}}
	d.OpenNavigation = func(_ context.Context, got notification.Request) (windowsToastSession, error) {
		if got.Target != r.Target {
			t.Fatal("capture changed", got.Target)
		}
		return native, nil
	}
	got := d.Deliver(context.Background(), r)
	if got.Status != "unknown" || native.submits.Load() != 1 || informational.opens.Load() != 0 || informational.submits.Load() != 0 {
		t.Fatal(got, native.submits.Load())
	}
}

// Breakage: a definitive native OS-disabled observation permits a second Show
// under the informational AUMID. Actual adapter composition must block it,
// while a genuinely unavailable pre-effect navigation route may fall back.
func TestWindowsDisabledBlocksBestEffortFallback(t *testing.T) {
	for _, tc := range []struct {
		reason  error
		status  string
		effects int32
	}{
		{windowscallback.ErrDisabled, "rejected", 0},
		{windowscallback.ErrUnavailable, "submitted", 1},
	} {
		informational := &fakeToastSession{}
		d, clock := windowsDelivery(t, informational)
		native := &fakeToastSession{ready: tc.reason}
		d.OpenNavigation = func(context.Context, notification.Request) (windowsToastSession, error) { return native, nil }
		r := boundWindowsRequest(clock)
		r.Navigation = notification.BestEffort
		got := d.Deliver(context.Background(), r)
		if got.Status != tc.status || informational.opens.Load() != tc.effects || informational.submits.Load() != tc.effects || native.submits.Load() != 0 {
			t.Fatal(got, informational.opens.Load(), informational.submits.Load())
		}
		if tc.reason == windowscallback.ErrDisabled && got.Reason != "permission_disabled" {
			t.Fatal(got)
		}
	}
}
