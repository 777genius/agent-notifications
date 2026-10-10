package copilotvscodeinstall

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"
)

// localProof only observes recorded ownership. Capture belongs to the separate
// new-owner setup boundary; neither recovery nor event input can replace it.
type localProof struct {
	cfg       uapinstaller.Config
	adapter   *vscode.LocalAdapter
	authority clients.PhysicalProfileAuthority
	engine    *uapinstaller.Engine
}

var _ ProofPort = (*localProof)(nil)

// Keep the selected revision, not the mutable desired package of a sibling.
func readLocalRecord(cfg uapinstaller.Config, b portable.Binding) (cursorRecord, error) {
	state, err := (statev2.Store{Path: cfg.StateFile}).Load()
	if err != nil {
		return cursorRecord{}, err
	}
	var found *domain.Installation
	for index := range state.Installations {
		i := &state.Installations[index]
		if i.InstallationID == b.InstallationID {
			if found != nil {
				return cursorRecord{}, ErrDenied
			}
			found = i
		}
	}
	if found == nil || found.NeedsRebind {
		return cursorRecord{}, ErrDenied
	}
	c, ok := found.Clients[b.BindingID]
	d, dataOK := found.DataReceipts[c.DataReceiptID]
	if !ok || !dataOK || c.ClientBindingID != b.BindingID || c.ClientID != string(domain.ClientVSCode) ||
		c.Scope != b.ScopeID || c.Scope != string(domain.ScopeUser) ||
		c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) ||
		c.NativeProfileRoot != b.ScopeRoot ||
		c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" ||
		c.Materialization != domain.MaterializationMaterialized || (c.Activation != domain.ActivationPrepared && c.Activation != domain.ActivationActive) ||
		c.InstallIntent != domain.InstallIntentAutomatic || c.Policy != domain.PolicyAllowed ||
		d.DataReceiptID != c.DataReceiptID || d.Locator != b.DataRoot || d.PhysicalBackend != c.PhysicalArtifact || d.Scope != c.Scope || d.State != domain.DataReceiptOwned ||
		c.PhysicalArtifact != domain.ComputePhysicalArtifactID(found.DeclaredName, found.InstallationID) ||
		c.SelectedDelivery.Validate() != nil || c.SelectedDelivery.ValidateClient(domain.ClientVSCode) != nil ||
		c.ValidateLocalEntryObservation() != nil || c.LocalEntryObservation == nil || !c.LocalEntryObservation.Facts().Enabled {
		return cursorRecord{}, ErrDenied
	}
	// Reading a recorded Local receipt is not a physical grant. Historical
	// Local records can lack the capability token; CheckLocal requires it before
	// any affirmative observation, and never captures a replacement.
	if c.ProfileAuthority == nil {
		if c.ProfileNamespace != "" {
			return cursorRecord{}, ErrDenied
		}
	} else if c.ProfileAuthority.IsZero() || c.ProfileNamespace != cfg.StateRoot || c.ProfileAuthority.Facts().CanonicalRoot != b.ScopeRoot {
		return cursorRecord{}, ErrDenied
	}
	f, local := c.SelectedDelivery.LocalFacts()
	if !local || f.ProfileRoot != b.ScopeRoot || f.ProfileIdentity != b.ScopeRoot ||
		f.SettingsPath != filepath.Join(b.ScopeRoot, "settings.json") || f.SettingsIdentity != f.SettingsPath ||
		f.Registration.Selector != c.TargetLocator || f.Registration.DesiredValue == nil || !*f.Registration.DesiredValue ||
		!reflect.DeepEqual(c.LocalEntryObservation.Facts().RevisionBasis, c.SelectedDelivery) ||
		!digest(strings.TrimPrefix(f.CanonicalDigest, "sha256:")) || !digest(strings.TrimPrefix(f.ProjectionDigest, "sha256:")) ||
		c.PackageRevision == nil || c.PackageRevision.TreeDigest != f.CanonicalDigest ||
		!strings.HasPrefix(c.PackageRevision.ManifestDigest, "sha256:") || !digest(strings.TrimPrefix(c.PackageRevision.ManifestDigest, "sha256:")) ||
		found.DeclaredName != found.Package.DeclaredName {
		return cursorRecord{}, ErrDenied
	}
	distribution := ""
	if found.Directory != nil {
		distribution = found.Directory.DistributionID
		if c.PackageRevision.DistributionID != distribution {
			return cursorRecord{}, ErrDenied
		}
	}
	packages, entries := 0, 0
	for _, o := range c.NativeObjects {
		if o.Kind == "managed_package_directory" {
			packages++
			if o != (domain.NativeObjectOwnership{ObjectID: "package:vscode:" + c.PhysicalArtifact, Kind: "managed_package_directory", LogicalName: found.DeclaredName, Path: c.TargetLocator, ManagedDigest: f.ProjectionDigest, ProtectionClass: "managed"}) {
				return cursorRecord{}, ErrDenied
			}
		} else {
			entries++
			if o != f.Registration.Ownership(f.SettingsPath) {
				return cursorRecord{}, ErrDenied
			}
		}
	}
	if packages != 1 || entries != 1 {
		return cursorRecord{}, ErrDenied
	}
	return cursorRecord{InstallationID: found.InstallationID, DeclaredName: found.DeclaredName, OriginMode: found.OriginMode,
		LoaderKind: found.Package.LoaderKind, FormatID: found.Package.FormatID, SchemaURI: found.Package.SchemaURI,
		DistributionID: distribution, Binding: c, Data: d}, nil
}

func (p *localProof) record(ctx context.Context, b portable.Binding) (cursorRecord, error) {
	view, err := p.engine.Inspect(ctx)
	if err != nil || view.StateRoot != p.cfg.StateRoot || view.Recovery.Required || len(view.Recovery.Journals) != 0 || len(view.Recovery.Receipts) != 0 || len(view.Recovery.NativeIntents) != 0 {
		return cursorRecord{}, ErrDenied
	}
	return readLocalRecord(p.cfg, b)
}

// LocalHookSpecs is the canonical installed Local Stop handoff used by setup
// and recorded proof. Rendering remains owned by the public projection renderer.
func LocalHookSpecs(b portable.Binding, executable string) []vscodelocalhooks.Spec {
	return localSpecs(b, executable)
}

func localSpecs(b portable.Binding, executable string) []vscodelocalhooks.Spec {
	return []vscodelocalhooks.Spec{{Event: vscodelocalhooks.Stop, Executable: executable,
		Args: []string{"copilot-vscode-event", "--event", "Stop", "--control-root", b.ControlRoot, "--binding", b.BindingID}, TimeoutSeconds: vscodelocalhooks.TimeoutSeconds}}
}

func (p *localProof) verify(ctx context.Context, b portable.Binding, r cursorRecord, primary string) error {
	if err := p.engine.VerifyProfileAuthority(ctx, b.InstallationID, b.BindingID); err != nil {
		return err
	}
	if err := p.authority.RevalidateProfileAuthority(ctx, domain.ClientVSCode, *r.Binding.ProfileAuthority); err != nil {
		return err
	}
	if err := p.adapter.ValidateBindingProfile(b.ScopeRoot, r.Binding); err != nil {
		return err
	}
	status, err := p.adapter.InspectRegistration(ctx, nativeconfig.New(), r.Binding.SelectedDelivery, r.Binding.NativeObjects)
	if err != nil || status != vscode.RegistrationActive {
		return ErrDenied
	}
	f, _ := r.Binding.SelectedDelivery.LocalFacts()
	stager := providers.Stager{Registry: p.cfg.Registry, Paths: pathpolicy.Policy{}, SnapshotBuilder: packagesnapshot.Builder{TempRoot: p.cfg.TempRoot}}
	if err := stager.Verify(ctx, r.Binding.TargetLocator, f.ProjectionDigest); err != nil {
		return err
	}
	if err := (providers.PluginDataManager{Base: p.cfg.PluginDataBase}).ValidateData(ctx, r.Data); err != nil {
		return err
	}
	if !f.NativeStop {
		return nil
	}
	hook, err := nativeconfig.New().ReadExactFile(filepath.Join(r.Binding.TargetLocator, filepath.FromSlash(vscodelocalhooks.PluginPath)))
	if err != nil || !hook.Exists {
		return ErrDenied
	}
	return vscodelocalhooks.VerifyOwned(hook.Body, vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(f.Tuple.TargetShell)}, localSpecs(b, primary))
}

func (p *localProof) CheckLocal(ctx context.Context, b portable.Binding, s installruntime.InstalledSnapshot) (PhysicalProof, error) {
	return p.checkSelectedLocal(ctx, b, s, true, false)
}

// QualifyLocalConsentFromSnapshot qualifies only a setup policy decision. It
// returns no native Gate or receipt. Manual enablement requires installed MCP;
// native enablement continues through the strict native Gate constructor.
func QualifyLocalConsentFromSnapshot(ctx context.Context, b portable.Binding, cfg uapinstaller.Config, s installruntime.PolicySnapshot, manual bool) error {
	if s.Installation.Ledger.WriterFloor < installruntime.LocalPolicyWriterFloor || !recorded(s, b) {
		return ErrDenied
	}
	p, err := newLocalProofFromSnapshot(ctx, b, cfg, s)
	if err != nil {
		return err
	}
	proof, err := p.checkSelectedLocal(ctx, b, s.Installation, false, manual)
	if err != nil || !proof.matches(b, s) {
		return ErrDenied
	}
	return nil
}

func (p *localProof) checkSelectedLocal(ctx context.Context, b portable.Binding, s installruntime.InstalledSnapshot, native, manual bool) (PhysicalProof, error) {
	if p == nil || p.authority == nil || p.engine == nil || ctx == nil || ctx.Err() != nil || b.Integration != portable.CopilotVSCode ||
		runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" || b.CheckSnapshot(s) != nil || s.Recovery || s.Ledger.PendingMutation != nil {
		return PhysicalProof{}, ErrDenied
	}
	registered, err := portable.ReadLocalBinding(b.ControlRoot, b.BindingID)
	if err != nil || registered != b {
		return PhysicalProof{}, ErrDenied
	}
	present, err := portable.ExactLocator(b)
	if err != nil || !present {
		return PhysicalProof{}, ErrDenied
	}
	primary, err := portable.ResolvePrimaryExecutable(s.Ledger, b.Primary)
	if err != nil {
		return PhysicalProof{}, err
	}
	n := s.Ledger.Native
	if n == nil || n.DecoderFloor != 1 || !digest(n.SHA256) {
		return PhysicalProof{}, ErrDenied
	}
	helper := installruntime.NativeGeneration{DirectoryID: n.DirectoryID, Path: n.Path, SHA256: n.SHA256, DecoderFloor: n.DecoderFloor, Attestation: n.Attestation, InstalledTreeSHA256: n.InstalledTreeSHA256}
	r, err := p.record(ctx, b)
	if err != nil {
		return PhysicalProof{}, err
	}
	if r.Binding.ProfileAuthority == nil || r.Binding.ProfileAuthority.IsZero() || r.Binding.ProfileNamespace != p.cfg.StateRoot {
		return PhysicalProof{}, ErrDenied
	}
	f, _ := r.Binding.SelectedDelivery.LocalFacts()
	if native && !f.NativeStop || manual && len(f.MCPServers) == 0 {
		return PhysicalProof{}, ErrDenied
	}
	// The adapter admits the reviewed version/qualification tuple. AN also fences
	// the sole Local production architecture. The public pin provides Darwin arm64
	// physical authority; source qualification cannot substitute for its recorded token.
	if f.Tuple != vscode.QualifiedDarwinTESTTuple() {
		return PhysicalProof{}, ErrDenied
	}
	observation, err := immutableObservation(struct {
		Record                                                           cursorRecord
		StateRoot, StateFile, OperationsDir, PluginDataBase, ManagedRoot string
		PrimaryPath                                                      string
		Primary                                                          installruntime.Identity
		Helper                                                           installruntime.NativeGeneration
	}{r, p.cfg.StateRoot, p.cfg.StateFile, p.cfg.OperationsDir, p.cfg.PluginDataBase, p.cfg.ManagedRoot, primary, s.Ledger.Files[primary], helper})
	if err != nil {
		return PhysicalProof{}, err
	}
	_, configIdentity, err := readLocalConfig(b)
	if err != nil {
		return PhysicalProof{}, err
	}
	if err := p.verify(ctx, b, r, primary); err != nil {
		return PhysicalProof{}, err
	}
	after, err := p.record(ctx, b)
	if err != nil || !reflect.DeepEqual(after, r) {
		return PhysicalProof{}, ErrDenied
	}
	if err := p.authority.RevalidateProfileAuthority(ctx, domain.ClientVSCode, *r.Binding.ProfileAuthority); err != nil {
		return PhysicalProof{}, err
	}
	current, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || !reflect.DeepEqual(current, s) {
		return PhysicalProof{}, ErrDenied
	}
	registered, err = portable.ReadLocalBinding(b.ControlRoot, b.BindingID)
	if err != nil || registered != b {
		return PhysicalProof{}, ErrDenied
	}
	present, err = portable.ExactLocator(b)
	if err != nil || !present {
		return PhysicalProof{}, ErrDenied
	}
	_, afterConfig, err := readLocalConfig(b)
	if err != nil || afterConfig != configIdentity || ctx.Err() != nil {
		return PhysicalProof{}, ErrDenied
	}
	return PhysicalProof{binding: b, physical: true, selectedLocalClass: true, qualifiedTuple: true, goos: runtime.GOOS, goarch: runtime.GOARCH,
		receiptDigest: rawDigest([]byte(observation)), packageDigest: strings.TrimPrefix(f.CanonicalDigest, "sha256:"), projectionDigest: strings.TrimPrefix(f.ProjectionDigest, "sha256:"),
		primaryDigest: s.Ledger.Files[primary].SHA256, helperDigest: n.SHA256, localObservation: observation, configObservation: configIdentity}, nil
}
