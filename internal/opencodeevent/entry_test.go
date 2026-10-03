package opencodeevent

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
)

func TestEntryStalledOwnedStdinIsClosedAndJoined(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	defer func() { _ = writer.Close() }()
	source := &mutableSnapshot{sample: independentSnapshot()}
	entry, err := BeginEntry(context.Background(), time.Now(), source)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadOwnedBounded(entry.Context(), input); done <- err }()
	// Native suspend expires the operation although the Go timer has 20s left.
	source.advance(20 * time.Second)
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("stalled input accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not join after continuous expiry")
	}
	entry.Close() // watcher itself is joined before this returns
	if _, err = input.Read(make([]byte, 1)); err == nil {
		t.Fatal("owned pipe left open")
	}
	if _, err = writer.Write([]byte("x")); err == nil {
		t.Fatal("reader survived cancellation")
	}
}
func TestEntryTimerUsesOriginalCommandStart(t *testing.T) {
	source := &mutableSnapshot{sample: independentSnapshot()}
	started := time.Now().Add(-20*time.Second + 60*time.Millisecond)
	entry, err := BeginEntry(context.Background(), started, source)
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	deadline, ok := entry.Context().Deadline()
	if !ok || !deadline.Equal(started.Add(20*time.Second)) {
		t.Fatal("entry budget refreshed")
	}
	select {
	case <-entry.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("old entry gained another20s")
	}
	if entry.Check() {
		t.Fatal("expired command authorized")
	}
}
func TestEntrySenderCanOnlyShortenAndCoordinateLossAborts(t *testing.T) {
	for _, mode := range []string{"sender", "boot", "domain", "kind", "regression", "unavailable", "wall", "selectedR"} {
		t.Run(mode, func(t *testing.T) {
			source := &mutableSnapshot{sample: independentSnapshot()}
			entry, err := BeginEntry(context.Background(), time.Now(), source)
			if err != nil {
				t.Fatal(err)
			}
			defer entry.Close()
			fact, err := DecodePrivate(literalPrivate(t, "v1", "turn_idle_verified"), independentSelection("v1"))
			if err != nil {
				t.Fatal(err)
			}
			if !entry.Tighten(fact.Provenance, independentSelection("v1").Policy) {
				t.Fatal("initial deadline failed")
			}
			source.mu.Lock()
			switch mode {
			case "sender":
				source.sample.MonoLoNs = 120000000000
				source.sample.MonoHiNs = 120002000000
			case "boot":
				source.sample.Boot = "22345678-1234-1234-1234-123456789abc"
			case "domain":
				source.sample.ClockDomain = "linux-time:4:8"
			case "kind":
				source.sample.ClockKind = "linux-monotonic"
			case "regression":
				source.sample.MonoLoNs--
				source.sample.MonoHiNs--
			case "unavailable":
				source.sample = ClockSnapshot{}
			case "wall":
				source.sample.WallUnixNs += 3_000_000_000
			case "selectedR":
				source.sample.MonoHiNs = source.sample.MonoLoNs + 100_000_001
				source.sample.UncertaintyNs = 103_000_001
			}
			source.mu.Unlock()
			if entry.Check() || entry.Context().Err() == nil {
				t.Fatal("lost coordinate/deadline did not abort")
			}
		})
	}
}
func TestOwnedReadRejectsOverflowAndEmptyInput(t *testing.T) {
	for _, value := range []string{strings.Repeat(" ", 4097), ""} {
		if _, err := ReadOwnedBounded(context.Background(), io.NopCloser(strings.NewReader(value))); err == nil {
			t.Fatal("unbounded or empty read")
		}
	}
}
func TestHeldNativeDeadlineBridgeKeepsOriginalCoordinate(t *testing.T) {
	const boot = "12345678-1234-1234-1234-123456789abc"
	d := notification.Deadline{BootID: boot, NotAfter: 109.75}
	bridged, err := BridgeNativeDeadline(d, strings.ToUpper(boot))
	if err != nil || bridged.NotAfter != d.NotAfter || bridged.BootID != strings.ToUpper(boot) {
		t.Fatal("bridge extended deadline or changed boot")
	}
	for _, raw := range []string{"  " + boot, boot + "\n", "22345678-1234-1234-1234-123456789abc", "00000000-0000-0000-0000-000000000000"} {
		if _, err := BridgeNativeDeadline(d, raw); err == nil {
			t.Fatal("ambiguous raw UUID accepted")
		}
	}
}

func TestEntryCannotExtendTightenedDeadlineOrNativeReadBound(t *testing.T) {
	for _, mode := range []string{"retighten", "selectedR"} {
		t.Run(mode, func(t *testing.T) {
			source := &mutableSnapshot{sample: independentSnapshot()}
			entry, err := BeginEntry(context.Background(), time.Now(), source)
			if err != nil {
				t.Fatal(err)
			}
			defer entry.Close()
			fact, err := DecodePrivate(literalPrivate(t, "v1", "turn_idle_verified"), independentSelection("v1"))
			if err != nil {
				t.Fatal(err)
			}
			policy := independentSelection("v1").Policy
			if mode == "retighten" {
				short := fact.Provenance
				short.DeadlineTickNS = 108000000000
				if !entry.Tighten(short, policy) || !entry.Tighten(fact.Provenance, policy) {
					t.Fatal("valid shortening failed")
				}
				source.advance(7 * time.Second) // before original121s; after shortened106.8s.
			} else {
				policy.NativeReadBoundNS = 5000000
				fact.Provenance.Clock.Fence = policy.Fence(fact.Provenance.Clock.BootID, fact.Provenance.Clock.Domain)
				if !entry.Tighten(fact.Provenance, policy) {
					t.Fatal("5ms native bound refused its exact valid sample")
				}
				source.mu.Lock()
				source.sample.MonoHiNs = source.sample.MonoLoNs + 4000000
				source.sample.UncertaintyNs = 7000000
				source.mu.Unlock()
			}
			if entry.Check() {
				t.Fatal("tightened native/deadline authority relaxed")
			}
		})
	}
}
