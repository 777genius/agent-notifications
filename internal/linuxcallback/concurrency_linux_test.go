//go:build linux

package linuxcallback

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func wireAttempt(ctx context.Context, server, caller *dbus.Conn, path dbus.ObjectPath, key, token string) *dbus.Call {
	return caller.Object(server.Names()[0], path).CallWithContext(ctx, "org.freedesktop.Application.ActivateAction", 0, "open", []dbus.Variant{dbus.MakeVariant(key)}, map[string]dbus.Variant{"activation-token": dbus.MakeVariant(token)})
}

func awaitValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("TEST barrier was not reached")
		var zero T
		return zero
	}
}

// Red when service-wide exclusion drops B, slots queue a third callback, or
// a later deliberate activation is suppressed by a consumed notification key.
func TestActivateActionIndependentAttempts(t *testing.T) {
	reached := make(chan string, 3)
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	var onceA, onceB sync.Once
	var verifies atomic.Int64
	ctx, server, caller, _, keyA, path := privateCallerFixture(t, func(h *Handler) {
		owners, e := h.ReadOwners(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		keyB := strings.Repeat("c", 64)
		if e = writeRecord(h.Snapshot, Record{1, keyB, h.Snapshot.InstallationID, h.Binding.SHA256, "second/id?", owners}); e != nil {
			t.Fatal(e)
		}
		h.Verify = func(context.Context, Snapshot) error { verifies.Add(1); return nil }
		h.Launch = func(_ context.Context, selected Snapshot, uri, token string) error {
			if selected != h.Snapshot {
				return ErrUnavailable
			}
			switch token {
			case "token-A":
				if uri != "codex://threads/opaque%2Fid%25%3F%23" {
					return ErrUnavailable
				}
				reached <- token
				<-releaseA
			case "token-B":
				if uri != "codex://threads/second%2Fid%3F" {
					return ErrUnavailable
				}
				reached <- token
				<-releaseB
			case "token-later":
				if uri != "codex://threads/opaque%2Fid%25%3F%23" {
					return ErrUnavailable
				}
				reached <- token
			default:
				return ErrUnavailable
			}
			return nil
		}
	})
	t.Cleanup(func() { onceA.Do(func() { close(releaseA) }); onceB.Do(func() { close(releaseB) }) })
	keyB := strings.Repeat("c", 64)
	doneA, doneB := make(chan *dbus.Call, 1), make(chan *dbus.Call, 1)
	go func() { doneA <- wireAttempt(ctx, server, caller, path, keyA, "token-A") }()
	if got := awaitValue(t, reached); got != "token-A" {
		t.Fatal(got)
	}
	go func() { doneB <- wireAttempt(ctx, server, caller, path, keyB, "token-B") }()
	if got := awaitValue(t, reached); got != "token-B" {
		t.Fatal(got)
	}
	third := wireAttempt(ctx, server, caller, path, keyA, "token-third")
	if third.Err == nil || !strings.Contains(third.Err.Error(), "callback_busy") || verifies.Load() != 2 {
		t.Fatal("third was queued or reached verification", third.Err, verifies.Load())
	}
	onceB.Do(func() { close(releaseB) })
	if c := awaitValue(t, doneB); c.Err != nil {
		t.Fatal(c.Err)
	}
	select {
	case c := <-doneA:
		t.Fatal("A completed before its release", c.Err)
	default:
	}
	onceA.Do(func() { close(releaseA) })
	if c := awaitValue(t, doneA); c.Err != nil {
		t.Fatal(c.Err)
	}
	if c := wireAttempt(ctx, server, caller, path, keyA, "token-later"); c.Err != nil {
		t.Fatal("later deliberate activation rejected", c.Err)
	}
	if got := awaitValue(t, reached); got != "token-later" || verifies.Load() != 3 {
		t.Fatal(got, verifies.Load())
	}
}

// Return time, not context cancellation, bounds verifier lifetime and capacity.
func TestActivateActionShutdownDrainsActualVerifier(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	var launches atomic.Int64
	ctx, server, caller, a, key, path := privateCallerFixture(t, func(h *Handler) {
		h.Verify = func(ctx context.Context, _ Snapshot) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		}
		h.Launch = func(context.Context, Snapshot, string, string) error { launches.Add(1); return nil }
	})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan *dbus.Call, 1)
	go func() { done <- wireAttempt(ctx, server, caller, path, key, "native-token") }()
	awaitValue(t, entered)
	drained := make(chan struct{})
	go func() { a.stop(); _ = server.Close(); close(drained) }()
	awaitValue(t, canceled)
	if c := wireAttempt(ctx, server, caller, path, key, "native-token"); c.Err == nil {
		t.Fatal("shutdown admitted a new callback")
	}
	select {
	case <-drained:
		t.Fatal("transport closed before verifier returned")
	default:
	}
	select {
	case <-done:
		t.Fatal("handler returned before verifier")
	default:
	}
	once.Do(func() { close(release) })
	awaitValue(t, drained)
	awaitValue(t, done)
	if launches.Load() != 0 {
		t.Fatal("canceled verifier launched", launches.Load())
	}
}

type entryAdvanceClock struct{ calls atomic.Int64 }

func (c *entryAdvanceClock) Now() (string, float64, error) {
	if c.calls.Add(1) == 1 {
		return "test-boot", 100, nil
	}
	return "test-boot", 104, nil
}

// Red if application entry at 100 is silently reset to a new budget at 104.
func TestActivateActionPreservesEntryBudget(t *testing.T) {
	clock := new(entryAdvanceClock)
	var launches atomic.Int64
	ctx, server, caller, _, key, path := privateCallerFixture(t, func(h *Handler) {
		h.Clock = clock
		h.Launch = func(context.Context, Snapshot, string, string) error { launches.Add(1); return nil }
	})
	if c := wireAttempt(ctx, server, caller, path, key, "native-token"); c.Err == nil || launches.Load() != 0 || clock.calls.Load() < 2 {
		t.Fatal("original entry budget was reset", c.Err, launches.Load(), clock.calls.Load())
	}
	// A separate new activation at 104 receives its own budget.
	if c := wireAttempt(ctx, server, caller, path, key, "native-token"); c.Err != nil || launches.Load() != 1 {
		t.Fatal("fresh activation rejected", c.Err, launches.Load())
	}
}

type startupClock struct {
	samples chan int
	calls   atomic.Int64
}

func (c *startupClock) Now() (string, float64, error) {
	n := int(c.calls.Add(1))
	if n <= 2 {
		c.samples <- n
	}
	return "test-boot", 100, nil
}

// A pending cold activation must survive the name-owner publication gap.
func TestActivateActionWaitsForStartupOutcome(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "owned", false: "canceled"}[success], func(t *testing.T) {
			startup := make(chan struct{})
			clock := &startupClock{samples: make(chan int, 2)}
			var launches atomic.Int64
			ctx, server, caller, a, key, path := privateCallerFixture(t, func(h *Handler) {
				h.Clock = clock
				h.Launch = func(context.Context, Snapshot, string, string) error { launches.Add(1); return nil }
			}, startup)
			done := make(chan *dbus.Call, 1)
			go func() { done <- wireAttempt(ctx, server, caller, path, key, "native-token") }()
			if awaitValue(t, clock.samples) != 1 || awaitValue(t, clock.samples) != 2 {
				t.Fatal("missing startup budget samples")
			}
			if success {
				a.mu.Lock()
				a.ready = true
				a.mu.Unlock()
				close(startup)
			} else {
				a.stop()
			}
			c := awaitValue(t, done)
			if success && (c.Err != nil || launches.Load() != 1) {
				t.Fatal("pending cold click lost", c.Err, launches.Load())
			}
			if !success && (c.Err == nil || launches.Load() != 0) {
				t.Fatal("failed startup launched", c.Err, launches.Load())
			}
		})
	}
}
