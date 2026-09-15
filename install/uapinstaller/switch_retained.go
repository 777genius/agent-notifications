package uapinstaller

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
)

// SwitchRetained is the §5.5.1 retained-only metadata update. It revises
// source/digest for a data_retained installation with zero live bindings.
// Active installations must use Update. RequiredComponents are ignored: this
// step does not install clients.
func (e *Engine) SwitchRetained(ctx context.Context, req Request, decision Decision) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if req.InstallationID == "" {
		return Result{}, fmt.Errorf("%w: InstallationID is required", ErrInvalidRequest)
	}
	if req.PackageRoot == "" || !validRoot(req.PackageRoot) {
		return Result{}, fmt.Errorf("%w: PackageRoot must be an explicit absolute clean path", ErrInvalidRequest)
	}
	if overlappingRoots(e.cfg.TempRoot, req.PackageRoot) {
		return Result{}, fmt.Errorf("%w: TempRoot must not overlap PackageRoot", ErrInvalidRequest)
	}
	view, inspectErr := e.Inspect(ctx)
	if inspectErr != nil || view.Recovery.Required {
		reason := view.Recovery.Reason
		if reason == "" && inspectErr != nil {
			reason = inspectErr.Error()
		}
		if reason == "" {
			reason = "pending transactions remain"
		}
		return Result{Outcome: OutcomeRecovery, Reason: reason}, fmt.Errorf("%w: %s", ErrRecoveryRequired, reason)
	}
	state, err := e.store.Load()
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrNotInstalled, err)
	}
	installation, ok := findInstall(state, req.InstallationID)
	if !ok {
		return Result{Outcome: OutcomeIncomplete, Reason: "not_installed"}, fmt.Errorf("%w: installation %s", ErrNotInstalled, req.InstallationID)
	}
	if !installation.DataRetained || len(installation.Clients) != 0 {
		return Result{Outcome: OutcomeConflict, Reason: "active_installation"}, fmt.Errorf("%w: active installation switch requires SwitchGroup", ErrUnsupported)
	}
	if !decision.Confirmed {
		return Result{Operation: OpUpdate, InstallationID: req.InstallationID, Outcome: OutcomeCancelled, Reason: "host cancelled"}, ErrCancelled
	}
	if err := os.MkdirAll(e.cfg.TempRoot, 0700); err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	e.report(ProgressPrepare)
	snapshot, err := snapshotLocalPackage(ctx, e.cfg.TempRoot, req.PackageRoot)
	if err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	defer func() { _ = packagedigest.Remove(snapshot) }()
	if err := e.assessSnapshot(ctx, snapshot); err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	ldr, err := newLoader()
	if err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	envelope, err := ldr.Load(ctx, domain.LoadInput{
		SnapshotRoot: snapshot.Root, TreeDigest: snapshot.TreeDigest,
		ExecutableFiles: snapshot.ExecutableFiles, Source: snapshot.Source,
	})
	if err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	e.report(ProgressPreflight)
	svc := e.lifecycle(nil, BindingFacts{})
	got, err := svc.SwitchRetained(ctx, usecase.BindingChangeInput{
		Selector: req.InstallationID, Envelope: envelope, Confirmed: true,
	}, domain.OriginModeDirect, nil)
	if err != nil {
		reason := err.Error()
		if strings.Contains(reason, "active installation switch requires SwitchGroup") {
			return Result{Outcome: OutcomeConflict, Reason: "active_installation"}, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		if strings.Contains(reason, "preserve manifest identity") {
			return Result{Outcome: OutcomeConflict, Reason: "package_identity"}, err
		}
		return Result{Outcome: OutcomeIncomplete, Reason: reason}, err
	}
	result := Result{
		Operation: OpUpdate, InstallationID: req.InstallationID,
		Outcome: OutcomeCompleted, Binding: BindingFacts{
			InstallationID: req.InstallationID, TreeDigest: snapshot.TreeDigest,
			OperationID: req.OperationID,
		},
	}
	if got.NoChange {
		result.Outcome = OutcomeUnchanged
		result.NoChange = true
	}
	e.report(ProgressCommit)
	e.report(ProgressComplete)
	return result, nil
}
