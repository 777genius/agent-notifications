// Package runtime composes explicit notifications. It never initializes state.
package runtime

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// Options are trusted process configuration, never tool arguments. Empty paths
// select stable production locations. Seams permit offline private-root tests.
type Options struct {
	ControlRoot, JournalRoot, SpoolRoot, GlobalConfig string
	BootClock                                         notifier.BootClock
	JournalClock                                      journal.Clock
	ReadSnapshot                                      func(context.Context, string) (installruntime.PolicySnapshot, error)
	ReadGlobal                                        func(string) ([]byte, error)
	OpenJournal                                       func(context.Context, journal.Options) (*journal.Store, error)
	DeliveryFactory                                   func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery
}
type Delivery interface {
	notification.ReadinessPort
	notification.DeliveryPort
}

// Clock bridges the native boot epoch; an unavailable clock returns no epoch.
type Clock struct{ Boot notifier.BootClock }

func (c Clock) Now() notification.Deadline {
	if c.Boot == nil {
		return notification.Deadline{}
	}
	b, s, e := c.Boot.Now()
	if e != nil || b == "" || s < 0 || math.IsNaN(s) || math.IsInf(s, 0) {
		return notification.Deadline{}
	}
	return notification.Deadline{BootID: b, NotAfter: s}
}

type Backend struct {
	opts    Options
	clock   Clock
	service *agentnotify.Service
	mu      sync.Mutex
	closed  bool
	active  int
	drained chan struct{}
}

// New allocates one Service. It performs no filesystem writes, probes or opens.
func New(o Options) (*Backend, error) {
	if o.ControlRoot == "" {
		p, e := os.UserConfigDir()
		if e != nil {
			return nil, e
		}
		o.ControlRoot = filepath.Join(p, "agent-notifications")
	}
	if o.GlobalConfig == "" {
		p, e := os.UserHomeDir()
		if e != nil {
			return nil, e
		}
		o.GlobalConfig = filepath.Join(p, ".claude", "claude-notifications-go", "config.json")
	}
	if o.JournalRoot == "" {
		o.JournalRoot = filepath.Join(o.ControlRoot, "state", "journal")
	}
	if o.SpoolRoot == "" {
		o.SpoolRoot = filepath.Join(o.ControlRoot, "state", "native-spool")
	}
	for _, p := range []string{o.ControlRoot, o.GlobalConfig, o.JournalRoot, o.SpoolRoot} {
		if !filepath.IsAbs(p) {
			return nil, errors.New("invalid_runtime_path")
		}
	}
	if o.BootClock == nil {
		o.BootClock = notifier.SystemBootClock{}
	}
	if o.JournalClock == nil {
		o.JournalClock = journal.PlatformClock{}
	}
	if o.ReadSnapshot == nil {
		o.ReadSnapshot = installruntime.ReadPolicySnapshot
	}
	if o.ReadGlobal == nil {
		o.ReadGlobal = readGlobal
	}
	if o.OpenJournal == nil {
		o.OpenJournal = journal.Open
	}
	if o.DeliveryFactory == nil {
		o.DeliveryFactory = func(m notifier.ManagedInstallation, p string, c notifier.BootClock) Delivery {
			return &notifier.StructuredDelivery{Installation: m, Clock: c, Process: notifier.ManagedNativeProcess{}, Spool: &notifier.PrivateNativeSpool{Root: p, Clock: c}}
		}
	}
	b := &Backend{opts: o, clock: Clock{o.BootClock}, drained: make(chan struct{})}
	ports := requestPorts{b}
	s, e := agentnotify.New(agentnotify.Dependencies{Admission: ports, Policy: ports, Target: agentnotify.TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
		return origin.ResolveCodex(o, p), nil
	}), Readiness: ports, Delivery: ports, Clock: b.clock})
	b.service = s
	return b, e
}
func (b *Backend) Clock() agentnotify.Clock { return b.clock }
func refused(reason string) agentnotify.Receipt {
	return agentnotify.Receipt{Status: "rejected", Reason: reason, RetrySafe: true, Navigation: notification.NavigationResult{Capability: "unavailable", Precision: "none"}}
}
func (b *Backend) Notify(ctx context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return refused("service_closed")
	}
	b.active++
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.active--
		if b.closed && b.active == 0 {
			close(b.drained)
		}
	}()
	if ctx == nil {
		return refused("invalid_context")
	}
	if b.clock.Now().BootID == "" {
		return refused("unsupported_platform")
	}
	return b.service.Notify(context.WithValue(ctx, requestKey{}, &requestState{}), p, o, d)
}

// Close refuses new work and drains all calls, including replay lookups. No
// background worker or shared open file resource survives the drain.
func (b *Backend) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("invalid_context")
	}
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		if b.active == 0 {
			close(b.drained)
		}
	}
	done := b.drained
	b.mu.Unlock()
	select {
	case <-done:
		return b.service.Close(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

type requestKey struct{}
type requestState struct {
	store    *journal.Store
	snapshot installruntime.PolicySnapshot
	delivery Delivery
}
type requestPorts struct{ b *Backend }

func state(c context.Context) *requestState { return c.Value(requestKey{}).(*requestState) }
func (p requestPorts) Lookup(c context.Context, k journal.Key, d journal.Digest) (journal.Result, error) {
	s := state(c)
	var e error
	s.store, e = p.b.opts.OpenJournal(c, journal.Options{Root: p.b.opts.JournalRoot, Clock: p.b.opts.JournalClock})
	if e != nil {
		return journal.Result{}, e
	}
	return s.store.Lookup(c, k, d)
}
func (p requestPorts) Admit(c context.Context, a journal.Admission) (journal.Result, error) {
	return state(c).store.Admit(c, a)
}
func (p requestPorts) FinalizeOutcome(c context.Context, k journal.Key, a, s, r, b string, n *journal.Navigation) error {
	return state(c).store.FinalizeOutcome(c, k, a, s, r, b, n)
}
func (p requestPorts) Load(c context.Context, _ origin.Context) (agentnotify.Policy, error) {
	s := state(c)
	snap, e := p.b.opts.ReadSnapshot(c, p.b.opts.ControlRoot)
	if e != nil {
		return agentnotify.Policy{}, errConfig
	}
	s.snapshot = snap
	policy, e := p.b.policy(snap)
	if e != nil {
		return policy, e
	}
	if policy.Delivery.ExplicitEnabled && policy.Delivery.DesktopEnabled {
		s.delivery = p.b.opts.DeliveryFactory(notifier.ManagedInstallation{ControlRoot: p.b.opts.ControlRoot, Expected: s.snapshot.Installation}, p.b.opts.SpoolRoot, p.b.opts.BootClock)
	}
	return policy, nil
}
func (p requestPorts) CheckReadiness(c context.Context, r notification.Request) notification.Readiness {
	return state(c).delivery.CheckReadiness(c, r)
}
func (p requestPorts) Deliver(c context.Context, r notification.Request) notification.Receipt {
	return state(c).delivery.Deliver(c, r)
}
