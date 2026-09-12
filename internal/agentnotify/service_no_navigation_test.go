//go:build linux || darwin

package agentnotify

import (
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
	"testing"
)

func TestNoneCallerConsent(t *testing.T) {
	for _, kind := range []string{"unknown", "asserted", "remote", "headless", "required", "best_effort"} {
		for _, allow := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-deny", true: "-allow"}[allow], func(t *testing.T) {
				f := setup(t, journal.Limits{})
				f.policy.Route = origin.RoutePolicy{AllowUnknownCaller: allow}
				o := caller()
				o.Provider = "claude"
				o.SessionID = ""
				o.AnonymousCaller = "shared_mcp"
				o.CallID = ""
				o.Locality = origin.LocalityUnknown
				p := payload("none")
				p.Navigation = notification.None
				switch kind {
				case "asserted":
					o.Provenance = origin.CallerAsserted
				case "remote":
					o.Locality = origin.Remote
				case "headless":
					o.Interface = origin.Headless
				case "required":
					o.SessionID = "qualified-session"
					p.Navigation = notification.Required
				case "best_effort":
					o.SessionID = "qualified-session"
					p.Navigation = notification.BestEffort
				}
				got := f.s.Notify(testContext(t), p, o, notification.Deadline{BootID: "boot", NotAfter: 115})
				accepted := kind == "unknown" && allow
				if !accepted {
					reason := "local_routing_unavailable"
					if kind == "remote" || kind == "headless" {
						reason = "local_gui_unavailable"
					}
					if got.Reason != reason {
						t.Fatalf("wanted %s: %+v", reason, got)
					}
				}
				if (got.Status == "submitted") != accepted || f.effects.Load() != map[bool]int64{false: 0, true: 1}[accepted] {
					t.Fatalf("%+v effects=%d", got, f.effects.Load())
				}
				if kind == "asserted" && allow {
					f.policy.Route.AllowCallerAsserted = true
					got = f.s.Notify(testContext(t), p, o, notification.Deadline{BootID: "boot", NotAfter: 115})
					if got.Status != "submitted" {
						t.Fatal(got)
					}
				}
			})
		}
	}
}
