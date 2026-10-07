//go:build linux || darwin

package agentnotify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

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

type fixture struct {
	s        *Service
	store    *journal.Store
	root     string
	now      atomic.Int64
	effects  atomic.Int64
	loads    atomic.Int64
	policy   Policy
	requests []notification.Request
	mu       sync.Mutex
}

func setup(t *testing.T, limits journal.Limits) *fixture {
	t.Helper()
	f := &fixture{}
	f.now.Store(100)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	f.root = root
	f.store, e = journal.Initialize(testContext(t), journal.Options{Root: root, Limits: limits, Clock: journal.ClockFunc(func() journal.Sample {
		return journal.Sample{Boot: "boot", Seconds: uint64(f.now.Load()), Available: true}
	})})
	if e != nil {
		t.Fatal(e)
	}
	f.policy = Policy{Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true, SoundEnabled: true}, Route: origin.RoutePolicy{LocalRouting: true, AllowUnknownCaller: true, AllowCallerAsserted: true, ApplicationPath: "/test/A.app", TeamID: "team-A"}}
	f.s, e = New(Dependencies{Admission: f.store, Clock: ClockFunc(func() notification.Deadline {
		return notification.Deadline{BootID: "boot", NotAfter: float64(f.now.Load())}
	}), Policy: PolicyFunc(func(context.Context, origin.Context) (Policy, error) { f.loads.Add(1); return f.policy, nil }), Target: TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
		return origin.ResolveCodex(o, p), nil
	}), Readiness: readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
	}), Delivery: deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
		f.effects.Add(1)
		f.mu.Lock()
		f.requests = append(f.requests, r)
		f.mu.Unlock()
		return notification.Receipt{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "fake"}
	})})
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func caller() origin.Context {
	return origin.Context{Provider: "codex", Namespace: "local", SessionID: "session-A", CallID: "call-1", Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.InterfaceUnknown}
}
func payload(id string) Payload {
	return Payload{Title: "literal --help [important]", Body: "body-private-sentinel", Category: "attention", RequestID: &id}
}
func (f *fixture) notify(p Payload, o origin.Context) Receipt {
	return f.s.Notify(context.Background(), p, o, notification.Deadline{BootID: "boot", NotAfter: float64(f.now.Load() + 15)})
}
func requireStatus(t *testing.T, r Receipt, status string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("got %+v, want %s", r, status)
	}
}
func (f *fixture) advance() { f.now.Add(62) }

func TestKeyPriorityScopeConflictAndReplay(t *testing.T) {
	f := setup(t, journal.Limits{})
	o := caller()
	p := payload("R")
	first := f.notify(p, o)
	requireStatus(t, first, "submitted")
	if first.RequestID == nil || *first.RequestID != "R" || first.KeyKind != journal.Explicit || len(first.TrackingID) != 64 {
		t.Fatal(first)
	}
	o.CallID = "different-call"
	again := f.notify(p, o)
	if !again.Replayed || again.TrackingID != first.TrackingID || f.effects.Load() != 1 {
		t.Fatal(again)
	}
	p.Body += "changed"
	conflict := f.notify(p, o)
	if conflict.Reason != "idempotency_conflict" || f.effects.Load() != 1 {
		t.Fatal(conflict)
	}
	// Same cwd is deliberately irrelevant: neither public input has a cwd field.
	f.advance()
	p = payload("R")
	o.SessionID = "session-B"
	requireStatus(t, f.notify(p, o), "submitted")
	f.advance()
	o.Provider = "claude"
	p.Navigation = notification.None
	requireStatus(t, f.notify(p, o), "submitted")
	f.advance()
	o = caller()
	requireStatus(t, f.notify(payload("R2"), o), "submitted")
	if f.effects.Load() != 4 {
		t.Fatal(f.effects.Load())
	}
}
func TestCallIDGeneratedAndAnonymousNamespaces(t *testing.T) {
	f := setup(t, journal.Limits{})
	o := caller()
	p := payload("")
	p.RequestID = nil
	a := f.notify(p, o)
	requireStatus(t, a, "submitted")
	if a.RequestID != nil || a.KeyKind != journal.ClientCall {
		t.Fatal(a)
	}
	if b := f.notify(p, o); !b.Replayed || b.TrackingID != a.TrackingID {
		t.Fatal(b)
	}
	f.advance()
	o.CallID = ""
	a = f.notify(p, o)
	f.advance()
	b := f.notify(p, o)
	if a.KeyKind != journal.Generated || a.RequestID != nil || b.TrackingID == a.TrackingID || b.Replayed {
		t.Fatal(a, b)
	}
	f.advance()
	o.SessionID = ""
	o.AnonymousCaller = "session-A"
	p.Navigation = notification.None
	requireStatus(t, f.notify(p, o), "submitted")
	f.mu.Lock()
	last := f.requests[len(f.requests)-1]
	f.mu.Unlock()
	if last.Target != (notification.DesktopTarget{}) {
		t.Fatal(last)
	}
	sa, aa := o.Scope()
	o.SessionID = "session-A"
	sb, ab := o.Scope()
	if sa != sb || aa == ab {
		t.Fatal("anonymous/session collision")
	}
}
func TestPreflightDoesNotConsumeKeyAndReplaySkipsMutablePorts(t *testing.T) {
	f := setup(t, journal.Limits{})
	p := payload("R")
	o := caller()
	f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "permission_denied", Status: "rejected"}
	})
	if r := f.notify(p, o); r.Reason != "permission_denied" || f.effects.Load() != 0 {
		t.Fatal(r)
	}
	f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
	})
	a := f.notify(p, o)
	requireStatus(t, a, "submitted")
	f.policy = Policy{}
	f.s.deps.Target = TargetFunc(func(context.Context, origin.Context, origin.RoutePolicy) (origin.Target, error) {
		t.Error("replay resolved target")
		return origin.Target{}, nil
	})
	f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		t.Error("replay probed readiness")
		return notification.Readiness{}
	})
	b := f.notify(p, o)
	if !b.Replayed || !reflect.DeepEqual(a.Navigation, b.Navigation) || a.TrackingID != b.TrackingID || f.loads.Load() != 2 {
		t.Fatal(a, b)
	}
	source, session := o.Scope()
	r, e := f.store.Lookup(testContext(t), journal.Key{Source: source, Session: session, Kind: journal.Explicit, Request: "R"}, digest(Payload{Title: p.Title, Body: p.Body, Category: p.Category, Navigation: notification.Required}))
	if e != nil || r.Record.Receipt.Decision.Target.Application != "/test/A.app" {
		t.Fatal(r, e)
	}
}

func waitSignal(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("barrier timed out")
	}
}
func TestConcurrentAdmitLoserUsesStoredDecision(t *testing.T) {
	f := setup(t, journal.Limits{})
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var count atomic.Int64
	f.s.deps.Policy = PolicyFunc(func(context.Context, origin.Context) (Policy, error) {
		p := f.policy
		if count.Add(1) == 2 {
			p.Route.ApplicationPath = "/test/B.app"
		}
		return p, nil
	})
	f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		arrived <- struct{}{}
		<-release
		return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
	})
	done := make(chan Receipt, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- f.notify(payload("same"), caller()) }()
	}
	waitSignal(t, arrived)
	waitSignal(t, arrived)
	close(release)
	a, b := <-done, <-done
	if f.effects.Load() != 1 || a.TrackingID != b.TrackingID || a.Replayed == b.Replayed || !reflect.DeepEqual(a.Navigation, b.Navigation) {
		t.Fatal(a, b, f.effects.Load())
	}
	// The loser can observe pending or terminal; it never waits for delivery.
	if a.Status != "submitted" && b.Status != "submitted" {
		t.Fatal(a, b)
	}
}
func TestBusyNoQueueReplayWhileFullAndCloseDrain(t *testing.T) {
	f := setup(t, journal.Limits{})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int64
	f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return notification.Receipt{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted"}
	})
	done := make(chan Receipt, 2)
	go func() { done <- f.notify(payload("one"), caller()) }()
	waitSignal(t, entered)
	go func() { done <- f.notify(payload("two"), caller()) }()
	waitSignal(t, entered)
	if r := f.notify(payload("three"), caller()); r.Reason != "busy" {
		t.Fatal(r)
	}
	if r := f.notify(payload("one"), caller()); !r.Replayed || r.Status != "unknown" || r.RetrySafe {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := f.s.Close(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if r := f.notify(payload("four"), caller()); r.Reason != "service_closed" {
		t.Fatal(r)
	}
	close(release)
	requireStatus(t, <-done, "submitted")
	requireStatus(t, <-done, "submitted")
	if e := f.s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := f.s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("queued effect", calls.Load())
	}
}

type admissionWrap struct {
	AdmissionPort
	beforeAdmit   func(context.Context)
	afterAdmit    func()
	finalizeError bool
}

func (w admissionWrap) Admit(c context.Context, a journal.Admission) (journal.Result, error) {
	if w.beforeAdmit != nil {
		w.beforeAdmit(c)
	}
	r, e := w.AdmissionPort.Admit(c, a)
	if w.afterAdmit != nil {
		w.afterAdmit()
	}
	return r, e
}
func (w admissionWrap) FinalizeOutcome(c context.Context, k journal.Key, a, s, r, b string, n *journal.Navigation) error {
	if w.finalizeError {
		return errors.New("injected finalize fault")
	}
	return w.AdmissionPort.FinalizeOutcome(c, k, a, s, r, b, n)
}
func TestReadinessLeaseReleasedBeforeAdmissionAndDeliveryOutsideJournal(t *testing.T) {
	f := setup(t, journal.Limits{})
	held := atomic.Bool{}
	entered := make(chan struct{})
	release := make(chan struct{})
	f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
		held.Store(true)
		close(entered)
		<-release
		held.Store(false)
		return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
	})
	f.s.deps.Admission = admissionWrap{AdmissionPort: f.store, beforeAdmit: func(context.Context) {
		if held.Load() {
			t.Error("lease nested with journal admission")
		}
	}}
	f.s.deps.Delivery = deliverFunc(func(c context.Context, r notification.Request) notification.Receipt {
		// A reentrant actual journal Lookup would time out if delivery held its lock.
		source, session := caller().Scope()
		p, _ := validate(payload("R"), caller())
		q, e := f.store.Lookup(c, journal.Key{Source: source, Session: session, Kind: journal.Explicit, Request: "R"}, digest(p))
		if e != nil || !q.Found {
			t.Error(q, e)
		}
		return notification.Receipt{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted"}
	})
	done := make(chan Receipt, 1)
	go func() { done <- f.notify(payload("R"), caller()) }()
	waitSignal(t, entered)
	entries, e := os.ReadDir(f.root)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		b, e := os.ReadFile(filepath.Join(f.root, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(b), "dispatching") {
			t.Fatal("admitted while readiness lease held")
		}
	}
	close(release)
	requireStatus(t, <-done, "submitted")
}

func TestCancellationAndDeadlineBoundaries(t *testing.T) {
	for _, point := range []string{"before", "policy", "readiness", "admit_wait", "after_admit", "delivery", "late_accepted", "expired", "boot", "missing", "too_long"} {
		t.Run(point, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deadline := notification.Deadline{BootID: "boot", NotAfter: 115}
			switch point {
			case "before":
				cancel()
			case "policy":
				f.s.deps.Policy = PolicyFunc(func(context.Context, origin.Context) (Policy, error) { cancel(); return f.policy, nil })
			case "readiness":
				f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
					f.now.Store(116)
					return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
				})
			case "admit_wait":
				f.s.deps.Admission = admissionWrap{AdmissionPort: f.store, beforeAdmit: func(context.Context) { cancel() }}
			case "after_admit":
				f.s.deps.Admission = admissionWrap{AdmissionPort: f.store, afterAdmit: cancel}
			case "delivery", "late_accepted":
				f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
					f.effects.Add(1)
					cancel()
					status, reason := "unknown", "handoff_canceled"
					if point == "late_accepted" {
						status, reason = "submitted", "os_accepted"
					}
					return notification.Receipt{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Status: status, Reason: reason}
				})
			case "expired":
				deadline.NotAfter = 99
			case "boot":
				deadline.BootID = "other"
			case "missing":
				deadline = notification.Deadline{}
			case "too_long":
				deadline.NotAfter = 116
			}
			r := f.s.Notify(ctx, payload("R"), caller(), deadline)
			switch point {
			case "late_accepted":
				requireStatus(t, r, "submitted")
			case "delivery", "after_admit":
				requireStatus(t, r, "unknown")
			default:
				requireStatus(t, r, "rejected")
			}
			if point != "delivery" && point != "late_accepted" && f.effects.Load() != 0 {
				t.Fatal(r)
			}
			if point == "after_admit" || point == "delivery" || point == "late_accepted" {
				again := f.notify(payload("R"), caller())
				if !again.Replayed || again.RetrySafe {
					t.Fatal(again)
				}
			}
		})
	}
}
func TestTerminalAndFinalizeFailureNeverResend(t *testing.T) {
	for _, status := range []string{"submitted", "unknown", "rejected", "suppressed", "bad_correlation", "finalize_failure"} {
		t.Run(status, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			f.s.deps.Delivery = deliverFunc(func(_ context.Context, r notification.Request) notification.Receipt {
				if status != "suppressed" {
					f.effects.Add(1)
				}
				id, s := r.CorrelationID, status
				if status == "bad_correlation" {
					id = "wrong"
					s = "submitted"
				}
				if status == "finalize_failure" {
					s = "submitted"
				}
				return notification.Receipt{Navigation: fakeNavigation(r), CorrelationID: id, Status: s, Reason: "test_result"}
			})
			if status == "finalize_failure" {
				f.s.deps.Admission = admissionWrap{AdmissionPort: f.store, finalizeError: true}
			}
			a := f.notify(payload("R"), caller())
			b := f.notify(payload("R"), caller())
			wantEffects := int64(1)
			if status == "suppressed" {
				wantEffects = 0
				requireStatus(t, a, "suppressed")
				requireStatus(t, b, "suppressed")
			}
			if !b.Replayed || f.effects.Load() != wantEffects || b.TrackingID != a.TrackingID || a.RetrySafe || b.RetrySafe {
				t.Fatal(a, b)
			}
			if status == "finalize_failure" {
				if a.Reason != "finalization_failed" || b.Reason != "pending_submission" {
					t.Fatal(a, b)
				}
			}
			if status == "bad_correlation" || status == "finalize_failure" {
				requireStatus(t, a, "unknown")
			}
		})
	}
}
func TestPayloadAbsentFromStoreAndNativeUUID(t *testing.T) {
	f := setup(t, journal.Limits{})
	p := payload("R")
	requireStatus(t, f.notify(p, caller()), "submitted")
	entries, e := os.ReadDir(f.root)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		b, e := os.ReadFile(filepath.Join(f.root, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{p.Title, p.Body, "client_metadata", "call-1"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("stored forbidden field %q", secret)
			}
		}
	}
	id := f.requests[0].CorrelationID
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-8[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatal(id)
	}
	first := correlationID("key", "attempt")
	if first != correlationID("key", "attempt") || first == correlationID("key", "other") {
		t.Fatal("correlation derivation")
	}
	if f.requests[0].Deadline.NotAfter != 115 || f.requests[0].Content.Title != p.Title || f.requests[0].Content.Body != p.Body {
		t.Fatal(f.requests[0])
	}
}
func TestFailClosedStoreAndRates(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		f := setup(t, journal.Limits{Records: 1})
		requireStatus(t, f.notify(payload("one"), caller()), "submitted")
		f.advance()
		r := f.notify(payload("two"), caller())
		if r.Reason != "journal_full" || f.effects.Load() != 1 {
			t.Fatal(r)
		}
	})
	t.Run("rates", func(t *testing.T) {
		f := setup(t, journal.Limits{})
		for _, id := range []string{"1", "2", "3"} {
			requireStatus(t, f.notify(payload(id), caller()), "submitted")
		}
		r := f.notify(payload("4"), caller())
		if r.Reason != "rate_limited" || f.effects.Load() != 3 {
			t.Fatal(r)
		}
		if r = f.notify(payload("1"), caller()); !r.Replayed {
			t.Fatal(r)
		}
	})
	for _, mode := range []string{"missing", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			entries, e := os.ReadDir(f.root)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					p := filepath.Join(f.root, entry.Name())
					var e error
					if mode == "missing" {
						e = os.Remove(p)
					} else {
						e = os.WriteFile(p, []byte("bad"), 0600)
					}
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			before, e := os.ReadDir(f.root)
			if e != nil {
				t.Fatal(e)
			}
			r := f.notify(payload("R"), caller())
			requireStatus(t, r, "rejected")
			after, e := os.ReadDir(f.root)
			if e != nil {
				t.Fatal(e)
			}
			if len(before) != len(after) || f.effects.Load() != 0 {
				t.Fatal(r)
			}
			if f.loads.Load() != 0 {
				t.Fatal("policy loaded before failing journal")
			}
		})
	}
}
func TestInvalidDependencies(t *testing.T) {
	f := setup(t, journal.Limits{})
	d := f.s.deps
	if _, e := New(Dependencies{}); e != ErrDependencies {
		t.Fatal(e)
	}
	var store *journal.Store
	d.Admission = store
	if _, e := New(d); e != ErrDependencies {
		t.Fatal(e)
	}
	d = f.s.deps
	d.Policy = PolicyFunc(nil)
	if _, e := New(d); e != ErrDependencies {
		t.Fatal(e)
	}
	var s *Service
	if r := s.Notify(context.Background(), payload("R"), caller(), notification.Deadline{}); r.Reason != "invalid_dependencies" {
		t.Fatal(r)
	}
	s = &Service{}
	if r := s.Notify(context.Background(), payload("R"), caller(), notification.Deadline{}); r.Reason != "invalid_dependencies" {
		t.Fatal(r)
	}
	if r := f.s.Notify(nil, payload("R"), caller(), notification.Deadline{}); r.Reason != "invalid_context" { //nolint:staticcheck // Deliberately verify nil-context rejection.
		t.Fatal(r)
	}
}

func TestInvalidDisabledAndUnavailableHaveNoEffects(t *testing.T) {
	for _, mode := range []string{"invalid", "disabled", "desktop_disabled", "config_invalid", "remote", "headless", "hidden", "unknown_denied", "asserted_denied", "click_disabled", "target_invalid", "readiness_invalid"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			p, o := payload("R"), caller()
			switch mode {
			case "invalid":
				p.Title = "\x00"
			case "disabled":
				f.policy.Delivery.ExplicitEnabled = false
			case "desktop_disabled":
				f.policy.Delivery.DesktopEnabled = false
			case "config_invalid":
				f.policy.Delivery.Valid = false
			case "remote":
				o.Locality = origin.Remote
			case "headless":
				o.Interface = origin.Headless
			case "hidden":
				o.Hidden = true
			case "unknown_denied":
				f.policy.Route.AllowUnknownCaller = false
			case "asserted_denied":
				o.Provenance = origin.CallerAsserted
				f.policy.Route.AllowCallerAsserted = false
			case "click_disabled":
				f.policy.Delivery.ClickToFocus = false
			case "target_invalid":
				f.s.deps.Target = TargetFunc(func(context.Context, origin.Context, origin.RoutePolicy) (origin.Target, error) {
					return origin.Target{Navigation: notification.NavigationResult{Capability: "available", Precision: "chat_id"}}, nil
				})
			case "readiness_invalid":
				f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
					return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "unknown"}
				})
			}
			r := f.notify(p, o)
			if r.Status == "submitted" || r.Status == "unknown" || f.effects.Load() != 0 {
				t.Fatal(r)
			}
			// No denied request consumes the key; the same key can be admitted after repair.
			f.policy = setupPolicy()
			f.s.deps.Target = TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
				return origin.ResolveCodex(o, p), nil
			})
			f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
				return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
			})
			requireStatus(t, f.notify(payload("R"), caller()), "submitted")
		})
	}
}
func setupPolicy() Policy {
	return Policy{Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true, SoundEnabled: true}, Route: origin.RoutePolicy{LocalRouting: true, AllowUnknownCaller: true, AllowCallerAsserted: true, ApplicationPath: "/test/A.app", TeamID: "team-A"}}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestActualJournalLockConsumesOriginalDeadline(t *testing.T) {
	for _, phase := range []string{"lookup", "admit"} {
		t.Run(phase, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			fd, e := unix.Open(filepath.Join(f.root, "lock"), unix.O_RDWR, 0)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if e := unix.Close(fd); e != nil {
					t.Error(e)
				}
			}()
			lock := func() {
				if e := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
					t.Fatal(e)
				}
			}
			if phase == "lookup" {
				lock()
			} else {
				f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
					lock()
					return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
				})
			}
			start := time.Now()
			r := f.s.Notify(context.Background(), payload("R"), caller(), notification.Deadline{BootID: "boot", NotAfter: 100.04})
			if e := unix.Flock(fd, unix.LOCK_UN); e != nil {
				t.Fatal(e)
			}
			if r.Reason != "deadline_exceeded" || f.effects.Load() != 0 || time.Since(start) > time.Second {
				t.Fatal(r, time.Since(start))
			}
			f.s.deps.Readiness = readyFunc(func(_ context.Context, r notification.Request) notification.Readiness {
				return notification.Readiness{Navigation: fakeNavigation(r), CorrelationID: r.CorrelationID, Reason: "ready", Status: "ready"}
			})
			requireStatus(t, f.notify(payload("R"), caller()), "submitted")
		})
	}
}
func TestBestEffortNoneAndSilent(t *testing.T) {
	for _, mode := range []string{"hidden_best_effort", "click_disabled_best_effort", "none", "unknown_none_denied"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, journal.Limits{})
			p, o := payload("R"), caller()
			p.Navigation = notification.BestEffort
			f.policy.Delivery.SoundEnabled = false
			switch mode {
			case "hidden_best_effort":
				o.Hidden = true
			case "click_disabled_best_effort":
				f.policy.Delivery.ClickToFocus = false
			case "none":
				p.Navigation = notification.None
			case "unknown_none_denied":
				p.Navigation = notification.None
				f.policy.Route.AllowUnknownCaller = false
			}
			r := f.notify(p, o)
			if mode == "unknown_none_denied" {
				requireStatus(t, r, "rejected")
				if f.effects.Load() != 0 {
					t.Fatal(r)
				}
				return
			}
			requireStatus(t, r, "submitted")
			if f.requests[0].Target != (notification.DesktopTarget{}) || !f.requests[0].Silent || r.Navigation.Precision != "none" {
				t.Fatal(r, f.requests[0])
			}
		})
	}
}
func TestDefaultNavigationCanonicalReplayAndExactUnicodeConflict(t *testing.T) {
	f := setup(t, journal.Limits{})
	p := payload("R")
	p.Title = "é"
	a := f.notify(p, caller())
	requireStatus(t, a, "submitted")
	p.Navigation = notification.Required
	if b := f.notify(p, caller()); !b.Replayed || b.TrackingID != a.TrackingID {
		t.Fatal(b)
	}
	p.Title = "e\u0301"
	if b := f.notify(p, caller()); b.Reason != "idempotency_conflict" {
		t.Fatal(b)
	}
}
func TestConstructorHasNoEffectsAndEachNilPortFails(t *testing.T) {
	f := setup(t, journal.Limits{})
	before, e := os.ReadFile(filepath.Join(f.root, "journal.json"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := New(f.s.deps); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(f.root, "journal.json"))
	if e != nil {
		t.Fatal(e)
	}
	if string(before) != string(after) || f.loads.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("constructor effects")
	}
	for i := 0; i < 6; i++ {
		d := f.s.deps
		switch i {
		case 0:
			d.Admission = nil
		case 1:
			d.Policy = nil
		case 2:
			d.Target = nil
		case 3:
			d.Readiness = readyFunc(nil)
		case 4:
			d.Delivery = deliverFunc(nil)
		case 5:
			d.Clock = ClockFunc(nil)
		}
		if _, e := New(d); e != ErrDependencies {
			t.Fatal(i, e)
		}
	}
	var zero Service
	if e := zero.Close(context.Background()); e != ErrDependencies {
		t.Fatal(e)
	}
}

func TestClientCallKeyKindsAndSourceIsolation(t *testing.T) {
	f := setup(t, journal.Limits{})
	p, o := payload("call-1"), caller()
	p.RequestID = nil
	first := f.notify(p, o)
	requireStatus(t, first, "submitted")
	for _, change := range []func(*Payload, *origin.Context){
		func(p *Payload, o *origin.Context) { id := o.CallID; p.RequestID = &id },
		func(_ *Payload, o *origin.Context) { o.SessionID = "session-B" },
		func(_ *Payload, o *origin.Context) { o.Namespace = "other-source" },
	} {
		f.advance()
		q, c := p, o
		change(&q, &c)
		r := f.notify(q, c)
		requireStatus(t, r, "submitted")
		if r.Replayed || r.TrackingID == first.TrackingID {
			t.Fatal(r)
		}
	}
	p.Body += "changed"
	if r := f.notify(p, o); r.Reason != "idempotency_conflict" || f.effects.Load() != 4 {
		t.Fatal(r)
	}
}

func TestUnqualifiedServiceClockFailsBeforePorts(t *testing.T) {
	f := setup(t, journal.Limits{})
	f.s.deps.Clock = ClockFunc(func() notification.Deadline { return notification.Deadline{} })
	r := f.notify(payload("R"), caller())
	if r.Reason != "invalid_deadline" || f.loads.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal(r)
	}
}

func fakeNavigation(r notification.Request) notification.NavigationResult {
	if r.Target.ThreadID != "" {
		return notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}
	}
	return notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "no_action"}
}
