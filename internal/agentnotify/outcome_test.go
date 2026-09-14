//go:build linux || darwin

package agentnotify

import (
	"context"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/notification"
)

func TestOutcomeNavigationValidation(t *testing.T) {
	for _, phase := range []string{"readiness", "delivery"} {
		for _, kind := range []string{"missing", "malformed", "upgrade", "scope_change", "required_missing"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				f := setup(t, journal.Limits{})
				p := payload("navigation")
				p.Navigation = notification.BestEffort
				if kind == "upgrade" {
					f.policy.Delivery.ClickToFocus = false
				}
				if kind == "required_missing" {
					p.Navigation = notification.Required
				}
				bad := func(r notification.Request) notification.NavigationResult {
					switch kind {
					case "missing":
						return notification.NavigationResult{}
					case "malformed":
						return notification.NavigationResult{Capability: "unavailable", Precision: "chat_id"}
					case "upgrade":
						return notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}
					case "scope_change":
						return notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "invented_profile"}
					default:
						return notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "application_unavailable"}
					}
				}
				if phase == "readiness" {
					f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
						return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "ready", Navigation: bad(r)}
					})
				} else {
					f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
						f.effects.Add(1)
						return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "accepted", Navigation: bad(r)}
					})
				}
				a := f.notify(p, caller())
				if phase == "readiness" {
					if a.Status != "rejected" || f.effects.Load() != 0 {
						t.Fatal(a)
					}
				} else {
					b := f.notify(p, caller())
					expected := "unknown"
					if kind == "required_missing" {
						expected = "submitted"
					}
					if a.Status != expected || !b.Replayed || f.effects.Load() != 1 || a.Navigation != b.Navigation {
						t.Fatal(a, b)
					}
				}
			})
		}
	}
}

func TestOutcomePreservesAdmittedIdentity(t *testing.T) {
	for _, phase := range []string{"readiness", "delivery"} {
		t.Run(phase, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			p := payload("identity")
			p.Navigation = notification.BestEffort
			limited := notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "application_unavailable"}
			original := f.s.deps.Readiness
			if phase == "readiness" {
				f.s.deps.Readiness = readyFunc(func(c context.Context, r notification.Request) notification.Readiness {
					v := original.CheckReadiness(c, r)
					v.Navigation = limited
					return v
				})
			}
			var admitted journal.Result
			f.s.deps.Delivery = deliverFunc(func(c context.Context, r notification.Request) notification.Receipt {
				source, session := caller().Scope()
				var e error
				admitted, e = f.store.Lookup(c, journal.Key{Source: source, Session: session, Kind: journal.Explicit, Request: *p.RequestID}, digest(p))
				if e != nil {
					t.Fatal(e)
				}
				if phase == "readiness" && r.Target != (notification.DesktopTarget{}) {
					t.Fatal("unqualified action", r.Target)
				}
				f.effects.Add(1)
				return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "accepted", Backend: "fake", Navigation: limited}
			})
			a := f.notify(p, caller())
			f.policy = Policy{}
			b := f.notify(p, caller())
			source, session := caller().Scope()
			final, e := f.store.Lookup(testContext(t), journal.Key{Source: source, Session: session, Kind: journal.Explicit, Request: *p.RequestID}, digest(p))
			if e != nil || !reflect.DeepEqual(admitted.Record.Receipt.Decision, final.Record.Receipt.Decision) || final.Record.Receipt.Decision.Target.ID == "" || final.Record.Receipt.Decision.Target.Application == "" {
				t.Fatal(final, e)
			}
			b.Replayed = false
			if !reflect.DeepEqual(a, b) || a.Navigation != limited || f.effects.Load() != 1 {
				t.Fatal(a, b)
			}
		})
	}
}
