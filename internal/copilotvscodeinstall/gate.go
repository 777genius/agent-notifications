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

// PhysicalProof is an immutable normalized observation. All fields are private.
// Private Local/Cursor producers derive facts from public installed observations.
// Zero values deny.
type PhysicalProof struct {
	binding                                                      portable.Binding
	physical, selectedLocalClass, qualifiedTuple                 bool
	goos, goarch, receiptDigest, packageDigest, projectionDigest string
	primaryDigest, helperDigest                                  string
	selectedCursorClass                                          bool
	cursorObservation, configObservation                         string
	localObservation                                             string
}

// ProofPort reloads actual physical profile/receipt/package/projection authority.
// It is not a lease and never executes a native app. Missing/unqualified proof
// denies. The private Cursor producer retains this existing interface.
type ProofPort interface {
	CheckLocal(context.Context, portable.Binding, installruntime.InstalledSnapshot) (PhysicalProof, error)
}

func digest(s string) bool {
	data, err := hex.DecodeString(s)
	return err == nil && len(data) == 32 && s != "0000000000000000000000000000000000000000000000000000000000000000"
}
func (p PhysicalProof) matches(b portable.Binding, s installruntime.PolicySnapshot) bool {
	class := b.Integration == portable.CopilotVSCode && p.selectedLocalClass && !p.selectedCursorClass && p.localObservation != "" && p.configObservation != "" && runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	if b.Integration == portable.Cursor {
		class = p.selectedCursorClass && !p.selectedLocalClass && p.cursorObservation != "" && p.configObservation != "" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
	}
	if !p.physical || !class || !p.qualifiedTuple || p.binding != b ||
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
	Binding       portable.Binding
	Proof         ProofPort
	localInitial  PhysicalProof
	localObserver ProofPort
}

var _ copilotvscodeevent.Gate = Gate{}

func (g Gate) qualify(ctx context.Context, s installruntime.PolicySnapshot, expected copilotvscodeevent.Binding) (Consent, PhysicalProof, error) {
	if ctx == nil || ctx.Err() != nil || g.Proof == nil || s.Installation.Ledger.WriterFloor < installruntime.LocalPolicyWriterFloor || g.Binding.CheckSnapshot(s.Installation) != nil || !recorded(s, g.Binding) || consumerBinding(s, g.Binding) != expected {
		return Consent{}, PhysicalProof{}, ErrDenied
	}
	port := g.Proof
	if g.Binding.Integration == portable.CopilotVSCode {
		// Retain the constructor's private observer. A copied affirmative value or
		// replacement of the public test/composition port cannot replay Local proof.
		if g.localObserver == nil {
			return Consent{}, PhysicalProof{}, ErrDenied
		}
		port = g.localObserver
	}
	proof, err := checkProofPort(ctx, port, g.Binding, s.Installation)
	if err != nil {
		return Consent{}, PhysicalProof{}, ErrDenied
	}
	return g.qualifyCheckedProof(ctx, s, expected, proof)
}

// qualifyCheckedProof applies the same predicates to one complete observation.
// Only the constructor supplies its just-checked proof; later qualify calls
// always obtain a fresh observation from the retained private observer above.
func (g Gate) qualifyCheckedProof(ctx context.Context, s installruntime.PolicySnapshot, expected copilotvscodeevent.Binding, proof PhysicalProof) (Consent, PhysicalProof, error) {
	if ctx == nil || ctx.Err() != nil || g.Proof == nil || s.Installation.Ledger.WriterFloor < installruntime.LocalPolicyWriterFloor || g.Binding.CheckSnapshot(s.Installation) != nil || !recorded(s, g.Binding) || consumerBinding(s, g.Binding) != expected || g.Binding.Integration == portable.CopilotVSCode && g.localObserver == nil {
		return Consent{}, PhysicalProof{}, ErrDenied
	}
	consent, err := ReadConsent(s, g.Binding)
	if g.Binding.Integration == portable.Cursor {
		consent, err = ReadCursorConsent(s, g.Binding)
	}
	if err != nil {
		return Consent{}, PhysicalProof{}, err
	}
	if !proof.matches(g.Binding, s) {
		return Consent{}, PhysicalProof{}, ErrDenied
	}
	if g.Binding.Integration == portable.CopilotVSCode {
		cfg, identity, err := readLocalConfig(g.Binding)
		if err != nil || identity != proof.configObservation || proof != g.localInitial {
			return Consent{}, PhysicalProof{}, ErrDenied
		}
		consent.desktop = consent.desktop && cfg.IsStatusDesktopEnabled("agent_stopping")
		consent.webhook = consent.webhook && cfg.IsStatusWebhookEnabled("agent_stopping") && cfg.Notifications.Webhook.Preset == "custom" && cfg.Notifications.Webhook.Format == "json" && len(cfg.Notifications.Webhook.Headers) == 0
	}
	if g.Binding.Integration == portable.Cursor {
		cfg, identity, err := readCursorConfig(g.Binding)
		if err != nil || identity != proof.configObservation {
			return Consent{}, PhysicalProof{}, ErrDenied
		}
		consent.desktop = consent.desktop && cfg.IsStatusDesktopEnabled("agent_stopping")
		consent.webhook = consent.webhook && cfg.IsStatusWebhookEnabled("agent_stopping") &&
			cfg.Notifications.Webhook.Preset == "custom" &&
			cfg.Notifications.Webhook.Format == "json" &&
			len(cfg.Notifications.Webhook.Headers) == 0
	}
	return consent, proof, nil
}

// ConsumerBinding returns only a qualified N1 value; it is not an authorizedGrant.
func (g Gate) ConsumerBinding(ctx context.Context) (copilotvscodeevent.Binding, error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return copilotvscodeevent.Binding{}, err
	}
	return g.ConsumerBindingFromSnapshot(ctx, s)
}

// ConsumerBindingFromSnapshot uses the snapshot already pinned by the caller's
// policy lease. It does not acquire either lock again or authorize an effect;
// the existing per-effect Gate lease still performs its own revalidation.
func (g Gate) ConsumerBindingFromSnapshot(ctx context.Context, s installruntime.PolicySnapshot) (copilotvscodeevent.Binding, error) {
	b := consumerBinding(s, g.Binding)
	if _, _, err := g.qualify(ctx, s, b); err != nil {
		return copilotvscodeevent.Binding{}, err
	}
	return b, nil
}
func (g Gate) Channels(ctx context.Context, b copilotvscodeevent.Binding) copilotvscodeevent.Channels {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return copilotvscodeevent.Channels{}
	}
	c, _, err := g.qualify(ctx, s, b)
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
	initial := PhysicalProof{}
	if g.Binding.Integration == portable.CopilotVSCode {
		// Local qualification requires full equality with the constructor proof;
		// the fresh lease check below validates that same baseline under the lease.
		initial = g.localInitial
	} else if g.Binding.Integration == portable.Cursor {
		_, initial, err = g.qualify(ctx, current, b)
		if err != nil {
			release()
			return nil, err
		}
	}
	lease := &authorityLease{gate: g, binding: b, channel: channel, expected: current, release: release, initial: initial}
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
	initial  PhysicalProof
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
	consent, proof, err := l.gate.qualify(ctx, l.expected, l.binding)
	if err != nil {
		return err
	}
	if proof != l.initial {
		return ErrDenied
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
