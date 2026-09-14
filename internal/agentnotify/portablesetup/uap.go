package portablesetup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"

	"github.com/777genius/agent-notifications/install/uapinstaller"
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
	Identity            Identity
	Integration         portable.Integration
	ExpectedGeneration  uint64
	PackageRoot         string
	ClientConfigRoot    string
	ClientExecutable    string
	SourceRevision      string
	SourceDigest        string
	Discovery           Discovery
	OperationID         string
	HelperExecutable    string
	ExternalUninstalled bool
	// HoldOnly publishes the uninstall reservation and returns without locator
	// revoke or UAP mutation. Wizard uses it to keep a Codex removal pending
	// until the host attests ExternalUninstalled.
	HoldOnly bool
}

type Materializer struct {
	Kernel Service
	Store  statev2.Store
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
	if roots.HelperVersion == "" {
		roots.HelperVersion = "agent-notify-portable-v1"
	}
	return Materializer{Kernel: Service{}, Store: statev2.Store{Path: roots.StateFile}, Roots: roots}, nil
}

func findInstallation(state domain.StateFileV2, id string) (domain.Installation, bool) {
	for _, item := range state.Installations {
		if item.InstallationID == id {
			return item, true
		}
	}
	return domain.Installation{}, false
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}

func (m Materializer) validate(req MaterializeRequest, install bool) error {
	switch req.Integration {
	case portable.Codex, portable.Claude:
	default:
		return ErrPreflight
	}
	if !explicitAbs(req.ClientConfigRoot) {
		return fmt.Errorf("%w: client config root must be explicit", ErrPreflight)
	}
	if req.ClientExecutable == "" || !filepath.IsAbs(req.ClientExecutable) {
		return fmt.Errorf("%w: client executable must be explicit", ErrPreflight)
	}
	if install && !explicitAbs(req.PackageRoot) {
		return fmt.Errorf("%w: package root must be an explicit absolute path", ErrPreflight)
	}
	helper := m.Roots.HelperExecutable
	if req.HelperExecutable != "" {
		if !explicitAbs(req.HelperExecutable) {
			return fmt.Errorf("%w: helper override must be explicit", ErrPreflight)
		}
		helper = req.HelperExecutable
	}
	if !explicitAbs(helper) {
		return fmt.Errorf("%w: helper must be explicit", ErrPreflight)
	}
	return nil
}

func (m Materializer) engine(req MaterializeRequest, generation *uint64, res *installruntime.PendingMutation) (*uapinstaller.Engine, error) {
	helper := m.Roots.HelperExecutable
	if req.HelperExecutable != "" {
		helper = req.HelperExecutable
	}
	runner := m.Roots.ClaudeRunner
	if req.Integration != portable.Claude {
		runner = nil
	}
	return uapinstaller.New(uapinstaller.Config{
		StateRoot:        filepath.Dir(m.Roots.StateFile),
		StateFile:        m.Roots.StateFile,
		LockFile:         m.Roots.LockFile,
		OperationsDir:    m.Roots.OperationsDir,
		PluginDataBase:   m.Roots.PluginDataBase,
		ManagedRoot:      m.Roots.ManagedRoot,
		TempRoot:         filepath.Join(filepath.Dir(m.Roots.StateFile), "tmp"),
		HelperExecutable: helper,
		HelperVersion:    m.Roots.HelperVersion,
		Runner:           runner,
		ServerName:       portableServerName,
		ProjectArgs: func(facts uapinstaller.BindingFacts) ([]string, error) {
			b, err := Complete(req.Identity, req.Integration, facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
			if err != nil {
				return nil, err
			}
			name, err := b.Filename()
			if err != nil {
				return nil, err
			}
			return []string{"portable-launch", "--locator", name}, nil
		},
		OnCommittedBinding: func(ctx context.Context, facts uapinstaller.BindingFacts) error {
			pb, err := Complete(req.Identity, req.Integration, facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
			if err != nil {
				return err
			}
			if pb.BindingID != facts.BindingID && facts.BindingID != "" {
				return fmt.Errorf("%w: binding identity does not match committed UAP client", ErrPreflight)
			}
			if _, err := m.Kernel.CommitBinding(ctx, Request{Binding: pb, ExpectedGeneration: *generation, Reservation: res}); err != nil {
				return err
			}
			snap, err := installruntime.ReadInstalledSnapshot(pb.ControlRoot)
			if err != nil {
				return err
			}
			*generation = snap.Ledger.Generation
			return nil
		},
	})
}

func (m Materializer) apply(ctx context.Context, eng *uapinstaller.Engine, req uapinstaller.Request) (uapinstaller.Result, error) {
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return uapinstaller.Result{}, err
	}
	defer func() { _ = prepared.Close() }()
	return eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
}

func (m Materializer) Install(ctx context.Context, req MaterializeRequest) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := m.validate(req, true); err != nil {
		return portable.Binding{}, err
	}
	template, err := Complete(req.Identity, req.Integration, string(req.Integration), string(domain.ScopeUser), req.Identity.ScopeRoot, req.Identity.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if req.Discovery.ConfigPath != "" {
		release, err := installruntime.AcquireCoordinatorLease(ctx, req.Identity.ControlRoot)
		if err != nil {
			return portable.Binding{}, err
		}
		defer release()
		if _, err = installruntime.Recover(ctx, req.Identity.ControlRoot); err != nil {
			return portable.Binding{}, err
		}
		snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if err != nil {
			return portable.Binding{}, err
		}
		req.ExpectedGeneration = snap.Ledger.Generation
	}
	eng, err := m.engine(req, new(uint64), nil)
	if err != nil {
		return portable.Binding{}, err
	}
	if err := eng.Recover(ctx); err != nil {
		return portable.Binding{}, err
	}
	gen, res, err := m.Kernel.handoffForward(ctx, Request{
		Binding: template, ExpectedGeneration: req.ExpectedGeneration, Discovery: req.Discovery,
		SourceRevision: req.SourceRevision, SourceDigest: req.SourceDigest,
		Profile: req.ClientConfigRoot,
	})
	if err != nil {
		return portable.Binding{}, err
	}
	generation := gen
	eng, err = m.engine(req, &generation, res)
	if err != nil {
		return portable.Binding{}, err
	}
	result, err := m.apply(ctx, eng, uapinstaller.Request{
		Operation: uapinstaller.OpInstall, PackageRoot: req.PackageRoot, ClientID: string(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		return portable.Binding{}, wrapUpdateRequired(err)
	}
	pb, err := Complete(req.Identity, req.Integration, result.Binding.ClientID, result.Binding.Scope, result.Binding.TargetPath, result.Binding.DataRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if err := m.Kernel.finishHandoff(ctx, Request{Binding: pb, ExpectedGeneration: generation, Reservation: res}, res); err != nil {
		return portable.Binding{}, err
	}
	return pb, nil
}

func IsUpdateRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUpdateRequired) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "run update separately") ||
		strings.Contains(msg, "use switch to change source") ||
		strings.Contains(msg, "is sticky to")
}

func wrapUpdateRequired(err error) error {
	if err == nil {
		return nil
	}
	if IsUpdateRequired(err) && !errors.Is(err, ErrUpdateRequired) {
		return fmt.Errorf("%w: %v", ErrUpdateRequired, err)
	}
	return err
}

func (m Materializer) OtherLiveClients(installationID, adding string) ([]string, error) {
	if installationID == "" {
		return nil, nil
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstallation(state, installationID)
	if !ok {
		return nil, nil
	}
	var others []string
	for _, binding := range installation.Clients {
		if binding.ClientID != adding {
			others = append(others, binding.ClientID)
		}
	}
	return others, nil
}

func (m Materializer) PreviewInstall(ctx context.Context, req MaterializeRequest) (uapinstaller.Plan, error) {
	if ctx == nil {
		return uapinstaller.Plan{}, ErrPreflight
	}
	if err := m.validate(req, true); err != nil {
		return uapinstaller.Plan{}, err
	}
	eng, err := m.engine(req, new(uint64), nil)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	if err := eng.Recover(ctx); err != nil {
		return uapinstaller.Plan{}, err
	}
	prepared, err := eng.Prepare(ctx, uapinstaller.Request{
		Operation: uapinstaller.OpInstall, PackageRoot: req.PackageRoot, ClientID: string(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID + "-preview",
		RequiredComponents: []string{"mcp", "skills"},
	})
	if err != nil {
		return uapinstaller.Plan{}, wrapUpdateRequired(err)
	}
	defer func() { _ = prepared.Close() }()
	return prepared.Plan(), nil
}

func (m Materializer) GuardSecondClient(ctx context.Context, req MaterializeRequest) error {
	others, err := m.OtherLiveClients(req.Identity.InstallationID, string(req.Integration))
	if err != nil {
		return err
	}
	if len(others) == 0 {
		return nil
	}
	plan, err := m.PreviewInstall(ctx, req)
	if err != nil {
		return wrapUpdateRequired(err)
	}
	state, err := m.Store.Load()
	if err != nil {
		return err
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok {
		return nil
	}
	if installation.Source.TreeDigest != "" && plan.TreeDigest != "" && installation.Source.TreeDigest != plan.TreeDigest {
		return fmt.Errorf("%w: recorded digest %s desired %s", ErrUpdateRequired, installation.Source.TreeDigest, plan.TreeDigest)
	}
	recorded := installation.Source.CanonicalSource
	if recorded == "" {
		recorded = installation.Source.RequestedSource
	}
	if recorded != "" && plan.SourceRoot != "" && filepath.Clean(recorded) != filepath.Clean(plan.SourceRoot) {
		return fmt.Errorf("%w: existing source %s", ErrUpdateRequired, recorded)
	}
	return nil
}

func (m Materializer) Remove(ctx context.Context, req MaterializeRequest) error {
	if ctx == nil {
		return ErrPreflight
	}
	if err := m.validate(req, false); err != nil {
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
	if req.Discovery.ConfigPath != "" {
		release, err := installruntime.AcquireCoordinatorLease(ctx, req.Identity.ControlRoot)
		if err != nil {
			return err
		}
		defer release()
		if _, err = installruntime.Recover(ctx, req.Identity.ControlRoot); err != nil {
			return err
		}
		snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if err != nil {
			return err
		}
		req.ExpectedGeneration = snap.Ledger.Generation
	}
	eng, err := m.engine(req, &req.ExpectedGeneration, nil)
	if err != nil {
		return err
	}
	if err := eng.Recover(ctx); err != nil {
		return err
	}
	kernelReq := Request{
		Binding: pb, ExpectedGeneration: req.ExpectedGeneration, Discovery: req.Discovery,
		SourceRevision: req.SourceRevision, SourceDigest: req.SourceDigest, Profile: req.ClientConfigRoot,
	}
	res, err := m.Kernel.matchingReservation(kernelReq, "uninstall")
	if err != nil {
		return err
	}
	if res == nil {
		published, created, err := m.Kernel.publishIntent(ctx, kernelReq, req.ExpectedGeneration, "uninstall", "revoke-locator", []string{"direct-mcp"})
		if err != nil {
			return err
		}
		kernelReq.ExpectedGeneration = published.Generation
		req.ExpectedGeneration = published.Generation
		res = created
	}
	kernelReq.Reservation = res
	if req.HoldOnly {
		return fmt.Errorf("%w: %w", ErrPreflight, ErrExternalUninstall)
	}
	if err := m.Kernel.RevokeBinding(ctx, kernelReq); err != nil {
		return err
	}
	if _, err := m.apply(ctx, eng, uapinstaller.Request{
		Operation: uapinstaller.OpRemove, ClientID: string(req.Integration),
		ClientConfigRoot: req.ClientConfigRoot, ClientExecutable: req.ClientExecutable,
		InstallationID: req.Identity.InstallationID, OperationID: req.OperationID,
		ExternalUninstalled: req.ExternalUninstalled,
	}); err != nil {
		return err
	}
	if req.Discovery.ConfigPath == "" {
		return m.Kernel.finishHandoff(ctx, kernelReq, res)
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil {
		return err
	}
	reqBody := Request{Binding: pb, ExpectedGeneration: snap.Ledger.Generation, Discovery: req.Discovery, Reservation: res, Profile: req.ClientConfigRoot}
	if _, err = m.Kernel.HandoffReverse(ctx, reqBody); err != nil {
		return err
	}
	return m.Kernel.finishHandoff(ctx, reqBody, res)
}
