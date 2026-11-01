package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestSharedPolicyReadCancellationAndFreshness(t *testing.T) {
	lifetime, cancelLifetime := context.WithCancel(context.Background())
	defer cancelLifetime()
	entered, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	reader := sharedPolicyReads{lifetime: lifetime, read: func(ctx context.Context, root string) (installruntime.PolicySnapshot, error) {
		n := calls.Add(1)
		if n == 1 {
			close(entered)
			select {
			case <-finish:
			case <-ctx.Done():
				return installruntime.PolicySnapshot{}, ctx.Err()
			}
		}
		return installruntime.PolicySnapshot{Installation: installruntime.InstalledSnapshot{Ledger: installruntime.Ledger{Generation: uint64(n)}}}, nil
	}}
	first, cancelFirst := context.WithCancel(lifetime)
	firstDone := make(chan error, 1)
	go func() { _, err := reader.get(first, "/fixture"); firstDone <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("read not started")
	}
	cancelFirst()
	if err := <-firstDone; err != context.Canceled {
		t.Fatal("caller cancellation lost", err)
	}
	// The canceled leader must not poison the still-running shared observation.
	if calls.Load() != 1 {
		t.Fatal("cancellation restarted the integrity read")
	}
	reader.mu.Lock()
	active := reader.flight
	reader.mu.Unlock()
	if active == nil {
		t.Fatal("leader canceled peer work")
	}
	close(finish)
	select {
	case <-active.done:
	case <-time.After(time.Second):
		t.Fatal("shared read did not finish")
	}
	if active.err != nil || active.snapshot.Installation.Ledger.Generation != 1 {
		t.Fatal("shared observation inherited leader cancellation", active.err)
	}
	latest, err := reader.get(lifetime, "/fixture")
	if err != nil || latest.Installation.Ledger.Generation != 2 || calls.Load() != 2 {
		t.Fatalf("completed policy was cached: %+v %v calls=%d", latest, err, calls.Load())
	}
}

// The shared transport admits waiting calls without widening native concurrency.
func TestSharedSubmitWaitsAndCancels(t *testing.T) {
	slots := make(chan struct{}, 2)
	ctx := context.Background()
	if err := acquireSharedSubmit(ctx, slots); err != nil {
		t.Fatal(err)
	}
	if err := acquireSharedSubmit(ctx, slots); err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- acquireSharedSubmit(waiting, slots) }()
	select {
	case err := <-done:
		t.Fatalf("third call did not wait: %v", err)
	default:
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	go func() { done <- acquireSharedSubmit(ctx, slots) }()
	<-slots
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting call did not resume")
	}
	if len(slots) != 2 {
		t.Fatalf("capacity changed: %d", len(slots))
	}
	<-slots
	<-slots
}
