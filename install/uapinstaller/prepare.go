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
	engine    *Engine
	mu        sync.Mutex
	closed    bool
	busy      bool
	applied   bool
	req       Request
	plan      Plan
	snapshot  domain.PackageSnapshot
	snapshots []domain.PackageSnapshot
	envelope  domain.PackageEnvelope
	envelopes []domain.PackageEnvelope
	client    domain.DetectedClient
	clients   []domain.DetectedClient
	facts     BindingFacts
	artifact  string
}

func (p *PreparedOperation) Plan() Plan {
	out := p.plan
	out.RequiredMissing = append([]string(nil), p.plan.RequiredMissing...)
	if len(p.plan.Targets) > 0 {
		out.Targets = append([]PlanTarget(nil), p.plan.Targets...)
	}
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
	seen := map[string]bool{}
	var first error
	remove := func(snapshot domain.PackageSnapshot) {
		if snapshot.Root == "" || seen[snapshot.Root] {
			return
		}
		seen[snapshot.Root] = true
		if err := packagedigest.Remove(snapshot); err != nil && first == nil {
			first = err
		}
	}
	for _, snapshot := range p.snapshots {
		remove(snapshot)
	}
	remove(p.snapshot)
	return first
}

// Prepare captures a sealed snapshot for install or inspects owned state for remove.
// It does not mutate installed client config or the Notifications runtime ledger.
func (e *Engine) Prepare(ctx context.Context, req Request) (*PreparedOperation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	copied := req
	copied.RequiredComponents = append([]string(nil), req.RequiredComponents...)
	copied.Targets = append([]ClientTarget(nil), req.Targets...)
	if len(copied.Targets) > 1 {
		return e.prepareGroup(ctx, copied)
	}
	switch copied.Operation {
	case OpInstall:
		return e.prepareInstall(ctx, copied)
	case OpUpdate:
		return e.prepareUpdate(ctx, copied)
	case OpRepair:
		return e.prepareRepair(ctx, copied)
	case OpRemove:
		return e.prepareRemove(ctx, copied)
	default:
		return nil, fmt.Errorf("%w: unknown operation", ErrInvalidRequest)
	}
}

func (e *Engine) prepareInstall(ctx context.Context, req Request) (*PreparedOperation, error) {
	return e.prepareMutatingPackage(ctx, req, OpInstall, false, func(svc usecase.Service, in usecase.AddInput) (usecase.AddResult, error) {
		return svc.Add(ctx, in)
	})
}

func (e *Engine) prepareUpdate(ctx context.Context, req Request) (*PreparedOperation, error) {
	if _, _, err := e.requireExistingBinding(req); err != nil {
		return nil, err
	}
	return e.prepareMutatingPackage(ctx, req, OpUpdate, true, func(svc usecase.Service, in usecase.AddInput) (usecase.AddResult, error) {
		return e.updateWithCompatibility(ctx, svc, req, in)
	})
}

func (e *Engine) prepareRepair(ctx context.Context, req Request) (*PreparedOperation, error) {
	if _, _, err := e.requireExistingBinding(req); err != nil {
		return nil, err
	}
	return e.prepareMutatingPackage(ctx, req, OpRepair, false, func(svc usecase.Service, in usecase.AddInput) (usecase.AddResult, error) {
		return svc.Repair(ctx, in)
	})
}

func (e *Engine) requireExistingBinding(req Request) (domain.Installation, domain.ClientBinding, error) {
	client, err := detectedClient(req)
	if err != nil {
		return domain.Installation{}, domain.ClientBinding{}, err
	}
	if req.InstallationID == "" {
		return domain.Installation{}, domain.ClientBinding{}, fmt.Errorf("%w: InstallationID is required", ErrInvalidRequest)
	}
	state, err := e.store.Load()
	if err != nil {
		return domain.Installation{}, domain.ClientBinding{}, fmt.Errorf("%w: %v", ErrNotInstalled, err)
	}
	installation, ok := findInstall(state, req.InstallationID)
	if !ok {
		return domain.Installation{}, domain.ClientBinding{}, fmt.Errorf("%w: installation %s", ErrNotInstalled, req.InstallationID)
	}
	binding, _, ok := findBinding(installation, client.ClientID)
	if !ok {
		return domain.Installation{}, domain.ClientBinding{}, fmt.Errorf("%w: client %s", ErrNotInstalled, req.ClientID)
	}
	return installation, binding, nil
}

func (e *Engine) prepareMutatingPackage(ctx context.Context, req Request, op Operation, allowDigestRewrite bool, dry func(usecase.Service, usecase.AddInput) (usecase.AddResult, error)) (*PreparedOperation, error) {
	client, err := detectedClient(req)
	if err != nil {
		return nil, err
	}
	if req.PackageRoot == "" || !validRoot(req.PackageRoot) {
		return nil, fmt.Errorf("%w: PackageRoot must be an explicit absolute clean path", ErrInvalidRequest)
	}
	if overlappingRoots(e.cfg.TempRoot, req.PackageRoot) {
		return nil, fmt.Errorf("%w: TempRoot must not overlap PackageRoot", ErrInvalidRequest)
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
	if op == OpInstall {
		if err := e.refuseRecordedDigestRewrite(req.InstallationID, snapshot.TreeDigest); err != nil {
			_ = handle.closeLocked()
			return nil, err
		}
	}
	if op == OpRepair {
		if err := e.refuseRepairRevisionRewrite(req.InstallationID, string(client.ClientID), snapshot.TreeDigest); err != nil {
			_ = handle.closeLocked()
			return nil, err
		}
	}
	e.reuseMatchingSourceIdentity(req.InstallationID, &snapshot, allowDigestRewrite)
	handle.snapshot = snapshot
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
	helper, _ := e.helper()
	svc := e.lifecycle(helper, BindingFacts{})
	preview, err := dry(svc, usecase.AddInput{
		Envelope: envelope, Client: client, Scope: domain.ScopeUser, DryRun: true, Confirmed: false,
		PersistAuthoritativeObservations: e.persistObservations,
		InstallationID:                   req.InstallationID, OperationID: req.OperationID, BackendExecutable: req.ClientExecutable,
	})
	if err != nil {
		_ = handle.closeLocked()
		return nil, wrapLifecycleError(err)
	}
	missing := missingRequired(envelope, req.RequiredComponents)
	helperVersion, helperDigest := e.helperIdentity()
	handle.plan = Plan{
		Operation: op, SourceRoot: req.PackageRoot, TreeDigest: snapshot.TreeDigest,
		DigestAlgorithm: snapshot.DigestAlgorithm, ClientID: string(client.ClientID),
		ConfigRoot: req.ClientConfigRoot, TargetPath: preview.Plan.ActivePath,
		InstallationID: firstNonEmpty(req.InstallationID, preview.InstallationID),
		BindingID:      domain.ComputeClientBindingID(firstNonEmpty(req.InstallationID, preview.InstallationID), string(preview.Plan.ClientID), string(preview.Plan.Scope), preview.Plan.ActivePath),
		HelperVersion:  helperVersion, HelperDigest: helperDigest,
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
		e.report(ProgressPrepare)
		e.report(ProgressPreflight)
		handle := &PreparedOperation{engine: e, req: req, client: client}
		helperVersion, helperDigest := e.helperIdentity()
		handle.plan = Plan{
			Operation: OpRemove, ClientID: string(client.ClientID), ConfigRoot: req.ClientConfigRoot,
			InstallationID: installation.InstallationID, HelperVersion: helperVersion,
			HelperDigest: helperDigest, NoChange: true,
		}
		handle.facts = BindingFacts{
			InstallationID: installation.InstallationID, ClientID: string(client.ClientID),
			OperationID: req.OperationID,
		}
		return handle, nil
	}
	e.report(ProgressPrepare)
	e.report(ProgressPreflight)
	if err := e.removalPreflight(ctx, client, binding); err != nil {
		return nil, err
	}
	handle := &PreparedOperation{engine: e, req: req, client: client}
	helperVersion, helperDigest := e.helperIdentity()
	handle.plan = Plan{
		Operation: OpRemove, ClientID: string(client.ClientID), ConfigRoot: req.ClientConfigRoot,
		TargetPath: binding.TargetLocator, InstallationID: installation.InstallationID,
		BindingID: binding.ClientBindingID, HelperVersion: helperVersion, HelperDigest: helperDigest,
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

func (e *Engine) refuseRepairRevisionRewrite(installationID, clientID, desired string) error {
	if installationID == "" || clientID == "" || desired == "" {
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
	binding, _, ok := findBinding(installation, domain.ClientID(clientID))
	if !ok || binding.PackageRevision == nil || binding.PackageRevision.TreeDigest == "" {
		return nil
	}
	if binding.PackageRevision.TreeDigest != desired {
		return fmt.Errorf("%w: recorded digest %s desired %s", ErrUpdateRequired, binding.PackageRevision.TreeDigest, desired)
	}
	return nil
}

// reuseMatchingSourceIdentity keeps the recorded source binding when the new
// snapshot is the same logical source. Local CanonicalSource is a capture
// path, not revision identity: a same-bytes Add from a new directory must
// not become a source switch, and an authorized Update may change TreeDigest
// without rewriting SourceBindingID.
func (e *Engine) reuseMatchingSourceIdentity(installationID string, snapshot *domain.PackageSnapshot, allowDigestRewrite bool) {
	if installationID == "" || snapshot == nil || snapshot.TreeDigest == "" {
		return
	}
	state, err := e.store.Load()
	if err != nil {
		return
	}
	installation, ok := findInstall(state, installationID)
	if !ok || installation.Source.TreeDigest == "" {
		return
	}
	if !allowDigestRewrite && installation.Source.TreeDigest != snapshot.TreeDigest {
		return
	}
	if installation.Source.CanonicalSource != "" {
		snapshot.Source.CanonicalSource = installation.Source.CanonicalSource
	}
	if installation.Source.RequestedSource != "" {
		snapshot.Source.RequestedSource = installation.Source.RequestedSource
	}
	if installation.Source.Repository != "" {
		snapshot.Source.Repository = installation.Source.Repository
	}
	if installation.Source.PackageSubpath != "" {
		snapshot.Source.PackageSubpath = installation.Source.PackageSubpath
	}
	if installation.Source.ResolvedRevision != "" {
		snapshot.Source.ResolvedRevision = installation.Source.ResolvedRevision
	}
}

func wrapLifecycleError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "run update separately") ||
		strings.Contains(msg, "at a different revision; use update") ||
		strings.Contains(msg, "differs from the installed revision; use update") ||
		strings.Contains(msg, "use switch to change source") {
		return fmt.Errorf("%w: %v", ErrUpdateRequired, err)
	}
	if strings.Contains(msg, "is not bound to an existing installation") ||
		strings.Contains(msg, "resolved source is not bound to an installation") ||
		strings.Contains(msg, "plugin is not materialized") {
		return fmt.Errorf("%w: %v", ErrNotInstalled, err)
	}
	return err
}
