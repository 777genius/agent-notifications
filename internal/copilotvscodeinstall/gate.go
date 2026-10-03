package copilotvscodeinstall

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// PhysicalProof is an immutable normalized observation. All fields are private;
// N2a supplies NO affirmative constructor. The later receipt adapter in this
// package must derive them from actual public NewLocal/Engine/observed receipts,
// never configuration, a bool, or fabricated SDK facts. Zero values deny.
type PhysicalProof struct {
	binding                                                      portable.Binding
	physical, selectedLocalClass, qualifiedTuple                 bool
	goos, goarch, receiptDigest, packageDigest, projectionDigest string
	primaryDigest, helperDigest                                  string
}

// ProofPort reloads actual physical profile/receipt/package/projection authority.
// It is not a lease and never executes a native app. Missing/unqualified proof
// denies. No production implementation is wired at this checkpoint.
type ProofPort interface {
	CheckLocal(context.Context, portable.Binding, installruntime.InstalledSnapshot) (PhysicalProof, error)
}

func digest(s string) bool {
	data, err := hex.DecodeString(s)
	return err == nil && len(data) == 32 && s != "0000000000000000000000000000000000000000000000000000000000000000"
}
func (p PhysicalProof) matches(b portable.Binding, s installruntime.PolicySnapshot) bool {
	if !p.physical || !p.selectedLocalClass || !p.qualifiedTuple || p.binding != b ||
		p.goos != runtime.GOOS || p.goarch != runtime.GOARCH || !digest(p.receiptDigest) ||
		!digest(p.packageDigest) || !digest(p.projectionDigest) || !digest(p.primaryDigest) {
		return false
	}
	key, consumer, _, err := b.Registration()
	if err != nil || !reflect.DeepEqual(s.Installation.Ledger.Consumers[key], consumer) {
		return false
	}
	primary, err := portable.ResolvePrimaryExecutable(s.Installation.Ledger, b.Primary)
	if err != nil || s.Installation.Ledger.Files[primary].SHA256 != p.primaryDigest {
		return false
	}
	if runtime.GOOS == "darwin" {
		n := s.Installation.Ledger.Native
		if n == nil || n.DecoderFloor != 1 || !digest(p.helperDigest) || n.SHA256 != p.helperDigest {
			return false
		}
	}
	return true
}

// Gate implements the ACTUAL N1 consumer interface. It binds one immutable
// portable registration; every request supplies the observed generation.
type Gate struct {
	Binding portable.Binding
	Proof   ProofPort
}

var _ copilotvscodeevent.Gate = Gate{}

func (g Gate) qualify(ctx context.Context, s installruntime.PolicySnapshot, expected copilotvscodeevent.Binding) (Consent, error) {
	if ctx.Err() != nil || g.Proof == nil || s.Installation.Ledger.WriterFloor < installruntime.LocalPolicyWriterFloor || g.Binding.CheckSnapshot(s.Installation) != nil || !recorded(s, g.Binding) || consumerBinding(s, g.Binding) != expected {
		return Consent{}, ErrDenied
	}
	consent, err := ReadConsent(s, g.Binding)
	if err != nil {
		return Consent{}, err
	}
	proof, err := checkProofPort(ctx, g.Proof, g.Binding, s.Installation)
	if err != nil || !proof.matches(g.Binding, s) {
		return Consent{}, ErrDenied
	}
	return consent, nil
}

// ConsumerBinding returns only a qualified N1 value; it is not an authorizedGrant.
func (g Gate) ConsumerBinding(ctx context.Context) (copilotvscodeevent.Binding, error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return copilotvscodeevent.Binding{}, err
	}
	b := consumerBinding(s, g.Binding)
	if _, err := g.qualify(ctx, s, b); err != nil {
		return copilotvscodeevent.Binding{}, err
	}
	return b, nil
}
func (g Gate) Channels(ctx context.Context, b copilotvscodeevent.Binding) copilotvscodeevent.Channels {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return copilotvscodeevent.Channels{}
	}
	c, err := g.qualify(ctx, s, b)
	if err != nil {
		return copilotvscodeevent.Channels{}
	}
	return copilotvscodeevent.Channels{Desktop: c.desktop, Webhook: c.webhook}
}
func (g Gate) Recheck(ctx context.Context, b copilotvscodeevent.Binding, channel copilotvscodeevent.Channel) bool {
	channels := g.Channels(ctx, b)
	return channel == copilotvscodeevent.DesktopChannel && channels.Desktop || channel == copilotvscodeevent.WebhookChannel && channels.Webhook
}

// acquire owns exactly one policy lease. Mac retains this through the native
// port; other desktops and webhook use the identical pre/completion seam.
func (g Gate) acquire(ctx context.Context, b copilotvscodeevent.Binding, channel copilotvscodeevent.Channel) (*authorityLease, error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return nil, err
	}
	current, release, err := installruntime.AcquirePolicyLease(ctx, g.Binding.ControlRoot, s)
	if err != nil {
		return nil, err
	}
	lease := &authorityLease{gate: g, binding: b, channel: channel, expected: current, release: release}
	if err := lease.check(ctx); err != nil {
		release()
		return nil, err
	}
	return lease, nil
}

type authorityLease struct {
	gate     Gate
	binding  copilotvscodeevent.Binding
	channel  copilotvscodeevent.Channel
	expected installruntime.PolicySnapshot
	release  func()
	once     sync.Once
}

var _ notifier.LocalAuthorityLease = (*authorityLease)(nil)

func (l *authorityLease) BundlePath() string {
	if n := l.expected.Installation.Ledger.Native; n != nil {
		return n.Path
	}
	return ""
}
func (l *authorityLease) ExecutablePath() string {
	return filepath.Join(l.BundlePath(), "Contents", "MacOS", "terminal-notifier-modern")
}
func (l *authorityLease) Release()                                { l.once.Do(l.release) }
func (l *authorityLease) BeforeHandoff(ctx context.Context) error { return l.check(ctx) }
func (l *authorityLease) Complete(ctx context.Context) error      { return l.check(ctx) }

// Re-read WITHOUT reacquiring the held locks. Cooperative AN writes are fenced;
// external profile/policy writers can still race between a check and handoff.
func (l *authorityLease) check(ctx context.Context) error {
	preimage, err := installruntime.Fingerprint(filepath.Join(l.gate.Binding.ControlRoot, "agent-notifications.json"))
	if err != nil || preimage != l.expected.Preimage {
		return ErrDenied
	}
	current, err := installruntime.ReadInstalledSnapshot(l.gate.Binding.ControlRoot)
	if err != nil || !reflect.DeepEqual(current, l.expected.Installation) {
		return ErrDenied
	}
	consent, err := l.gate.qualify(ctx, l.expected, l.binding)
	if err != nil {
		return err
	}
	if l.channel == copilotvscodeevent.DesktopChannel && consent.desktop || l.channel == copilotvscodeevent.WebhookChannel && consent.webhook {
		return nil
	}
	return ErrDenied
}

// An unavailable public proof adapter, including a typed nil or a panic, denies.
func checkProofPort(ctx context.Context, port ProofPort, b portable.Binding, s installruntime.InstalledSnapshot) (proof PhysicalProof, err error) {
	err = ErrDenied
	defer func() {
		if recover() != nil {
			proof, err = PhysicalProof{}, ErrDenied
		}
	}()
	if port == nil {
		return proof, err
	}
	return port.CheckLocal(ctx, b, s)
}
