package main

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
)

// Synchronous admission/provider calls share this
// serial guard. Revalidation only revokes the original held authority; neither
// a new context nor a later equal image can renew a cancelled invocation.
type runtimeLifetimeGuard struct {
	mu     sync.Mutex
	lease  *runtimeImageLease
	cancel context.CancelFunc
}

func (g *runtimeLifetimeGuard) check(ctx context.Context) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lease == nil || g.lease.origin == nil || ctx == nil {
		g.cancel()
		return false
	}
	// Retain the acquisition deadline during every hash/read. Supplied context
	// cancellation can shorten that budget; it cannot create another lifetime.
	readCtx, cancelRead := context.WithCancel(g.lease.origin)
	stop := context.AfterFunc(ctx, cancelRead)
	defer func() { stop(); cancelRead() }()
	if ctx.Err() != nil {
		cancelRead()
	}
	_, err := g.lease.Revalidate(readCtx)
	if err != nil || ctx.Err() != nil {
		g.lease.revoked = true
		g.cancel()
		return false
	}
	return true
}

// Admission already samples this port before locks and immediately before its
// claim. Use that existing boundary to recheck the actual parent, without an
// admission-kernel change or a cache of live authority.
type runtimeGuardedSnapshot struct {
	source opencodeevent.SnapshotPort
	guard  *runtimeLifetimeGuard
	ctx    context.Context
}

func (p runtimeGuardedSnapshot) SampleSnapshot() (opencodeevent.ClockSnapshot, error) {
	return p.SampleSnapshotContext(p.ctx)
}
func (p runtimeGuardedSnapshot) SampleSnapshotContext(ctx context.Context) (opencodeevent.ClockSnapshot, error) {
	if !p.guard.check(ctx) {
		return opencodeevent.ClockSnapshot{}, opencodeevent.ErrClockUnavailable
	}
	return p.source.SampleSnapshot()
}

type runtimeGuardedDelivery struct {
	port  notification.DeliveryPort
	guard *runtimeLifetimeGuard
}

func (p runtimeGuardedDelivery) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	if !p.guard.check(ctx) || p.port == nil {
		return notification.Receipt{Status: "unavailable"}
	}
	return p.port.Deliver(ctx, r)
}

// Bind the consumed frame to the held owned descriptor's origin. Reading and
// decoding retain the same Entry deadline; no probe or fresh budget is created.
func consumeRuntimeBoundFrame(consumer opencodeevent.ComposedConsumer, entry *opencodeevent.Entry, input io.ReadCloser, origin string) opencodeevent.Receipt {
	raw, err := opencodeevent.ReadOwnedBounded(entry.Context(), input)
	if err != nil {
		return opencodeevent.Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	frame, err := opencodeevent.DecodePrivate(raw, consumer.Selection)
	if err != nil || frame.Origin != origin {
		return opencodeevent.Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	return consumer.ConsumeEntry(entry, io.NopCloser(bytes.NewReader(raw)))
}
