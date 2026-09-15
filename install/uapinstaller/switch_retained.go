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
	saveErr := e.persistRetainedSource(ctx, req.InstallationID, envelope)
	recorded, digestErr := e.recordedTreeDigest(ctx, req.InstallationID)
	if saveErr != nil {
		if digestErr == nil && recorded == snapshot.TreeDigest {
			saveErr = e.persistRetainedSource(ctx, req.InstallationID, envelope)
			recorded, digestErr = e.recordedTreeDigest(ctx, req.InstallationID)
			if saveErr == nil && digestErr == nil && recorded == snapshot.TreeDigest {
				return e.retainedSourceUpdated(req, recorded), nil
			}
		}
		return switchRetainedError(req.InstallationID, saveErr)
	}
	if digestErr != nil || recorded != snapshot.TreeDigest {
		if retryErr := e.persistRetainedSource(ctx, req.InstallationID, envelope); retryErr != nil {
			if digestErr != nil {
				return switchRetainedError(req.InstallationID, digestErr)
			}
			return switchRetainedError(req.InstallationID, retryErr)
		}
		recorded, digestErr = e.recordedTreeDigest(ctx, req.InstallationID)
		if digestErr != nil || recorded != snapshot.TreeDigest {
			reason := "retained_source_not_durable"
			if digestErr != nil {
				reason = digestErr.Error()
			}
			return Result{Operation: OpUpdate, InstallationID: req.InstallationID, Outcome: OutcomeIncomplete, Reason: reason}, fmt.Errorf("%w: %s", ErrIncomplete, reason)
		}
	}
	return e.retainedSourceUpdated(req, recorded), nil
}

func (e *Engine) retainedSourceUpdated(req Request, digest string) Result {
	e.report(ProgressCommit)
	e.report(ProgressComplete)
	return Result{
		Operation: OpUpdate, InstallationID: req.InstallationID,
		Outcome: OutcomeCompleted, Binding: BindingFacts{
			InstallationID: req.InstallationID, TreeDigest: digest,
			OperationID: req.OperationID,
		},
	}
}

func (e *Engine) persistRetainedSource(ctx context.Context, installationID string, envelope domain.PackageEnvelope) error {
	_, err := e.lifecycle(nil, BindingFacts{}).SwitchRetained(ctx, usecase.BindingChangeInput{
		Selector: installationID, Envelope: envelope, Confirmed: true,
	}, domain.OriginModeDirect, nil)
	return err
}

func (e *Engine) recordedTreeDigest(ctx context.Context, installationID string) (string, error) {
	view, err := e.Inspect(ctx)
	if err != nil || view.Recovery.Required {
		reason := view.Recovery.Reason
		if reason == "" && err != nil {
			reason = err.Error()
		}
		if reason == "" {
			reason = "pending transactions remain"
		}
		return "", fmt.Errorf("%w: %s", ErrRecoveryRequired, reason)
	}
	for _, installation := range view.Installations {
		if installation.InstallationID == installationID {
			return installation.TreeDigest, nil
		}
	}
	return "", fmt.Errorf("%w: installation %s", ErrNotInstalled, installationID)
}

func switchRetainedError(installationID string, err error) (Result, error) {
	reason := err.Error()
	if strings.Contains(reason, "active installation switch requires SwitchGroup") {
		return Result{Outcome: OutcomeConflict, Reason: "active_installation"}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if strings.Contains(reason, "preserve manifest identity") {
		return Result{Outcome: OutcomeConflict, Reason: "package_identity"}, err
	}
	if strings.Contains(reason, "already bound to installation") {
		return Result{Outcome: OutcomeConflict, Reason: "source_collision"}, err
	}
	return Result{Operation: OpUpdate, InstallationID: installationID, Outcome: OutcomeIncomplete, Reason: reason}, err
}
