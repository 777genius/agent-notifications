package uapinstaller

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/sourceacquisition"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
)

// PreparedOperation owns a sealed source snapshot until Close or a terminal Apply.
type PreparedOperation struct {
	engine   *Engine
	mu       sync.Mutex
	closed   bool
	busy     bool
	applied  bool
	req      Request
	plan     Plan
	snapshot domain.PackageSnapshot
	envelope domain.PackageEnvelope
	client   domain.DetectedClient
	facts    BindingFacts
}

func (p *PreparedOperation) Plan() Plan {
	out := p.plan
	out.RequiredMissing = append([]string(nil), p.plan.RequiredMissing...)
	return out
}

func (p *PreparedOperation) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		return ErrHandleBusy
	}
	return p.closeLocked()
}

func (p *PreparedOperation) closeLocked() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.snapshot.Root == "" {
		return nil
	}
	return packagedigest.Remove(p.snapshot)
}

// Prepare captures a sealed snapshot for install or inspects owned state for remove.
// It does not mutate installed client config or the Notifications runtime ledger.
func (e *Engine) Prepare(ctx context.Context, req Request) (*PreparedOperation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	copied := req
	copied.RequiredComponents = append([]string(nil), req.RequiredComponents...)
	switch copied.Operation {
	case OpInstall:
		return e.prepareInstall(ctx, copied)
	case OpRemove:
		return e.prepareRemove(ctx, copied)
	case OpUpdate, OpRepair:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, copied.Operation)
	default:
		return nil, fmt.Errorf("%w: unknown operation", ErrInvalidRequest)
	}
}

func (e *Engine) prepareInstall(ctx context.Context, req Request) (*PreparedOperation, error) {
	client, err := detectedClient(req)
	if err != nil {
		return nil, err
	}
	if req.PackageRoot == "" || !validRoot(req.PackageRoot) {
		return nil, fmt.Errorf("%w: PackageRoot must be an explicit absolute clean path", ErrInvalidRequest)
	}
	if err := os.MkdirAll(e.cfg.TempRoot, 0700); err != nil {
		return nil, err
	}
	acq := sourceacquisition.Acquirer{TempRoot: e.cfg.TempRoot}
	snapshot, err := acq.AcquireLocal(ctx, req.PackageRoot)
	if err != nil {
		return nil, err
	}
	handle := &PreparedOperation{engine: e, req: req, snapshot: snapshot, client: client}
	ldr, err := newLoader()
	if err != nil {
		_ = handle.closeLocked()
		return nil, err
	}
	envelope, err := ldr.Load(ctx, domain.LoadInput{
		SnapshotRoot: snapshot.Root, TreeDigest: snapshot.TreeDigest,
		ExecutableFiles: snapshot.ExecutableFiles, Source: snapshot.Source,
	})
	if err != nil {
		_ = handle.closeLocked()
		return nil, err
	}
	handle.envelope = envelope
	svc := e.lifecycle(nil, BindingFacts{})
	preview, err := svc.Add(ctx, usecase.AddInput{
		Envelope: envelope, Client: client, Scope: domain.ScopeUser, DryRun: true, Confirmed: false,
		InstallationID: req.InstallationID, OperationID: req.OperationID, BackendExecutable: req.ClientExecutable,
	})
	if err != nil {
		_ = handle.closeLocked()
		return nil, err
	}
	missing := missingRequired(envelope, req.RequiredComponents)
	handle.plan = Plan{
		Operation: OpInstall, SourceRoot: req.PackageRoot, TreeDigest: snapshot.TreeDigest,
		DigestAlgorithm: snapshot.DigestAlgorithm, ClientID: string(client.ClientID),
		ConfigRoot: req.ClientConfigRoot, TargetPath: preview.Plan.ActivePath,
		InstallationID:  firstNonEmpty(req.InstallationID, preview.InstallationID),
		BindingID:       domain.ComputeClientBindingID(firstNonEmpty(req.InstallationID, preview.InstallationID), string(preview.Plan.ClientID), string(preview.Plan.Scope), preview.Plan.ActivePath),
		RequiredMissing: missing, NoChange: preview.NoChange,
	}
	handle.facts = BindingFacts{
		InstallationID: handle.plan.InstallationID, ClientID: handle.plan.ClientID,
		BindingID: handle.plan.BindingID, Scope: string(preview.Plan.Scope),
		TargetPath: handle.plan.TargetPath, OperationID: req.OperationID, TreeDigest: snapshot.TreeDigest,
	}
	if len(missing) != 0 {
		_ = handle.closeLocked()
		return nil, fmt.Errorf("%w: %v", ErrIncomplete, missing)
	}
	return handle, nil
}

func (e *Engine) prepareRemove(ctx context.Context, req Request) (*PreparedOperation, error) {
	client, err := detectedClient(req)
	if err != nil {
		return nil, err
	}
	selector := req.Selector
	if selector == "" {
		selector = req.InstallationID
	}
	if selector == "" {
		return nil, fmt.Errorf("%w: remove requires InstallationID or Selector", ErrInvalidRequest)
	}
	state, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstall(state, selector)
	if !ok {
		return nil, fmt.Errorf("%w: installation %s is not installed", ErrInvalidRequest, selector)
	}
	binding, receipt, ok := findBinding(installation, client.ClientID)
	if !ok {
		return nil, fmt.Errorf("%w: client %s is not installed", ErrInvalidRequest, client.ClientID)
	}
	handle := &PreparedOperation{engine: e, req: req, client: client}
	handle.plan = Plan{
		Operation: OpRemove, ClientID: string(client.ClientID), ConfigRoot: req.ClientConfigRoot,
		TargetPath: binding.TargetLocator, InstallationID: installation.InstallationID,
		BindingID: binding.ClientBindingID,
	}
	handle.facts = BindingFacts{
		InstallationID: installation.InstallationID, ClientID: string(client.ClientID),
		BindingID: binding.ClientBindingID, Scope: binding.Scope, TargetPath: binding.TargetLocator,
		DataRoot: receipt.Locator, DataReceiptID: binding.DataReceiptID, OperationID: req.OperationID,
	}
	return handle, nil
}

func detectedClient(req Request) (domain.DetectedClient, error) {
	var id domain.ClientID
	switch req.ClientID {
	case string(domain.ClientCodex):
		id = domain.ClientCodex
	case string(domain.ClientClaude):
		id = domain.ClientClaude
	default:
		return domain.DetectedClient{}, fmt.Errorf("%w: client %q is not in this beta", ErrUnsupported, req.ClientID)
	}
	if req.ClientConfigRoot == "" || !validRoot(req.ClientConfigRoot) {
		return domain.DetectedClient{}, fmt.Errorf("%w: ClientConfigRoot must be an explicit absolute clean path", ErrInvalidRequest)
	}
	if req.Operation == OpInstall && (req.ClientExecutable == "" || !validRoot(req.ClientExecutable)) {
		return domain.DetectedClient{}, fmt.Errorf("%w: ClientExecutable must be an explicit absolute clean path", ErrInvalidRequest)
	}
	return domain.DetectedClient{ClientID: id, Status: domain.DetectionDetected, ConfigRoot: req.ClientConfigRoot, ExecutablePath: req.ClientExecutable}, nil
}

func missingRequired(envelope domain.PackageEnvelope, required []string) []string {
	var missing []string
	for _, item := range required {
		switch item {
		case "mcp":
			if len(envelope.MCP.Servers) == 0 {
				missing = append(missing, item)
			}
		case "skills":
			if len(envelope.Skills) == 0 {
				missing = append(missing, item)
			}
		}
	}
	return missing
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func findInstall(state domain.StateFileV2, id string) (domain.Installation, bool) {
	for _, item := range state.Installations {
		if item.InstallationID == id {
			return item, true
		}
	}
	return domain.Installation{}, false
}

func findBinding(installation domain.Installation, client domain.ClientID) (domain.ClientBinding, domain.DataReceipt, bool) {
	for _, binding := range installation.Clients {
		if binding.ClientID != string(client) {
			continue
		}
		return binding, installation.DataReceipts[binding.DataReceiptID], true
	}
	return domain.ClientBinding{}, domain.DataReceipt{}, false
}
