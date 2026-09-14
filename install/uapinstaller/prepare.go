package uapinstaller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
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
	artifact string
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
	e.report(ProgressPrepare)
	snapshot, err := snapshotLocalPackage(ctx, e.cfg.TempRoot, req.PackageRoot)
	if err != nil {
		return nil, err
	}
	handle := &PreparedOperation{engine: e, req: req, snapshot: snapshot, client: client}
	if err := e.assessSnapshot(ctx, snapshot); err != nil {
		_ = handle.closeLocked()
		return nil, err
	}
	if err := e.refuseRecordedDigestRewrite(req.InstallationID, snapshot.TreeDigest); err != nil {
		_ = handle.closeLocked()
		return nil, err
	}
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
		return nil, wrapLifecycleError(err)
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
	handle.artifact = preview.Plan.PhysicalArtifactID
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
	e.report(ProgressPrepare)
	e.report(ProgressPreflight)
	if err := e.removalPreflight(ctx, client, binding); err != nil {
		return nil, err
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
	handle.artifact = binding.PhysicalArtifact
	return handle, nil
}

// removalPreflight is the §5.5.2 read-only check: exact target, persisted path,
// and managed artifact digest. It does not lock, recover, EnsureData, invoke a
// helper, or deactivate the client. Apply repeats it before UAP Remove.
func (e *Engine) removalPreflight(ctx context.Context, client domain.DetectedClient, binding domain.ClientBinding) error {
	digest := managedPackageDigest(binding)
	if digest == "" {
		return fmt.Errorf("%w: managed package digest is missing; refusing removal", ErrInvalidRequest)
	}
	target, err := (planner.Planner{ManagedRoot: e.cfg.ManagedRoot}).ResolveTarget(ctx, client, domain.ScopeUser, binding.PhysicalArtifact)
	if err != nil {
		return fmt.Errorf("%w: resolve managed removal target: %v", ErrInvalidRequest, err)
	}
	if err := pathpolicy.RequireExactPath(target.ActivePath, binding.TargetLocator); err != nil {
		return fmt.Errorf("%w: refuse removal from untrusted persisted target: %v", ErrInvalidRequest, err)
	}
	if err := (providers.Stager{}).Verify(ctx, binding.TargetLocator, digest); err != nil {
		return fmt.Errorf("%w: managed package was changed or is missing; refusing silent removal", ErrInvalidRequest)
	}
	return nil
}

func managedPackageDigest(client domain.ClientBinding) string {
	for _, object := range client.NativeObjects {
		if object.Kind == "managed_package_directory" && object.ManagedDigest != "" {
			return object.ManagedDigest
		}
	}
	return ""
}

// snapshotLocalPackage uses packagedigest executable overrides so Windows
// host FileMode (no 0111 on regular files) does not drop logical bin/ helpers
// from TreeDigest. AcquireLocal hashes POSIX bits from the checkout.
func snapshotLocalPackage(ctx context.Context, tempRoot, packageRoot string) (domain.PackageSnapshot, error) {
	absolute, err := filepath.Abs(packageRoot)
	if err != nil {
		return domain.PackageSnapshot{}, fmt.Errorf("acquire local package: resolve source failed")
	}
	executables, err := declaredPackageExecutables(absolute)
	if err != nil {
		return domain.PackageSnapshot{}, err
	}
	source := domain.SourceIdentity{RequestedSource: packageRoot, CanonicalSource: filepath.Clean(absolute), SourceBindingHint: "direct-local"}
	snapshot, err := (packagedigest.Builder{TempRoot: tempRoot}).SnapshotWithExecutables(ctx, absolute, source, executables)
	if err != nil {
		return domain.PackageSnapshot{}, fmt.Errorf("acquire local package: snapshot package content failed")
	}
	return snapshot, nil
}

func declaredPackageExecutables(root string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(rel string) {
		rel = path.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "./"))
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			return
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		if _, ok := seen[rel]; ok {
			return
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	raw, err := os.ReadFile(filepath.Join(root, "mcp.json"))
	if err == nil {
		var mcp struct {
			Servers map[string]struct {
				Command string `json:"command"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &mcp) == nil {
			for _, server := range mcp.Servers {
				add(server.Command)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "bin"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		add(path.Join("bin", entry.Name()))
	}
	return out, nil
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

func (e *Engine) assessSnapshot(ctx context.Context, snapshot domain.PackageSnapshot) error {
	if e.cfg.Assess == nil {
		return nil
	}
	got, err := e.cfg.Assess(ctx, snapshot.Root, snapshot.TreeDigest)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAssessmentRejected, err)
	}
	if got.TreeDigest != snapshot.TreeDigest {
		return fmt.Errorf("%w: assessment digest %s snapshot %s", ErrAssessmentRejected, got.TreeDigest, snapshot.TreeDigest)
	}
	if got.Outcome != AssessmentAllow {
		reason := got.Reason
		if reason == "" {
			reason = string(got.Outcome)
		}
		if reason == "" {
			reason = "unavailable"
		}
		return fmt.Errorf("%w: %s", ErrAssessmentRejected, reason)
	}
	return nil
}

func (e *Engine) refuseRecordedDigestRewrite(installationID, desired string) error {
	if installationID == "" || desired == "" {
		return nil
	}
	state, err := e.store.Load()
	if err != nil {
		return nil
	}
	installation, ok := findInstall(state, installationID)
	if !ok {
		return nil
	}
	recorded := installation.Source.TreeDigest
	if recorded != "" && recorded != desired {
		return fmt.Errorf("%w: recorded digest %s desired %s", ErrUpdateRequired, recorded, desired)
	}
	return nil
}

func wrapLifecycleError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "run update separately") ||
		strings.Contains(msg, "at a different revision; use update") ||
		strings.Contains(msg, "use switch to change source") {
		return fmt.Errorf("%w: %v", ErrUpdateRequired, err)
	}
	return err
}
