package copilotvscodeinstall

import (
	"context"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/cursorevent"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

// CursorGate is a closed typed consumer of the existing policy/effect owner.
// Its composition inputs grant no channel; each observation checks installed
// receipts and revalidates the original recorded physical token.
type CursorGate struct{ gate Gate }

var _ cursorevent.Gate = CursorGate{}

// NewCursorGate requires trusted, already qualified fixed vendor authority and
// an explicit installed layout. It neither captures authority nor prepares any
// live assets. Caller registry/callbacks are not retained by this observer.
func NewCursorGate(b portable.Binding, cfg uapinstaller.Config, fixed cursorinstall.Authority) (CursorGate, error) {
	if b.Integration != portable.Cursor {
		return CursorGate{}, ErrDenied
	}
	if _, _, _, err := b.Registration(); err != nil {
		return CursorGate{}, err
	}
	name, err := b.Filename()
	if err != nil || fixed.ProfileRoot != b.ScopeRoot || fixed.Selector != filepath.Join(b.DataRoot, name) {
		return CursorGate{}, ErrDenied
	}
	for _, path := range []string{cfg.StateRoot, cfg.StateFile, cfg.LockFile, cfg.OperationsDir, cfg.PluginDataBase, cfg.ManagedRoot, cfg.TempRoot} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return CursorGate{}, ErrDenied
		}
	}
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, &fixed)
	if err != nil {
		return CursorGate{}, err
	}
	registry, err := clients.NewRegistry(adapter)
	if err != nil {
		return CursorGate{}, err
	}
	// Copy only the read-only composition needed here, with no ambient probe env.
	owned := uapinstaller.Config{StateRoot: cfg.StateRoot, StateFile: cfg.StateFile, LockFile: cfg.LockFile,
		OperationsDir: cfg.OperationsDir, PluginDataBase: cfg.PluginDataBase, ManagedRoot: cfg.ManagedRoot,
		TempRoot: cfg.TempRoot, Registry: registry, TrustedLocalPackages: cfg.TrustedLocalPackages, OpenCodeProbeEnvironment: []string{"PATH="}}
	engine, err := uapinstaller.New(owned)
	if err != nil {
		return CursorGate{}, err
	}
	return CursorGate{gate: Gate{Binding: b, Proof: &cursorProof{cfg: owned, fixed: fixed, adapter: adapter, engine: engine}}}, nil
}

func localCursorBinding(b cursorevent.Binding) copilotvscodeevent.Binding {
	return copilotvscodeevent.Binding{InstallationID: b.InstallationID, BindingID: b.BindingID,
		ProfileIdentity: b.ProfileIdentity, Product: b.Product, Generation: b.Generation}
}

// ConsumerBinding copies all current identity and generation fields.
func (g CursorGate) ConsumerBinding(ctx context.Context) (cursorevent.Binding, error) {
	if ctx == nil || g.gate.Binding.Integration != portable.Cursor || g.gate.Proof == nil {
		return cursorevent.Binding{}, ErrDenied
	}
	b, err := g.gate.ConsumerBinding(ctx)
	if err != nil {
		return cursorevent.Binding{}, err
	}
	return cursorevent.Binding{InstallationID: b.InstallationID, BindingID: b.BindingID,
		ProfileIdentity: b.ProfileIdentity, Product: b.Product, Generation: b.Generation}, nil
}

func (g CursorGate) Channels(ctx context.Context, b cursorevent.Binding) cursorevent.Channels {
	if ctx == nil || g.gate.Binding.Integration != portable.Cursor || g.gate.Proof == nil {
		return cursorevent.Channels{}
	}
	c := g.gate.Channels(ctx, localCursorBinding(b))
	return cursorevent.Channels{Desktop: c.Desktop, Webhook: c.Webhook}
}

func (g CursorGate) Recheck(ctx context.Context, b cursorevent.Binding, channel cursorevent.Channel) bool {
	if ctx == nil || g.gate.Binding.Integration != portable.Cursor || g.gate.Proof == nil {
		return false
	}
	switch channel {
	case cursorevent.DesktopChannel:
		return g.gate.Recheck(ctx, localCursorBinding(b), copilotvscodeevent.DesktopChannel)
	case cursorevent.WebhookChannel:
		return g.gate.Recheck(ctx, localCursorBinding(b), copilotvscodeevent.WebhookChannel)
	default:
		return false
	}
}

// EffectiveConfig returns a fresh independent config for the same qualified
// registration and generation. Every effect check separately reloads opt-out.
func (g CursorGate) EffectiveConfig(ctx context.Context) (*config.Config, error) {
	if ctx == nil || g.gate.Binding.Integration != portable.Cursor || g.gate.Proof == nil {
		return nil, ErrDenied
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, g.gate.Binding.ControlRoot)
	if err != nil {
		return nil, err
	}
	_, proof, err := g.gate.qualify(ctx, s, consumerBinding(s, g.gate.Binding))
	if err != nil {
		return nil, err
	}
	c, identity, err := readCursorConfig(g.gate.Binding)
	if err != nil || identity != proof.configObservation {
		return nil, ErrDenied
	}
	current, err := installruntime.ReadInstalledSnapshot(g.gate.Binding.ControlRoot)
	if err != nil || current.Ledger.Generation != s.Installation.Ledger.Generation || g.gate.Binding.CheckSnapshot(current) != nil {
		return nil, ErrDenied
	}
	return c, nil
}
