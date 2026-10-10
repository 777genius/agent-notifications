package copilotvscodeinstall

import (
	"context"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

// NewLocalGate returns the existing effect owner and its frozen, qualified
// observations. It creates no profile authority, registration, assets or lease.
// Physical profile authority is limited to the qualified Darwin arm64 tuple.
// Positive proof requires the optional capability and persisted bound token.
func NewLocalGate(ctx context.Context, b portable.Binding, cfg uapinstaller.Config) (Gate, PhysicalProof, *config.Config, error) {
	deny := func() (Gate, PhysicalProof, *config.Config, error) { return Gate{}, PhysicalProof{}, nil, ErrDenied }
	if ctx == nil || ctx.Err() != nil || b.Integration != portable.CopilotVSCode {
		return deny()
	}
	if _, _, _, err := b.Registration(); err != nil {
		return deny()
	}
	for _, path := range []string{cfg.StateRoot, cfg.StateFile, cfg.LockFile, cfg.OperationsDir, cfg.PluginDataBase, cfg.ManagedRoot, cfg.TempRoot} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return deny()
		}
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || b.CheckSnapshot(snapshot) != nil {
		return deny()
	}
	r, err := readLocalRecord(cfg, b)
	if err != nil {
		return deny()
	}
	primary, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, b.Primary)
	if err != nil {
		return deny()
	}
	f, _ := r.Binding.SelectedDelivery.LocalFacts()
	target := vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(f.Tuple.TargetShell)}
	specs := localSpecs(b, primary)
	rendered, err := vscodelocalhooks.Render(target, specs)
	if err != nil {
		return deny()
	}
	adapter, err := vscode.NewLocal(vscode.LocalConfig{ProfileSettingsPath: f.SettingsPath, QualifiedTuple: f.Tuple, TargetShell: target, NativeStop: true,
		HookSpecs: specs, DeclaredHookDigest: "sha256:" + rawDigest(rendered), MCPServers: f.MCPServers, Skills: f.Skills})
	if err != nil {
		return deny()
	}
	authority, ok := any(adapter).(clients.PhysicalProfileAuthority)
	if !ok {
		return deny()
	}
	registry, err := clients.NewRegistry(adapter)
	if err != nil {
		return deny()
	}
	owned := uapinstaller.Config{StateRoot: cfg.StateRoot, StateFile: cfg.StateFile, LockFile: cfg.LockFile, OperationsDir: cfg.OperationsDir,
		PluginDataBase: cfg.PluginDataBase, ManagedRoot: cfg.ManagedRoot, TempRoot: cfg.TempRoot, Registry: registry, OpenCodeProbeEnvironment: []string{"PATH="}}
	engine, err := uapinstaller.New(owned)
	if err != nil {
		return deny()
	}
	p := &localProof{cfg: owned, adapter: adapter, authority: authority, engine: engine}
	proof, err := p.CheckLocal(ctx, b, snapshot)
	if err != nil {
		return deny()
	}
	g := Gate{Binding: b, Proof: p, localInitial: proof, localObserver: p}
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		return deny()
	}
	if _, _, err := g.qualify(ctx, s, consumerBinding(s, b)); err != nil {
		return deny()
	}
	effective, identity, err := readLocalConfig(b)
	if err != nil || identity != proof.configObservation {
		return deny()
	}
	return g, proof, effective, nil
}

// Explicit bound config readback; defaults and ambient discovery cannot opt in.
func readLocalConfig(b portable.Binding) (*config.Config, string, error) {
	if b.Integration != portable.CopilotVSCode || b.GlobalConfig == "" || b.RuntimeRoot == "" {
		return nil, "", ErrDenied
	}
	before, err := installruntime.Fingerprint(b.GlobalConfig)
	if err != nil || !before.Exists || before.Link != "" {
		return nil, "", ErrDenied
	}
	file, err := nativeconfig.New().ReadExactFile(b.GlobalConfig)
	if err != nil || !file.Exists || rawDigest(file.Body) != before.SHA256 {
		return nil, "", ErrDenied
	}
	doc, err := config.ParseDocument(file.Body, b.GlobalConfig, true)
	if err != nil {
		return nil, "", err
	}
	c, err := doc.Effective(config.AssetContext{Agent: config.AgentCopilotVSCode, PluginRoot: b.RuntimeRoot})
	if err != nil {
		return nil, "", err
	}
	after, err := installruntime.Fingerprint(b.GlobalConfig)
	if err != nil || before != after {
		return nil, "", ErrDenied
	}
	identity, err := immutableObservation(struct {
		Path, AssetRoot string
		Identity        installruntime.Identity
	}{b.GlobalConfig, b.RuntimeRoot, before})
	return c, identity, err
}
