//go:build linux

package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/godbus/dbus/v5"
)

func strictTestHints(id uint32) FocusHints {
	return FocusHints{Strict: &notification.TerminalTarget{Generation: uint64(id)}, StrictOwner: ":1.42"}
}

func strictTestSignal(id uint32) *dbus.Signal {
	return &dbus.Signal{Name: "org.freedesktop.Notifications.ActionInvoked",
		Path: "/org/freedesktop/Notifications", Sender: ":1.42",
		Body: []any{id, "default"}}
}

func strictTestServer() *Server {
	return &Server{focusCtx: make(map[uint32]FocusHints), done: make(chan struct{})}
}

func sendStrictTest(t *testing.T, signals chan<- *dbus.Signal, signal *dbus.Signal) {
	t.Helper()
	select {
	case signals <- signal:
	case <-time.After(2 * time.Second):
		t.Fatal("raw strict action ingress blocked behind activation")
	}
}

func recvStrictTest[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("strict action observation missing")
		var zero T
		return zero
	}
}

func waitStrictTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("strict action worker was not joined")
	}
}

func startStrictTest(t *testing.T, s *Server,
	activate func(context.Context, *notification.TerminalTarget) error) (chan *dbus.Signal, <-chan struct{}) {
	t.Helper()
	signals, stopped := make(chan *dbus.Signal), make(chan struct{})
	go func() {
		s.processStrictActions(signals, activate)
		close(stopped)
	}()
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
		waitStrictTest(t, stopped)
	})
	return signals, stopped
}

type strictTestCall struct {
	target      *notification.TerminalTarget
	active      int32
	deadline    time.Time
	hasDeadline bool
}

func TestStrictActionQueueCapturesSecondBeforeCloseExpiry(t *testing.T) {
	s := strictTestServer()
	first, second := strictTestHints(1), strictTestHints(2)
	s.focusCtx[1], s.focusCtx[2] = first, second
	release := make(chan struct{})
	calls := make(chan strictTestCall, 4)
	finished := make(chan *notification.TerminalTarget, 4)
	var active atomic.Int32
	signals, stopped := startStrictTest(t, s, func(ctx context.Context, target *notification.TerminalTarget) error {
		n := active.Add(1)
		defer active.Add(-1)
		deadline, ok := ctx.Deadline()
		calls <- strictTestCall{target, n, deadline, ok}
		if target == first.Strict {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		finished <- target
		return ctx.Err()
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	sendStrictTest(t, signals, strictTestSignal(1))
	a := recvStrictTest(t, calls)
	if a.target != first.Strict || a.active != 1 || !a.hasDeadline {
		t.Fatalf("first activation = %+v", a)
	}
	// This is the production expiry callback with the snapshot captured by
	// ClosedB. Invoke it deterministically while A still owns the worker.
	expireSecond := func() { s.expireStrictFocus(2, second) }
	sendStrictTest(t, signals, strictTestSignal(2))
	sendStrictTest(t, signals, nil) // next receive acknowledges prior admission
	s.focusCtxMu.RLock()
	_, remains := s.focusCtx[2]
	s.focusCtxMu.RUnlock()
	if remains {
		t.Fatal("second action remained vulnerable to close expiry")
	}
	expireSecond()
	select {
	case extra := <-calls:
		t.Fatalf("activation was not serial: %+v", extra)
	default:
	}
	close(release)
	if recvStrictTest(t, finished) != first.Strict {
		t.Fatal("first activation did not finish first")
	}
	b := recvStrictTest(t, calls)
	if b.target != second.Strict || b.active != 1 || !b.hasDeadline || !b.deadline.After(a.deadline) {
		t.Fatalf("second activation did not receive a fresh serial budget: %+v", b)
	}
	if remaining := time.Until(b.deadline); remaining <= 0 || remaining > 4*time.Second {
		t.Fatalf("activation budget = %v", remaining)
	}
	if recvStrictTest(t, finished) != second.Strict {
		t.Fatal("accepted second action was lost after close expiry")
	}
	if s.takeStrictAction(strictTestSignal(2)) != nil {
		t.Fatal("accepted action was replayable")
	}
	close(s.done)
	waitStrictTest(t, stopped)
	select {
	case extra := <-calls:
		t.Fatalf("duplicate activation: %+v", extra)
	default:
	}
}

func TestStrictActionAdmissionRejectsSpoofMalformedAndReplay(t *testing.T) {
	s := strictTestServer()
	snapshot := strictTestHints(1)
	s.focusCtx[1] = snapshot
	s.focusCtx[2] = FocusHints{TerminalName: "legacy"}
	s.focusCtx[3] = FocusHints{Strict: strictTestHints(3).Strict}
	foreign, wrongPath, wrongName := strictTestSignal(1), strictTestSignal(1), strictTestSignal(1)
	foreign.Sender, wrongPath.Path, wrongName.Name = ":1.99", "/foreign", "foreign.ActionInvoked"
	wrongAction, wrongID, shortBody := strictTestSignal(1), strictTestSignal(1), strictTestSignal(1)
	wrongAction.Body, wrongID.Body, shortBody.Body = []any{uint32(1), "other"}, []any{1, "default"}, []any{uint32(1)}
	for _, signal := range []*dbus.Signal{nil, foreign, wrongPath, wrongName, wrongAction,
		wrongID, shortBody, strictTestSignal(2), strictTestSignal(3)} {
		if s.takeStrictAction(signal) != nil {
			t.Fatalf("unauthenticated or malformed action admitted: %+v", signal)
		}
		if s.focusCtx[1].Strict != snapshot.Strict {
			t.Fatal("rejected action consumed the authentic snapshot")
		}
	}
	if s.takeStrictAction(strictTestSignal(1)) != snapshot.Strict {
		t.Fatal("authentic action was not admitted")
	}
	if s.takeStrictAction(strictTestSignal(1)) != nil {
		t.Fatal("replay was admitted")
	}
}

func TestStrictClosedExpiryPreservesReplacementSnapshot(t *testing.T) {
	original := strictTestHints(7)
	for name, replacement := range map[string]FocusHints{
		"new target": strictTestHints(7),
		"new owner":  {Strict: original.Strict, StrictOwner: ":1.99"},
		"legacy":     {TerminalName: "replacement"},
	} {
		t.Run(name, func(t *testing.T) {
			s := strictTestServer()
			s.focusCtx[7] = replacement
			s.expireStrictFocus(7, original)
			got, exists := s.focusCtx[7]
			if !exists || got.Strict != replacement.Strict ||
				got.StrictOwner != replacement.StrictOwner || got.TerminalName != replacement.TerminalName {
				t.Fatal("old close expiry deleted the replacement notification")
			}
		})
	}
	s := strictTestServer()
	s.focusCtx[7] = original
	s.expireStrictFocus(7, original)
	if _, exists := s.focusCtx[7]; exists {
		t.Fatal("closed original snapshot survived expiry")
	}
}

func TestStrictActionQueueOverflowIsConsumedAndBounded(t *testing.T) {
	s := strictTestServer()
	for id := uint32(1); id <= 34; id++ {
		s.focusCtx[id] = strictTestHints(id)
	}
	release := make(chan struct{})
	calls, finished := make(chan uint64, 64), make(chan uint64, 64)
	signals, stopped := startStrictTest(t, s, func(ctx context.Context, target *notification.TerminalTarget) error {
		calls <- target.Generation
		if target.Generation == 1 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		finished <- target.Generation
		return ctx.Err()
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	sendStrictTest(t, signals, strictTestSignal(1))
	if recvStrictTest(t, calls) != 1 {
		t.Fatal("first activation missing")
	}
	for id := uint32(2); id <= 34; id++ {
		sendStrictTest(t, signals, strictTestSignal(id))
	}
	sendStrictTest(t, signals, nil)
	s.focusCtxMu.RLock()
	remaining := len(s.focusCtx)
	s.focusCtxMu.RUnlock()
	if remaining != 0 || s.takeStrictAction(strictTestSignal(34)) != nil {
		t.Fatal("overflow was not consumed or remained replayable")
	}
	close(release)
	seen := map[uint64]bool{1: true}
	for i := 0; i < 32; i++ {
		id := recvStrictTest(t, calls)
		if id < 2 || id > 33 || seen[id] {
			t.Fatalf("queue delivered duplicate or overflow action %d", id)
		}
		seen[id] = true
	}
	for i := 0; i < 33; i++ {
		recvStrictTest(t, finished)
	}
	close(s.done)
	waitStrictTest(t, stopped)
	select {
	case id := <-calls:
		t.Fatalf("queue exceeded 32 pending actions: %d", id)
	default:
	}
}

func TestStrictActionShutdownCancelsAndJoinsWithoutStartingQueued(t *testing.T) {
	for _, stopBy := range []string{"done", "signal channel"} {
		t.Run(stopBy, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := strictTestServer()
				first, second := strictTestHints(1), strictTestHints(2)
				s.focusCtx[1], s.focusCtx[2] = first, second
				calls := make(chan *notification.TerminalTarget, 2)
				cancelled, releaseCleanup := make(chan struct{}), make(chan struct{})
				signals, stopped := startStrictTest(t, s, func(ctx context.Context, target *notification.TerminalTarget) error {
					calls <- target
					if target != first.Strict {
						return nil
					}
					<-ctx.Done()
					close(cancelled)
					<-releaseCleanup
					return ctx.Err()
				})
				t.Cleanup(func() {
					select {
					case <-releaseCleanup:
					default:
						close(releaseCleanup)
					}
				})
				sendStrictTest(t, signals, strictTestSignal(1))
				if recvStrictTest(t, calls) != first.Strict {
					t.Fatal("first activation missing")
				}
				sendStrictTest(t, signals, strictTestSignal(2))
				sendStrictTest(t, signals, nil)
				if stopBy == "done" {
					close(s.done)
				} else {
					close(signals)
				}
				waitStrictTest(t, cancelled)
				// The worker is now durably blocked in its cleanup. There is
				// no wall-clock sleep or scheduler guess in this join check.
				synctest.Wait()
				select {
				case <-stopped:
					t.Fatal("processor returned without joining its worker")
				default:
				}
				close(releaseCleanup)
				waitStrictTest(t, stopped)
				select {
				case target := <-calls:
					t.Fatalf("queued action started after shutdown: %+v", target)
				default:
				}
			})
		})
	}
}

func TestLegacyFocusConsumptionIsAtomicAndDoesNotTakeStrictReplacement(t *testing.T) {
	s := strictTestServer()
	s.focusCtx[1] = FocusHints{TerminalName: "legacy"}
	taken := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, ok := s.takeLegacyFocus(1)
			taken <- ok
		}()
	}
	count := 0
	for i := 0; i < 2; i++ {
		if recvStrictTest(t, taken) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("legacy snapshot consumed %d times", count)
	}
	replacement := strictTestHints(1)
	s.focusCtx[1] = replacement
	if _, ok := s.takeLegacyFocus(1); ok || s.focusCtx[1].Strict != replacement.Strict {
		t.Fatal("unauthenticated callback consumed the strict replacement")
	}
	if s.takeStrictAction(strictTestSignal(1)) != replacement.Strict {
		t.Fatal("strict replacement was lost after the legacy callback")
	}
}
