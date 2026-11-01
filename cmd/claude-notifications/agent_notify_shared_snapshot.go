package main

import (
	"context"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Coalesce overlapping observations only. Completed reads are never cached;
// delivery still reacquires its exact expected installation lease unchanged.
type sharedPolicyReads struct {
	mu       sync.Mutex
	lifetime context.Context
	read     func(context.Context, string) (installruntime.PolicySnapshot, error)
	flight   *sharedPolicyFlight
}

type sharedPolicyFlight struct {
	done     chan struct{}
	snapshot installruntime.PolicySnapshot
	err      error
}

func (r *sharedPolicyReads) get(ctx context.Context, root string) (installruntime.PolicySnapshot, error) {
	r.mu.Lock()
	f := r.flight
	if f == nil {
		f = &sharedPolicyFlight{done: make(chan struct{})}
		r.flight = f
		go func() {
			readCtx, cancel := context.WithTimeout(r.lifetime, 5*time.Second)
			defer cancel()
			f.snapshot, f.err = r.read(readCtx, root)
			r.mu.Lock()
			r.flight = nil
			close(f.done)
			r.mu.Unlock()
		}()
	}
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return installruntime.PolicySnapshot{}, ctx.Err()
	case <-f.done:
		return f.snapshot, f.err
	}
}
