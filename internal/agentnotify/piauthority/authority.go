// Package piauthority binds Pi observations to the existing portable consumer,
// public UAP package/data receipts and one retained installed-policy lease.
package piauthority

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/pievent"
	"github.com/777genius/agent-notifications/internal/pisource"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/pi"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/shared"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
)

var ErrDenied = errors.New("pi installed authority denied")

type Assets struct{ RuntimeRoot, NativeHelper string }
type Gate struct {
	Binding        portable.Binding
	stateFile      string
	configIdentity installruntime.Identity
}

var _ pievent.Gate = (*Gate)(nil)

func stateFile(b portable.Binding) string {
	return filepath.Join(filepath.Dir(b.ControlRoot), "uap", "state", "state-v2.json")
}

// Open observes current authority for composition; it grants no effect lease.
func Open(ctx context.Context, selector string, revision pisource.Revision) (*Gate, pievent.Binding, *config.Config, Assets, error) {
	b, err := portable.ReadPiBinding(selector)
	if err != nil {
		return nil, pievent.Binding{}, nil, Assets{}, ErrDenied
	}
	g := &Gate{Binding: b, stateFile: stateFile(b)}
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		return nil, pievent.Binding{}, nil, Assets{}, err
	}
	current, cfg, assets, identity, err := g.qualify(ctx, s, revision, true)
	if err != nil {
		return nil, pievent.Binding{}, nil, Assets{}, err
	}
	g.configIdentity = identity
	return g, current, cfg, assets, nil
}

func (g *Gate) Channels(ctx context.Context, binding pievent.Binding, revision pisource.Revision) pievent.Channels {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return pievent.Channels{}
	}
	got, cfg, _, _, err := g.qualify(ctx, s, revision, false)
	if err != nil || got != binding {
		return pievent.Channels{}
	}
	return pievent.Channels{Desktop: cfg.IsDesktopEnabled(), Webhook: cfg.IsWebhookEnabled()}
}

// Acquire owns the only effect lease. No sender acquires another policy lease.
func (g *Gate) Acquire(ctx context.Context, binding pievent.Binding, revision pisource.Revision, channel pievent.Channel) (func(), error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, g.Binding.ControlRoot)
	if err != nil {
		return nil, err
	}
	current, release, err := installruntime.AcquirePolicyLease(ctx, g.Binding.ControlRoot, s)
	if err != nil {
		return nil, err
	}
	got, cfg, _, _, err := g.qualify(ctx, current, revision, false)
	allowed := err == nil && got == binding && (channel == pievent.DesktopChannel && cfg.IsDesktopEnabled() || channel == pievent.WebhookChannel && cfg.IsWebhookEnabled())
	if !allowed {
		release()
		return nil, ErrDenied
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}

func (g *Gate) qualify(ctx context.Context, snapshot installruntime.PolicySnapshot, expected pisource.Revision, initial bool) (pievent.Binding, *config.Config, Assets, installruntime.Identity, error) {
	deny := func(stage string) (pievent.Binding, *config.Config, Assets, installruntime.Identity, error) {
		return pievent.Binding{}, nil, Assets{}, installruntime.Identity{}, fmt.Errorf("%w: %s", ErrDenied, stage)
	}
	b := g.Binding
	if ctx == nil || ctx.Err() != nil || b.Integration != portable.Pi || snapshot.Installation.Ledger.PendingMutation != nil || b.CheckSnapshot(snapshot.Installation) != nil {
		return deny("policy-or-consumer")
	}
	name, err := b.Filename()
	if err != nil {
		return deny("locator-name")
	}
	read, err := portable.ReadPiBinding(filepath.Join(b.DataRoot, name))
	if err != nil || read != b {
		return deny("locator-bytes")
	}
	revision, _, err := installedRevision(ctx, b, g.stateFile, snapshot.Installation, true)
	if err != nil || revision != expected {
		return deny("installed-revision")
	}
	cfg, identity, err := readConfig(b)
	if err != nil || !initial && identity != g.configIdentity {
		return deny("effective-config")
	}
	desktop, webhook, present, err := channelChoices(snapshot.Fields["route"])
	if err != nil || !present {
		return deny("channel-consent")
	}
	cfg.Notifications.Desktop.Enabled = cfg.Notifications.Desktop.Enabled && desktop
	cfg.Notifications.Webhook.Enabled = cfg.Notifications.Webhook.Enabled && webhook
	assets := Assets{RuntimeRoot: b.RuntimeRoot}
	if runtime.GOOS == "darwin" {
		if snapshot.Installation.Ledger.Native == nil {
			return deny("native-helper")
		}
		assets.NativeHelper = filepath.Join(snapshot.Installation.Ledger.Native.Path, "Contents", "MacOS", "terminal-notifier-modern")
	}
	current, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || !reflect.DeepEqual(current, snapshot.Installation) || ctx.Err() != nil {
		return deny("installation-readback")
	}
	return pievent.Binding{InstallationID: b.InstallationID, BindingID: b.BindingID, Generation: current.Ledger.Generation}, cfg, assets, identity, nil
}

func readConfig(b portable.Binding) (*config.Config, installruntime.Identity, error) {
	before, err := installruntime.Fingerprint(b.GlobalConfig)
	if err != nil || before.Link != "" {
		return nil, installruntime.Identity{}, ErrDenied
	}
	file, err := nativeconfig.New().ReadExactFile(b.GlobalConfig)
	if err != nil {
		return nil, before, ErrDenied
	}
	body := file.Body
	if !file.Exists {
		body = []byte("{}")
	}
	doc, err := config.ParseDocument(body, b.GlobalConfig, file.Exists)
	if err != nil {
		return nil, before, err
	}
	cfg, err := doc.Effective(config.AssetContext{Agent: config.AgentPi, PluginRoot: b.RuntimeRoot, LookupEnv: os.LookupEnv})
	if err != nil {
		return nil, before, err
	}
	after, err := installruntime.Fingerprint(b.GlobalConfig)
	if err != nil || after != before {
		return nil, before, ErrDenied
	}
	return cfg, before, nil
}

// installedRevision is shared with publication but never reads pi-runtime.json.
// That file helps the producer find an executable; it grants no authority here.
func installedRevision(ctx context.Context, b portable.Binding, path string, snapshot installruntime.InstalledSnapshot, active bool) (pisource.Revision, domain.ClientBinding, error) {
	deny := func() (pisource.Revision, domain.ClientBinding, error) {
		return pisource.Revision{}, domain.ClientBinding{}, ErrDenied
	}
	if ctx == nil || ctx.Err() != nil || b.Integration != portable.Pi || path != stateFile(b) || b.CheckSnapshot(snapshot) != nil {
		return deny()
	}
	before, err := installruntime.Fingerprint(path)
	if err != nil || !before.Exists || before.Link != "" {
		return deny()
	}
	state, err := (statev2.Store{Path: path}).Load()
	if err != nil {
		return deny()
	}
	var selected *domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != b.InstallationID {
			continue
		}
		c, ok := installation.Clients[b.BindingID]
		if selected != nil || !ok || installation.NeedsRebind || c.ClientID != string(domain.ClientPi) || c.Scope != string(domain.ScopeUser) || c.Scope != b.ScopeID || c.ClientBindingID != b.BindingID || c.PackageRevision == nil || c.NativeActivationAttempt != "" || c.PendingNativeIntent != nil || c.Materialization != domain.MaterializationMaterialized {
			return deny()
		}
		verified := c.Verification == domain.VerificationInstalled || c.Verification == domain.VerificationPackageValid
		if active && (c.Activation != domain.ActivationActive || !verified || c.Policy != domain.PolicyAllowed) {
			return deny()
		}
		if c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) || pi.New().ValidateBindingProfile(c.NativeProfileRoot, c) != nil {
			return deny()
		}
		data, ok := installation.DataReceipts[c.DataReceiptID]
		base := filepath.Join(filepath.Dir(filepath.Dir(path)), "plugin-data")
		if !ok || data.Locator != b.DataRoot || data.Scope != c.Scope || data.PhysicalBackend != c.PhysicalArtifact || data.State != domain.DataReceiptOwned || (providers.PluginDataManager{Base: base}).ValidateData(ctx, data) != nil {
			return deny()
		}
		selected = &c
	}
	if selected == nil {
		return deny()
	}
	registry, err := clients.NewRegistry(pi.New())
	if err != nil {
		return deny()
	}
	engine, err := installer.New(installer.Config{StateRoot: filepath.Dir(path), Registry: registry})
	if err != nil || engine.VerifyProfileAuthority(ctx, b.InstallationID, b.BindingID) != nil {
		return deny()
	}
	projection := shared.ManagedPackageDigest(*selected)
	if err := (providers.Stager{}).Verify(ctx, selected.TargetLocator, projection); err != nil {
		return deny()
	}
	descriptorFile, err := nativeconfig.New().ReadExactFile(filepath.Join(selected.TargetLocator, pi.DescriptorName))
	if err != nil || !descriptorFile.Exists {
		return deny()
	}
	var descriptor pi.Descriptor
	if json.Unmarshal(descriptorFile.Body, &descriptor) != nil || descriptor.SchemaVersion != 1 || descriptor.BootstrapVersion != pi.BootstrapVersion || descriptor.ActivePath != selected.TargetLocator || descriptor.PackageRoot != selected.TargetLocator || descriptor.DataRoot != b.DataRoot || descriptor.CanonicalTreeDigest != selected.PackageRevision.TreeDigest {
		return deny()
	}
	executable, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, b.Primary)
	if err != nil {
		return deny()
	}
	primary, ok := snapshot.Ledger.Files[executable]
	if !ok {
		return deny()
	}
	after, err := installruntime.Fingerprint(path)
	if err != nil || after != before || ctx.Err() != nil {
		return deny()
	}
	return pisource.Revision{ProjectionDigest: projection, CanonicalDigest: selected.PackageRevision.TreeDigest, BootstrapVersion: pi.BootstrapVersion, BindingID: b.BindingID, RuntimeRoot: b.RuntimeRoot, PrimarySHA256: primary.SHA256}, *selected, nil
}
