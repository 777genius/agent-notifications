package portablesetup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/loader"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/processlock"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/specregistry"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/transaction"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const portableServerName = "agent-notify"

// Identity is the operator-selected existing-installer surface. BindingID and
// DataRoot are completed from the committed UAP plan, not guessed from HOME.
type Identity struct {
	InstallationID, ComponentID, Owner, ScopeRoot, ControlRoot, GlobalConfig, RuntimeRoot, Primary string
}

// UAPRoots are explicit UAP state locations. None are derived from cwd/HOME.
type UAPRoots struct {
	StateFile, LockFile, OperationsDir, PluginDataBase, ManagedRoot string
	HelperExecutable, HelperVersion                                 string
	ClaudeRunner                                                    providers.CommandRunner
}

// MaterializeRequest selects one client. Integration is never taken from clientInfo.
type MaterializeRequest struct {
	Identity           Identity
	Integration        portable.Integration
	ExpectedGeneration uint64
	PackageRoot        string
	ClientConfigRoot   string
	ClientExecutable   string
	Discovery          Discovery
	OperationID        string
	HelperExecutable   string
}

type Materializer struct {
	Kernel Service
	UAP    usecase.Service
	Store  statev2.Store
	Data   providers.PluginDataManager
	Roots  UAPRoots
}

func physicalRoot(path string) string {
	got, err := installruntime.PhysicalPath(path)
	if err != nil {
		return path
	}
	return got
}

func Complete(id Identity, integration portable.Integration, clientID, scope, activePath, dataRoot string) (portable.Binding, error) {
	if scope == "" {
		scope = string(domain.ScopeUser)
	}
	b := portable.Binding{
		Version: 1, Integration: integration, InstallationID: id.InstallationID,
		BindingID: domain.ComputeClientBindingID(id.InstallationID, clientID, scope, activePath),
		ScopeID:   scope, ComponentID: id.ComponentID, Owner: id.Owner, ScopeRoot: physicalRoot(id.ScopeRoot),
		DataRoot: physicalRoot(dataRoot), ControlRoot: physicalRoot(id.ControlRoot), GlobalConfig: physicalRoot(id.GlobalConfig),
		RuntimeRoot: physicalRoot(id.RuntimeRoot), Primary: id.Primary,
	}
	if _, _, _, err := b.Registration(); err != nil {
		return portable.Binding{}, fmt.Errorf("%w: integration=%s bindingID=%s scopeID=%s dataRoot=%q activePath=%q primary=%q", err, integration, b.BindingID, b.ScopeID, dataRoot, activePath, id.Primary)
	}
	return b, nil
}

func NewMaterializer(roots UAPRoots) (Materializer, error) {
	for _, p := range []string{roots.StateFile, roots.LockFile, roots.OperationsDir, roots.PluginDataBase, roots.ManagedRoot, roots.HelperExecutable} {
		if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return Materializer{}, fmt.Errorf("%w: UAP roots must be explicit absolute paths", ErrPreflight)
		}
	}
	for _, dir := range []string{filepath.Dir(roots.StateFile), filepath.Dir(roots.LockFile), roots.OperationsDir, roots.PluginDataBase, roots.ManagedRoot} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return Materializer{}, err
		}
	}
	store := statev2.Store{Path: roots.StateFile}
	data := providers.PluginDataManager{Base: roots.PluginDataBase}
	plan := planner.Planner{ManagedRoot: roots.ManagedRoot}
	version := roots.HelperVersion
	if version == "" {
		version = "agent-notify-portable-v1"
	}
	roots.HelperVersion = version
	helper, err := managedstdio.NewSource(roots.HelperExecutable, version)
	if err != nil {
		return Materializer{}, err
	}
	stager := providers.Stager{LauncherSource: helper}
	activator := providers.Activator{Runner: roots.ClaudeRunner}
	svc := usecase.Service{
		StateStore: store, Planner: plan, Targets: plan, Stager: stager, Activator: activator,
		PluginData: data, Lock: processlock.Lock{Path: roots.LockFile},
		Kernel: transaction.Kernel{Directory: dirswap.Manager{JournalDir: roots.OperationsDir}},
	}
	return Materializer{Kernel: Service{}, UAP: svc, Store: store, Data: data, Roots: roots}, nil
}

func findInstallation(state domain.StateFileV2, id string) (domain.Installation, bool) {
	for _, item := range state.Installations {
		if item.InstallationID == id {
			return item, true
		}
	}
	return domain.Installation{}, false
}

func LoadPackage(ctx context.Context, root string) (domain.PackageEnvelope, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return domain.PackageEnvelope{}, fmt.Errorf("%w: package root must be an explicit absolute path", ErrPreflight)
	}
	reg, err := specregistry.New()
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	snap, err := (packagesnapshot.Builder{}).Build(ctx, root)
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	defer snap.Close()
	return (loader.Loader{Registry: reg}).Load(ctx, domain.LoadInput{
		SnapshotRoot: root, TreeDigest: snap.Digest,
		Source: domain.SourceIdentity{RequestedSource: root, CanonicalSource: root},
	})
}

func injectLocator(envelope domain.PackageEnvelope, name string) (domain.PackageEnvelope, error) {
	raw, err := json.Marshal(envelope.MCP.Servers)
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	var servers map[string]domain.MCPServer
	if err := json.Unmarshal(raw, &servers); err != nil {
		return domain.PackageEnvelope{}, err
	}
	server, ok := servers[portableServerName]
	if !ok {
		return domain.PackageEnvelope{}, fmt.Errorf("%w: package is missing %s MCP server", ErrPreflight, portableServerName)
	}
	if server.Decoded == nil {
		server.Decoded = map[string]any{}
	}
	server.Decoded["args"] = []any{"portable-launch", "--locator", name}
	server.Raw, err = json.Marshal(server.Decoded)
	if err != nil {
		return domain.PackageEnvelope{}, err
	}
	servers[portableServerName] = server
	envelope.MCP.Servers = servers
	return envelope, nil
}

type locatorStager struct {
	providers.Stager
	identity    Identity
	integration portable.Integration
}

func (s locatorStager) Stage(context.Context, domain.PackageEnvelope, domain.DeliveryPlan, string, domain.CompatibilityHints) (domain.StagedDelivery, error) {
	return domain.StagedDelivery{}, fmt.Errorf("%w: locator projection requires owned plugin data", ErrPreflight)
}

func (s locatorStager) StageWithPluginData(ctx context.Context, envelope domain.PackageEnvelope, plan domain.DeliveryPlan, operationID string, hints domain.CompatibilityHints, data string) (domain.StagedDelivery, error) {
	b, err := Complete(s.identity, s.integration, string(plan.ClientID), string(plan.Scope), plan.ActivePath, data)
	if err != nil {
		return domain.StagedDelivery{}, err
	}
	name, err := b.Filename()
	if err != nil {
		return domain.StagedDelivery{}, err
	}
	envelope, err = injectLocator(envelope, name)
	if err != nil {
		return domain.StagedDelivery{}, err
	}
	return s.Stager.StageWithPluginData(ctx, envelope, plan, operationID, hints, data)
}

type locatorActivator struct {
	inner       providers.Activator
	store       statev2.Store
	data        providers.PluginDataManager
	identity    Identity
	integration portable.Integration
	kernel      Service
	generation  *uint64
}

func (a locatorActivator) Activate(ctx context.Context, request domain.ActivationRequest) (domain.ActivationOutcome, error) {
	state, err := a.store.Load()
	if err != nil {
		return domain.ActivationOutcome{}, err
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != a.identity.InstallationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.TargetLocator != request.Plan.ActivePath || binding.ClientID != string(request.Plan.ClientID) {
				continue
			}
			receipt, ok := installation.DataReceipts[binding.DataReceiptID]
			if !ok {
				return domain.ActivationOutcome{}, fmt.Errorf("%w: missing committed data receipt", ErrPreflight)
			}
			if err := a.data.ValidateData(ctx, receipt); err != nil {
				return domain.ActivationOutcome{}, err
			}
			pb, err := Complete(a.identity, a.integration, binding.ClientID, binding.Scope, request.Plan.ActivePath, receipt.Locator)
			if err != nil {
				return domain.ActivationOutcome{}, err
			}
			if pb.BindingID != binding.ClientBindingID {
				return domain.ActivationOutcome{}, fmt.Errorf("%w: binding identity does not match committed UAP client", ErrPreflight)
			}
			if _, err := a.kernel.CommitBinding(ctx, Request{Binding: pb, ExpectedGeneration: *a.generation}); err != nil {
				return domain.ActivationOutcome{}, err
			}
			snap, err := installruntime.ReadInstalledSnapshot(pb.ControlRoot)
			if err != nil {
				return domain.ActivationOutcome{}, err
			}
			*a.generation = snap.Ledger.Generation
			return a.inner.Activate(ctx, request)
		}
	}
	return domain.ActivationOutcome{}, fmt.Errorf("%w: committed binding not found", ErrPreflight)
}

func (a locatorActivator) Deactivate(ctx context.Context, request domain.DeactivationRequest) (domain.DeactivationOutcome, error) {
	return a.inner.Deactivate(ctx, request)
}

func (m Materializer) composed(req MaterializeRequest, generation *uint64) usecase.Service {
	helper := m.Roots.HelperExecutable
	if req.HelperExecutable != "" {
		helper = req.HelperExecutable
	}
	source, err := managedstdio.NewSource(helper, m.Roots.HelperVersion)
	if err != nil {
		source, _ = managedstdio.NewSource(m.Roots.HelperExecutable, m.Roots.HelperVersion)
	}
	stager := locatorStager{Stager: providers.Stager{LauncherSource: source}, identity: req.Identity, integration: req.Integration}
	inner := providers.Activator{}
	if req.Integration == portable.Claude {
		inner.Runner = m.Roots.ClaudeRunner
	}
	activator := locatorActivator{
		inner: inner, store: m.Store, data: m.Data, identity: req.Identity,
		integration: req.Integration, kernel: m.Kernel, generation: generation,
	}
	svc := m.UAP
	svc.Stager = stager
	svc.Activator = activator
	return svc
}

func (m Materializer) client(req MaterializeRequest) (domain.DetectedClient, error) {
	var id domain.ClientID
	switch req.Integration {
	case portable.Codex:
		id = domain.ClientCodex
	case portable.Claude:
		id = domain.ClientClaude
	default:
		return domain.DetectedClient{}, ErrPreflight
	}
	if req.ClientConfigRoot == "" || !filepath.IsAbs(req.ClientConfigRoot) || filepath.Clean(req.ClientConfigRoot) != req.ClientConfigRoot {
		return domain.DetectedClient{}, fmt.Errorf("%w: client config root must be explicit", ErrPreflight)
	}
	if req.ClientExecutable == "" || !filepath.IsAbs(req.ClientExecutable) {
		return domain.DetectedClient{}, fmt.Errorf("%w: client executable must be explicit", ErrPreflight)
	}
	return domain.DetectedClient{ClientID: id, Status: domain.DetectionDetected, ConfigRoot: req.ClientConfigRoot, ExecutablePath: req.ClientExecutable}, nil
}

func (m Materializer) Install(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	template, err := Complete(req.Identity, req.Integration, string(req.Integration), string(domain.ScopeUser), req.Identity.ScopeRoot, req.Identity.ControlRoot)
	if err != nil {
		// BindingID/DataRoot placeholders must still be valid absolute paths for Complete;
		// ScopeRoot/ControlRoot are operator paths used only to pass Registration() here.
		return portable.Binding{}, err
	}
	gen, err := m.Kernel.handoffForward(ctx, Request{
		Binding: template, ExpectedGeneration: req.ExpectedGeneration, Discovery: req.Discovery,
	})
	if err != nil {
		return portable.Binding{}, err
	}
	envelope, err := LoadPackage(ctx, req.PackageRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	client, err := m.client(req)
	if err != nil {
		return portable.Binding{}, err
	}
	generation := gen
	svc := m.composed(req, &generation)
	result, err := svc.Add(ctx, usecase.AddInput{
		Envelope: envelope, Client: client, Scope: domain.ScopeUser, Confirmed: true,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		BackendExecutable: req.ClientExecutable,
	})
	if err != nil {
		return portable.Binding{}, err
	}
	state, err := m.Store.Load()
	if err != nil {
		return portable.Binding{}, err
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok {
		return portable.Binding{}, fmt.Errorf("%w: UAP installation missing after add", ErrPreflight)
	}
	for _, binding := range installation.Clients {
		if binding.TargetLocator != result.Plan.ActivePath || binding.ClientID != string(result.Plan.ClientID) {
			continue
		}
		receipt, ok := installation.DataReceipts[binding.DataReceiptID]
		if !ok {
			return portable.Binding{}, fmt.Errorf("%w: UAP data receipt missing after add", ErrPreflight)
		}
		return Complete(req.Identity, req.Integration, binding.ClientID, binding.Scope, result.Plan.ActivePath, receipt.Locator)
	}
	return portable.Binding{}, fmt.Errorf("%w: UAP client binding missing after add", ErrPreflight)
}

func (m Materializer) Remove(ctx context.Context, req MaterializeRequest) error {
	if ctx == nil {
		return ErrPreflight
	}
	client, err := m.client(req)
	if err != nil {
		return err
	}
	state, err := m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok {
		return fmt.Errorf("%w: portable binding is not installed", ErrPreflight)
	}
	var found *domain.ClientBinding
	var receipt domain.DataReceipt
	for _, binding := range installation.Clients {
		if binding.ClientID != string(req.Integration) {
			continue
		}
		item := binding
		found = &item
		receipt = installation.DataReceipts[binding.DataReceiptID]
	}
	if found == nil {
		return fmt.Errorf("%w: portable binding is not installed", ErrPreflight)
	}
	pb, err := Complete(req.Identity, req.Integration, found.ClientID, found.Scope, found.TargetLocator, receipt.Locator)
	if err != nil {
		return err
	}
	if err := m.Kernel.RevokeBinding(ctx, Request{Binding: pb, ExpectedGeneration: req.ExpectedGeneration}); err != nil {
		return err
	}
	svc := m.UAP
	svc.Activator = providers.Activator{Runner: m.Roots.ClaudeRunner}
	_, err = svc.Remove(ctx, usecase.RemoveInput{
		Selector: req.Identity.InstallationID, Client: client, Scope: domain.ScopeUser,
		Confirmed: true, OperationID: req.OperationID, BackendExecutable: req.ClientExecutable,
	})
	if err != nil {
		return err
	}
	if req.Discovery.ConfigPath == "" {
		return nil
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil {
		return err
	}
	_, err = m.Kernel.HandoffReverse(ctx, Request{
		Binding: pb, ExpectedGeneration: snap.Ledger.Generation, Discovery: req.Discovery,
	})
	return err
}
