//go:build linux || darwin

package sharedrpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

// journalDelivery is the only OS boundary in this test. It records the exact
// typed handoff without loading native helpers or touching installed profiles.
type journalDelivery struct {
	mu    sync.Mutex
	calls []notification.Request
}

func (*journalDelivery) CheckReadiness(_ context.Context, r notification.Request) notification.Readiness {
	return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "ready", Navigation: notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}}
}

func (d *journalDelivery) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	d.mu.Lock()
	d.calls = append(d.calls, r)
	d.mu.Unlock()
	out := notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "test-port", Navigation: notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}}
	if r.Target.ThreadID == "unknown-session" {
		out.Status = "unknown"
		out.Reason = "handoff_unconfirmed"
	}
	return out
}

// This checks TLS RPC -> real service -> on-disk journal. Reconstructing both
// service and daemon ensures process-local caches cannot supply durable replay.
// A scope regression duplicates or collapses effects; an unsafe unknown retry
// becomes visible as an extra delivery or changed terminal receipt.
func TestServiceJournalReplayAfterDaemonReconstructionAndSessionIsolation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	journalOptions := journal.Options{Root: root, Clock: journal.ClockFunc(func() journal.Sample {
		return journal.Sample{Boot: "test-boot", Seconds: 100, Available: true}
	})}
	if _, err := journal.Initialize(ctx, journalOptions); err != nil {
		t.Fatal(err)
	}
	delivery := &journalDelivery{}
	newService := func() *agentnotify.Service {
		t.Helper()
		store, err := journal.Open(ctx, journalOptions)
		if err != nil {
			t.Fatal(err)
		}
		service, err := agentnotify.New(agentnotify.Dependencies{
			Admission: store,
			Clock: agentnotify.ClockFunc(func() notification.Deadline {
				return notification.Deadline{BootID: "test-boot", NotAfter: 100}
			}),
			Policy: agentnotify.PolicyFunc(func(context.Context, origin.Context) (agentnotify.Policy, error) {
				return agentnotify.Policy{
					Rates:    journal.RatePolicy{SessionPerMinute: 6, RuntimePerMinute: 30, Burst: 6},
					Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true},
					Route:    origin.RoutePolicy{LocalRouting: true, AllowUnknownCaller: true, ApplicationPath: "/test-fixture/Codex.app", TeamID: "test-team"},
				}, nil
			}),
			Target: agentnotify.TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
				return origin.ResolveCodex(o, p), nil
			}),
			Readiness: delivery,
			Delivery:  delivery,
		})
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	service := newService()
	c, cancel, done := startServer(t, testOptions(service))
	shutdown := func(service *agentnotify.Service, cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		if err := awaitServer(t, done); !errors.Is(err, context.Canceled) {
			t.Fatalf("daemon shutdown: %v", err)
		}
		if err := service.Close(ctx); err != nil {
			t.Fatalf("service drain: %v", err)
		}
	}
	id := "same-explicit-request-id"
	payload := testPayload()
	payload.RequestID = &id
	first := make(map[string]agentnotify.Receipt)
	tracking := make(map[string]bool)
	for _, session := range []string{"session-A", "session-B", "unknown-session"} {
		want := "submitted"
		if session == "unknown-session" {
			want = "unknown"
		}
		r := c.Notify(ctx, payload, testOrigin(session), testDeadline())
		if r.Status != want || r.Replayed || r.RetrySafe || r.TrackingID == "" || tracking[r.TrackingID] || r.RequestID == nil || *r.RequestID != id {
			t.Fatalf("first %s: %+v", session, r)
		}
		tracking[r.TrackingID] = true
		first[session] = r
		if replay := c.Notify(ctx, payload, testOrigin(session), testDeadline()); !replay.Replayed || replay.Status != want || replay.TrackingID != r.TrackingID {
			t.Fatalf("same daemon replay %s: %+v", session, replay)
		}
	}
	shutdown(service, cancel, done)

	service = newService() // journal.Open, never Initialize, on the same test root
	c, cancel, done = startServer(t, testOptions(service))
	defer shutdown(service, cancel, done)
	for _, session := range []string{"session-A", "session-B", "unknown-session"} {
		want := first[session]
		want.Replayed = true
		if got := c.Notify(ctx, payload, testOrigin(session), testDeadline()); !reflect.DeepEqual(got, want) {
			t.Fatalf("reconstructed replay %s: got %+v; want %+v", session, got, want)
		}
	}
	changed := payload
	changed.Body = "different event under same explicit id"
	if r := c.Notify(ctx, changed, testOrigin("session-A"), testDeadline()); r.Status != "rejected" || r.Reason != "idempotency_conflict" || r.RetrySafe {
		t.Fatalf("durable conflict: %+v", r)
	}
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if len(delivery.calls) != 3 {
		t.Fatalf("replay repeated OS effects: got %d; want 3", len(delivery.calls))
	}
	for i, session := range []string{"session-A", "session-B", "unknown-session"} {
		r := delivery.calls[i]
		if r.Target.ThreadID != session || r.Target.ApplicationPath != "/test-fixture/Codex.app" || r.Target.TeamID != "test-team" || r.Deadline != testDeadline() || r.Content.Body != payload.Body || r.Navigation != notification.Required {
			t.Fatalf("scoped handoff %s changed: %+v", session, r)
		}
	}
}
