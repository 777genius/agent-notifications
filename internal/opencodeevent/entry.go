package opencodeevent

import (
	"context"
	"io"
	"sync"
	"time"
)

// Entry owns the original timer and joined continuous watcher. E0 checks are
// independent of source qualification: availability alone grants no admission.
type Entry struct {
	ctx               context.Context
	cancel            context.CancelFunc
	done              chan struct{}
	mu                sync.Mutex
	source            SnapshotPort
	anchor            ClockSnapshot
	last, senderLimit int64
	Started           time.Time
	policy            *TimePolicy
}

func BeginEntry(parent context.Context, started time.Time, source SnapshotPort) (*Entry, error) {
	ctx, cancel := context.WithDeadline(parent, started.Add(20*time.Second))
	e := &Entry{ctx: ctx, cancel: cancel, done: make(chan struct{}), source: source, Started: started}
	if source == nil || started.IsZero() || ctx.Err() != nil {
		cancel()
		return nil, ErrClockUnavailable
	}
	a, err := source.SampleSnapshot()
	if _, invalid := a.JSON(); err != nil || invalid != nil || ctx.Err() != nil {
		cancel()
		return nil, ErrClockUnavailable
	}
	e.anchor = a
	e.last = a.MonoLoNs
	go func() {
		defer close(e.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.Check() {
					return
				}
			}
		}
	}()
	return e, nil
}
func (e *Entry) Context() context.Context { return e.ctx }
func (e *Entry) Close()                   { e.cancel(); <-e.done }
func (e *Entry) Check() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return false
	}
	s, err := e.source.SampleSnapshot()
	_, invalid := s.JSON()
	if err != nil || invalid != nil || s.Boot != e.anchor.Boot || s.ClockDomain != e.anchor.ClockDomain ||
		s.ClockKind != e.anchor.ClockKind || s.MonoLoNs < e.last || s.MonoLoNs-e.anchor.MonoLoNs >= int64(20*time.Second) ||
		(e.senderLimit > 0 && s.MonoHiNs >= e.senderLimit) || (e.policy != nil && (s.ClockKind != e.policy.RawKind || s.UncertaintyNs > e.policy.NativeReadBoundNS || !qualifiedProgress(ClockSample{TickNS: e.anchor.MonoLoNs, WallNS: e.anchor.WallUnixNs}, ClockSample{TickNS: s.MonoLoNs, WallNS: s.WallUnixNs}, e.policy.ComparisonBoundNS))) {
		e.cancel()
		return false
	}
	e.last = s.MonoLoNs
	return true
}
func (e *Entry) Tighten(p Provenance, policy TimePolicy) bool {
	e.mu.Lock()
	ok := p.Clock.BootID == e.anchor.Boot && p.Clock.Domain == e.anchor.ClockDomain && policy.RawKind == e.anchor.ClockKind &&
		p.Clock.Fence == policy.Fence(e.anchor.Boot, e.anchor.ClockDomain) && p.DeadlineTickNS > policy.ComparisonBoundNS
	if e.policy != nil && *e.policy != policy {
		ok = false
	}
	if ok {
		limit := p.DeadlineTickNS - policy.ComparisonBoundNS
		if e.senderLimit == 0 || limit < e.senderLimit {
			e.senderLimit = limit
		}
		bound := policy
		e.policy = &bound
	}
	e.mu.Unlock()
	if !ok {
		e.cancel()
		return false
	}
	return e.Check()
}

// ReadOwnedBounded accepts only a handle whose Close unblocks Read (production
// os.Stdin). Cancellation closes and joins the reader before returning.
func ReadOwnedBounded(ctx context.Context, input io.ReadCloser) ([]byte, error) {
	if input == nil || ctx.Err() != nil {
		return nil, ErrPrivateFrame
	}

	var raw []byte
	var err error
	done := make(chan struct{})
	go func() { defer close(done); raw, err = io.ReadAll(io.LimitReader(input, 4097)) }()
	select {
	case <-done:
		if err != nil || len(raw) == 0 || len(raw) > 4096 || ctx.Err() != nil {
			return nil, ErrPrivateFrame
		}
		return raw, nil
	case <-ctx.Done():
		_ = input.Close()
		<-done
		return nil, ErrPrivateFrame
	}
}
