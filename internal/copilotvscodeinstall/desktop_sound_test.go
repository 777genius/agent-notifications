package copilotvscodeinstall

import (
	"context"
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
)

type soundPolicySink struct {
	calls int
	last  notification.Request
}

func (s *soundPolicySink) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	s.calls++
	s.last = r
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted"}
}

// Both installed Cursor and internal Copilot VS Code delivery pass through
// localDesktop before their native/leased effect ports. The bridge must never
// reject a legitimately opted-in cue or accept a navigable/unconsented request.
func TestLocalDesktopNativeSoundAndNavigationGate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		silent     bool
		sound      bool
		navigation notification.Navigation
		click      bool
		target     notification.DesktopTarget
		wantSubmit bool
	}{
		{name: "original silent", silent: true, navigation: notification.None, wantSubmit: true},
		{name: "explicit OS sound", silent: false, sound: true, navigation: notification.None, wantSubmit: true},
		{name: "sound permitted but muted", silent: true, sound: true, navigation: notification.None, wantSubmit: true},
		{name: "audible without opt-in", silent: false, navigation: notification.None},
		{name: "required navigation", silent: true, navigation: notification.Required},
		{name: "click flag despite None", silent: true, navigation: notification.None, click: true},
		{name: "hidden desktop target", silent: true, navigation: notification.None, target: notification.DesktopTarget{ThreadID: "PRIVATE-foreign"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &soundPolicySink{}
			p := localDesktop{backend: sink}
			r := notification.Request{
				CorrelationID: "TEST-correlation",
				Content:       notification.Content{Title: "TEST", Body: "stop", Category: "info"},
				Navigation:    tc.navigation, Target: tc.target, Silent: tc.silent,
				Policy: notification.PolicySnapshot{
					Valid: true, ExplicitEnabled: true, DesktopEnabled: true,
					ClickToFocus: tc.click, SoundEnabled: tc.sound,
				},
			}
			got := p.Deliver(context.Background(), r)
			if tc.wantSubmit {
				if got.Status != "submitted" || sink.calls != 1 || sink.last != r {
					t.Fatalf("installed wrapper lost native delivery: receipt=%+v calls=%d", got, sink.calls)
				}
			} else if got.Status != "rejected" || got.Reason != "local_route_unavailable" || sink.calls != 0 {
				t.Fatalf("installed wrapper accepted unsafe request: receipt=%+v calls=%d", got, sink.calls)
			}
		})
	}
}

func TestLocalDesktopRejectsDisabledPolicyOrMissingBackend(t *testing.T) {
	allowed := notification.Request{
		CorrelationID: "TEST", Navigation: notification.None, Silent: true,
		Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true},
	}
	for _, change := range []func(*notification.Request){
		func(r *notification.Request) { r.Policy.Valid = false },
		func(r *notification.Request) { r.Policy.ExplicitEnabled = false },
		func(r *notification.Request) { r.Policy.DesktopEnabled = false },
	} {
		sink := &soundPolicySink{}
		r := allowed
		change(&r)
		if got := (localDesktop{backend: sink}).Deliver(context.Background(), r); got.Status != "rejected" || sink.calls != 0 {
			t.Fatalf("disabled consent reached native backend: %+v calls=%d", got, sink.calls)
		}
	}
	if got := (localDesktop{}).Deliver(context.Background(), allowed); got.Status != "rejected" {
		t.Fatalf("missing native backend accepted delivery: %+v", got)
	}
}
