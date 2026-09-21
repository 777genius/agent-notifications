//go:build linux || darwin

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

type readyFunc func(context.Context, notification.Request) notification.Readiness

func (f readyFunc) CheckReadiness(c context.Context, r notification.Request) notification.Readiness {
	return f(c, r)
}

type deliverFunc func(context.Context, notification.Request) notification.Receipt

func (f deliverFunc) Deliver(c context.Context, r notification.Request) notification.Receipt {
	return f(c, r)
}
func TestActualServiceJournalReplayConflictUnknown(t *testing.T) {
	for _, status := range []string{"submitted", "unknown"} {
		t.Run(status, func(t *testing.T) {
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Chmod(root, 0700); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			jo := journal.Options{Root: root, Clock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "boot", Seconds: 100, Available: true} })}
			store, e := journal.Initialize(ctx, jo)
			if e != nil {
				t.Fatal(e)
			}
			effects := 0
			makeService := func(store *journal.Store) *agentnotify.Service {
				s, e := agentnotify.New(agentnotify.Dependencies{Admission: store, Clock: clock(), Policy: agentnotify.PolicyFunc(func(context.Context, origin.Context) (agentnotify.Policy, error) {
					return agentnotify.Policy{Route: origin.RoutePolicy{LocalRouting: true, AllowUnknownCaller: true, AllowCallerAsserted: true}, Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}}, nil
				}), Target: agentnotify.TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
					return origin.ResolveCodex(o, p), nil
				}), Readiness: readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
					return notification.Readiness{Status: "ready", Reason: "ready", CorrelationID: r.CorrelationID, Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "no_action"}}
				}), Delivery: deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
					effects++
					if r.Content.Title != "--help $(x)" || r.Content.Body != "private-body\n\t" || r.Deadline.NotAfter != 115 {
						t.Fatal(r)
					}
					return notification.Receipt{Status: status, Reason: "fake_outcome", Backend: "fake", CorrelationID: r.CorrelationID, Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "no_action"}}
				})})
				if e != nil {
					t.Fatal(e)
				}
				return s
			}
			s := makeService(store)
			o := Options{Clock: clock(), Backend: s}
			n, first, _ := execute(t, good, nil, o)
			want := ExitSuccess
			if status == "unknown" {
				want = ExitUnknown
			}
			if n != want || first.Status != status || effects != 1 {
				t.Fatal(n, first, effects)
			}
			// Same service remains live across Run calls. Replay also survives reopening
			// the actual on-disk journal and constructing a separate service instance.
			_, again, _ := execute(t, good, nil, o)
			if !again.Replayed || effects != 1 {
				t.Fatal(again, effects)
			}
			reopened, e := journal.Open(ctx, jo)
			if e != nil {
				t.Fatal(e)
			}
			o.Backend = makeService(reopened)
			_, again, _ = execute(t, good, nil, o)
			if !again.Replayed || again.TrackingID != first.TrackingID || effects != 1 || (status == "unknown" && again.RetrySafe) {
				t.Fatal(again, effects)
			}
			n, conflict, _ := execute(t, strings.Replace(good, "private-body", "different", 1), nil, o)
			if n != ExitRejected || conflict.Reason != "idempotency_conflict" || effects != 1 {
				t.Fatal(n, conflict, effects)
			}
		})
	}
}

func TestSecureEnvelopeActualServiceRoutingPolicy(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	contextPath := filepath.Join(root, "context.json")
	if e = os.WriteFile(contextPath, []byte(`{"provider":"codex","session":"explicit-session","locality":"local","interface":"desktop"}`), 0600); e != nil {
		t.Fatal(e)
	}
	journalRoot := filepath.Join(root, "journal")
	if e = os.Mkdir(journalRoot, 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, e := journal.Initialize(ctx, journal.Options{Root: journalRoot, Clock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "boot", Seconds: 100, Available: true} })})
	if e != nil {
		t.Fatal(e)
	}
	allow := false
	effects := 0
	navigation := notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}
	s, e := agentnotify.New(agentnotify.Dependencies{Admission: store, Clock: clock(), Policy: agentnotify.PolicyFunc(func(_ context.Context, o origin.Context) (agentnotify.Policy, error) {
		if o.Provenance != origin.CallerAsserted || o.SessionID != "explicit-session" || o.Namespace != "cli" {
			t.Fatal(o)
		}
		return agentnotify.Policy{Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true}, Route: origin.RoutePolicy{LocalRouting: true, AllowCallerAsserted: allow, ApplicationPath: "/fake/Application.app", TeamID: "fake-team"}}, nil
	}), Target: agentnotify.TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
		return origin.ResolveCodex(o, p), nil
	}), Readiness: readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		return notification.Readiness{Status: "ready", Reason: "ready", CorrelationID: r.CorrelationID, Navigation: navigation}
	}), Delivery: deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
		effects++
		if r.Target.ThreadID != "explicit-session" || r.Target.ApplicationPath != "/fake/Application.app" {
			t.Fatal(r.Target)
		}
		return notification.Receipt{Status: "submitted", Reason: "fake_accepted", Backend: "fake", CorrelationID: r.CorrelationID, Navigation: navigation}
	})})
	if e != nil {
		t.Fatal(e)
	}
	o := Options{Backend: s, Clock: clock()}
	raw := strings.Replace(good, `"none"`, `"required"`, 1)
	args := []string{"--context-file", contextPath}
	n, r, _ := execute(t, raw, args, o)
	if n != ExitRejected || r.Reason != "local_routing_unavailable" || effects != 0 {
		t.Fatal(n, r, effects)
	}
	allow = true
	n, r, _ = execute(t, raw, args, o)
	if n != ExitSuccess || effects != 1 || r.Navigation.Precision != "chat_id" {
		t.Fatal(n, r, effects)
	}
}
