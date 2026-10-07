package copilotvscodeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"
)

// cursorProof owns no lease/store/pipeline. Only public observational APIs may
// produce these facts. In particular, delivery never captures a replacement
// token or substitutes the selected planned receipt for acknowledged ownership.
type cursorProof struct {
	cfg     uapinstaller.Config
	fixed   cursorinstall.Authority
	adapter *cursorinstall.Adapter
	engine  *uapinstaller.Engine
}

var _ ProofPort = (*cursorProof)(nil)

// cursorRecord carries only relevant public state. Encoding it to an immutable
// string fences selected linkage without freezing other installations globally.
type cursorRecord struct {
	InstallationID string
	DeclaredName   string
	OriginMode     domain.OriginMode
	LoaderKind     string
	FormatID       string
	SchemaURI      string
	DistributionID string
	Binding        domain.ClientBinding
	Data           domain.DataReceipt
}

func immutableObservation(value any) (string, error) {
	body, err := json.Marshal(value)
	return string(body), err
}

func (p *cursorProof) record(ctx context.Context, b portable.Binding) (cursorRecord, error) {
	view, err := p.engine.Inspect(ctx)
	if err != nil || view.StateRoot != p.cfg.StateRoot || view.Recovery.Required ||
		len(view.Recovery.Journals) != 0 || len(view.Recovery.Receipts) != 0 || len(view.Recovery.NativeIntents) != 0 {
		return cursorRecord{}, ErrDenied
	}
	state, err := (statev2.Store{Path: p.cfg.StateFile}).Load()
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
	if !ok || !dataOK || c.ClientBindingID != b.BindingID || c.ClientID != string(domain.ClientCursor) ||
		c.Scope != b.ScopeID || c.Scope != string(domain.ScopeUser) || c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) ||
		c.NativeProfileRoot != b.ScopeRoot || c.ProfileNamespace != p.cfg.StateRoot ||
		c.ProfileAuthority == nil || c.ProfileAuthority.IsZero() || c.ProfileAuthority.Facts().CanonicalRoot != b.ScopeRoot ||
		c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" || c.LocalEntryObservation != nil ||
		c.Materialization != domain.MaterializationMaterialized || (c.Activation != domain.ActivationPrepared && c.Activation != domain.ActivationActive) || c.InstallIntent != domain.InstallIntentAutomatic || c.Policy != domain.PolicyAllowed ||
		d.DataReceiptID != c.DataReceiptID || d.Locator != b.DataRoot || d.PhysicalBackend != c.PhysicalArtifact || d.Scope != c.Scope || d.State != domain.DataReceiptOwned ||
		c.PhysicalArtifact != domain.ComputePhysicalArtifactID(found.DeclaredName, found.InstallationID) {
		return cursorRecord{}, ErrDenied
	}
	if c.SelectedDelivery.Validate() != nil || c.SelectedDelivery.ValidateClient(domain.ClientCursor) != nil || c.SelectedDelivery.ValidateCursorObjects(c.NativeObjects) != nil {
		return cursorRecord{}, ErrDenied
	}
	f, selected := c.SelectedDelivery.CursorFacts()
	if !selected || f.CursorVersion != "2026.09.28-64d2043" || f.TargetOS != "linux" || f.TargetArch != "amd64" ||
		f.Shell != string(cursorhooks.LinuxUserShell32212) || f.ProfileRoot != b.ScopeRoot || f.ProfileIdentity != b.ScopeRoot ||
		f.HooksPath != filepath.Join(b.ScopeRoot, "hooks.json") || f.Selector != p.fixed.Selector || f.Executable != p.fixed.Executable ||
		f.QualificationID != p.fixed.QualificationID || f.ObjectID != p.fixed.ObjectID ||
		!digest(strings.TrimPrefix(f.CanonicalDigest, "sha256:")) || !digest(strings.TrimPrefix(f.ProjectionDigest, "sha256:")) {
		return cursorRecord{}, ErrDenied
	}
	// The selected binding is authoritative for its installed revision. Desired
	// Source/Package can advance independently when a sibling is updated.
	// Store.Load still validates provenance and Directory applied-release bounds.
	if c.PackageRevision == nil || c.PackageRevision.TreeDigest != f.CanonicalDigest ||
		!strings.HasPrefix(c.PackageRevision.ManifestDigest, "sha256:") ||
		!digest(strings.TrimPrefix(c.PackageRevision.ManifestDigest, "sha256:")) ||
		found.DeclaredName != found.Package.DeclaredName {
		return cursorRecord{}, ErrDenied
	}
	distributionID := ""
	if found.Directory != nil {
		distributionID = found.Directory.DistributionID
		if c.PackageRevision.DistributionID != distributionID {
			return cursorRecord{}, ErrDenied
		}
	}
	packages, hooks := 0, 0
	for _, object := range c.NativeObjects {
		switch object.Kind {
		case "managed_package_directory":
			packages++
			want := domain.NativeObjectOwnership{ObjectID: "package:cursor:" + c.PhysicalArtifact, Kind: "managed_package_directory",
				LogicalName: found.DeclaredName, Path: c.TargetLocator, ManagedDigest: f.ProjectionDigest, ProtectionClass: "managed"}
			if object != want {
				return cursorRecord{}, ErrDenied
			}
		case "cursor_user_stop":
			hooks++
		default:
			return cursorRecord{}, ErrDenied
		}
	}
	if packages != 1 || hooks != 1 {
		return cursorRecord{}, ErrDenied
	}
	return cursorRecord{InstallationID: found.InstallationID, DeclaredName: found.DeclaredName,
		OriginMode: found.OriginMode, LoaderKind: found.Package.LoaderKind, FormatID: found.Package.FormatID,
		SchemaURI: found.Package.SchemaURI, DistributionID: distributionID, Binding: c, Data: d}, nil
}

// readCursorConfig uses the bound path and explicit asset root only. No SDK,
// HOME, cwd, environment or default file discovery participates in consent.
func readCursorConfig(b portable.Binding) (*config.Config, string, error) {
	if b.Integration != portable.Cursor || b.GlobalConfig == "" || b.RuntimeRoot == "" {
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
	c, err := doc.Effective(config.AssetContext{Agent: config.AgentCursor, PluginRoot: b.RuntimeRoot})
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

func rawDigest(body []byte) string { return fmt.Sprintf("%x", sha256.Sum256(body)) }

func (p *cursorProof) verify(ctx context.Context, b portable.Binding, r cursorRecord) error {
	c := r.Binding
	if err := p.engine.VerifyProfileAuthority(ctx, b.InstallationID, b.BindingID); err != nil {
		return err
	}
	// Revalidate this original value as well as the engine's current record.
	if err := p.adapter.RevalidateProfileAuthority(ctx, domain.ClientCursor, *c.ProfileAuthority); err != nil {
		return err
	}
	if err := p.adapter.ValidateBindingProfile(b.ScopeRoot, c); err != nil {
		return err
	}
	detected := domain.DetectedClient{ClientID: domain.ClientCursor, ConfigRoot: b.ScopeRoot,
		ProfileNamespace: p.cfg.StateRoot, ProfileAuthority: domain.CloneProfileAuthority(c.ProfileAuthority)}
	plan := domain.DeliveryPlan{ClientID: domain.ClientCursor, Scope: domain.ScopeUser, ActivePath: c.TargetLocator,
		NativeRegistryRoot: b.ScopeRoot, SelectedDelivery: c.SelectedDelivery, PreviousNativeObjects: c.NativeObjects}
	finding, err := p.adapter.InspectNativeRegistry(ctx, clients.Env{NativeConfig: nativeconfig.New(), Paths: pathpolicy.Policy{}}, detected, plan, &c)
	if err != nil || finding != clients.RegistryExpected {
		return ErrDenied
	}
	f, _ := c.SelectedDelivery.CursorFacts()
	stager := providers.Stager{Registry: p.cfg.Registry, Paths: pathpolicy.Policy{}, SnapshotBuilder: packagesnapshot.Builder{TempRoot: p.cfg.TempRoot}}
	// Installation sealed canonical bytes and the complete selected revision.
	// Disposable acquisition roots are not live delivery authority. Independently
	// verify the actual installed projection; its digest is not canonical identity.
	if err := stager.Verify(ctx, c.TargetLocator, f.ProjectionDigest); err != nil {
		return err
	}
	if err := (providers.PluginDataManager{Base: p.cfg.PluginDataBase}).ValidateData(ctx, r.Data); err != nil {
		return err
	}
	return verifyCursorProjection(b, r)
}

// Cursor's actual projector writes mcp.json, not .mcp.json. Validate its owned
// portable selector and PLUGIN_DATA linkage independently of the tree digest.
func verifyCursorProjection(b portable.Binding, r cursorRecord) error {
	read := func(path string, value any) error {
		file, err := nativeconfig.New().ReadExactFile(path)
		if err != nil || !file.Exists || strictjson.Validate(file.Body, strictjson.Budget{Bytes: 65536, Depth: 8, Entries: 128}) != nil {
			return ErrDenied
		}
		return json.Unmarshal(file.Body, value)
	}
	var manifest map[string]json.RawMessage
	if read(filepath.Join(r.Binding.TargetLocator, ".cursor-plugin", "plugin.json"), &manifest) != nil {
		return ErrDenied
	}
	var manifestName, mcpFile string
	if json.Unmarshal(manifest["name"], &manifestName) != nil || manifestName != r.DeclaredName ||
		json.Unmarshal(manifest["mcpServers"], &mcpFile) != nil || mcpFile != "./mcp.json" {
		return ErrDenied
	}
	var document map[string]json.RawMessage
	var servers map[string]struct {
		Command, Cwd string
		Args         []string
		Env          map[string]string
	}
	if read(filepath.Join(r.Binding.TargetLocator, "mcp.json"), &document) != nil || json.Unmarshal(document["mcpServers"], &servers) != nil {
		return ErrDenied
	}
	server, ok := servers["agent-notify"]
	name, err := b.Filename()
	if !ok || err != nil || !reflect.DeepEqual(server.Args, []string{"portable-launch", "--locator", name}) ||
		server.Cwd != r.Binding.TargetLocator || server.Env["PLUGIN_ROOT"] != r.Binding.TargetLocator || server.Env["PLUGIN_DATA"] != b.DataRoot {
		return ErrDenied
	}
	return (pathpolicy.Policy{}).RequireContainedChild(r.Binding.TargetLocator, server.Command)
}

func (p *cursorProof) CheckLocal(ctx context.Context, b portable.Binding, s installruntime.InstalledSnapshot) (PhysicalProof, error) {
	if p == nil || ctx == nil || ctx.Err() != nil || b.Integration != portable.Cursor || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" ||
		b.CheckSnapshot(s) != nil || s.Recovery || s.Ledger.PendingMutation != nil {
		return PhysicalProof{}, ErrDenied
	}
	present, err := portable.ExactLocator(b)
	if err != nil || !present {
		return PhysicalProof{}, ErrDenied
	}
	primary, err := portable.ResolvePrimaryExecutable(s.Ledger, b.Primary)
	if err != nil || primary != p.fixed.Executable || "sha256:"+s.Ledger.Files[primary].SHA256 != p.fixed.ExecutableDigest {
		return PhysicalProof{}, ErrDenied
	}
	r, err := p.record(ctx, b)
	if err != nil {
		return PhysicalProof{}, err
	}
	initial, err := immutableObservation(struct {
		Record                                                                     cursorRecord
		StateRoot, StateFile, OperationsDir, PluginDataBase, ManagedRoot, TempRoot string
		Fixed                                                                      cursorinstall.Authority
	}{r, p.cfg.StateRoot, p.cfg.StateFile, p.cfg.OperationsDir, p.cfg.PluginDataBase, p.cfg.ManagedRoot, p.cfg.TempRoot, p.fixed})
	if err != nil {
		return PhysicalProof{}, err
	}
	_, cfgIdentity, err := readCursorConfig(b)
	if err != nil {
		return PhysicalProof{}, err
	}
	if err := p.verify(ctx, b, r); err != nil {
		return PhysicalProof{}, err
	}
	// Observe the same selected packages/receipt again; never recapture token.
	after, err := p.record(ctx, b)
	if err != nil {
		return PhysicalProof{}, err
	}
	if !reflect.DeepEqual(after, r) {
		return PhysicalProof{}, ErrDenied
	}
	if err := p.adapter.RevalidateProfileAuthority(ctx, domain.ClientCursor, *r.Binding.ProfileAuthority); err != nil {
		return PhysicalProof{}, err
	}
	present, err = portable.ExactLocator(b)
	if err != nil || !present {
		return PhysicalProof{}, ErrDenied
	}
	currentPrimary, err := portable.ResolvePrimaryExecutable(s.Ledger, b.Primary)
	if err != nil || currentPrimary != primary {
		return PhysicalProof{}, ErrDenied
	}
	_, configAfter, err := readCursorConfig(b)
	if err != nil || configAfter != cfgIdentity || ctx.Err() != nil {
		return PhysicalProof{}, ErrDenied
	}
	current, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || current.Ledger.Generation != s.Ledger.Generation || b.CheckSnapshot(current) != nil || current.Ledger.PendingMutation != nil {
		return PhysicalProof{}, ErrDenied
	}
	f, _ := r.Binding.SelectedDelivery.CursorFacts()
	return PhysicalProof{binding: b, physical: true, selectedCursorClass: true, qualifiedTuple: true,
		goos: runtime.GOOS, goarch: runtime.GOARCH, receiptDigest: rawDigest([]byte(initial)),
		packageDigest: strings.TrimPrefix(f.CanonicalDigest, "sha256:"), projectionDigest: strings.TrimPrefix(f.ProjectionDigest, "sha256:"),
		primaryDigest: s.Ledger.Files[primary].SHA256, cursorObservation: initial, configObservation: cfgIdentity}, nil
}
