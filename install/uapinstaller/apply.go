package uapinstaller

import (
	"context"
	"errors"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/usecase"
)

// Apply executes a prepared operation. A cancelled decision returns before usecase
// mutation, recovery, and callbacks. Result is populated even when err != nil.
// Close during Apply returns ErrHandleBusy without releasing the snapshot.
func (e *Engine) Apply(ctx context.Context, prepared *PreparedOperation, decision Decision) (result Result, err error) {
	if prepared == nil || prepared.engine != e {
		return Result{}, ErrInvalidHandle
	}
	prepared.mu.Lock()
	if prepared.closed {
		prepared.mu.Unlock()
		return Result{}, ErrHandleClosed
	}
	if prepared.applied {
		prepared.mu.Unlock()
		return Result{}, ErrAlreadyApplied
	}
	if prepared.busy {
		prepared.mu.Unlock()
		return Result{}, ErrHandleBusy
	}
	if !decision.Confirmed {
		op := prepared.req.Operation
		prepared.mu.Unlock()
		return Result{Operation: op, Outcome: OutcomeCancelled, Reason: "host cancelled"}, ErrCancelled
	}
	prepared.busy = true
	op := prepared.req.Operation
	prepared.mu.Unlock()
	defer func() {
		prepared.mu.Lock()
		prepared.busy = false
		if err == nil {
			prepared.applied = true
		}
		prepared.mu.Unlock()
	}()
	view, inspectErr := e.Inspect(ctx)
	if inspectErr != nil || view.Recovery.Required {
		reason := view.Recovery.Reason
		if reason == "" && inspectErr != nil {
			reason = inspectErr.Error()
		}
		if reason == "" {
			reason = "pending transactions remain"
		}
		result = Result{Operation: op, Outcome: OutcomeRecovery, Reason: reason}
		err = fmt.Errorf("%w: %s", ErrRecoveryRequired, reason)
		attachNextActions(&result)
		return result, err
	}
	if err = e.confirmPreparedPlan(ctx, prepared); err != nil {
		reason := "plan_changed"
		if errors.Is(err, ErrUpdateRequired) {
			reason = "update_required"
		}
		result = Result{Operation: op, Outcome: OutcomeConflict, Reason: reason}
		attachNextActions(&result)
		return result, err
	}
	if !prepared.plan.NoChange {
		if op == OpInstall || op == OpUpdate || op == OpRepair {
			if _, err = e.helper(); err != nil {
				result = Result{Operation: op, Outcome: OutcomeIncomplete, Reason: err.Error()}
				attachNextActions(&result)
				return result, err
			}
		}
		if err = e.ensureDirs(); err != nil {
			result = Result{Operation: op, Outcome: OutcomeIncomplete, Reason: err.Error()}
			attachNextActions(&result)
			return result, err
		}
	}
	e.report(ProgressPreflight)
	if len(prepared.req.Targets) > 1 {
		result, err = e.applyGroup(ctx, prepared)
	} else {
		switch op {
		case OpInstall:
			result, err = e.applyInstall(ctx, prepared)
		case OpUpdate:
			result, err = e.applyUpdate(ctx, prepared)
		case OpRepair:
			result, err = e.applyRepair(ctx, prepared)
		case OpRemove:
			result, err = e.applyRemove(ctx, prepared)
		default:
			err = fmt.Errorf("%w: %s", ErrUnsupported, op)
			return Result{}, err
		}
	}
	attachNextActions(&result)
	return result, err
}

func (e *Engine) applyInstall(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	return e.applyMutatingPackage(ctx, prepared, func(svc usecase.Service, in usecase.AddInput) (usecase.AddResult, error) {
		return svc.Add(ctx, in)
	})
}

func (e *Engine) applyUpdate(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	return e.applyMutatingPackage(ctx, prepared, func(svc usecase.Service, in usecase.AddInput) (usecase.AddResult, error) {
		return e.updateWithCompatibility(ctx, svc, prepared.req, in)
	})
}

func (e *Engine) applyRepair(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	return e.applyMutatingPackage(ctx, prepared, func(svc usecase.Service, in usecase.AddInput) (usecase.AddResult, error) {
		return svc.Repair(ctx, in)
	})
}

func (e *Engine) applyMutatingPackage(ctx context.Context, prepared *PreparedOperation, call func(usecase.Service, usecase.AddInput) (usecase.AddResult, error)) (Result, error) {
	if committed, binding, ok := e.liveBinding(prepared); ok && hostHandoffPending(binding) && e.cfg.OnCommittedBinding != nil {
		if err := e.cfg.OnCommittedBinding(ctx, committed.Binding); err != nil {
			committed.Outcome = OutcomeIncomplete
			committed.Reason = err.Error()
			e.report(ProgressCommit)
			return committed, err
		}
	}
	helper, err := e.helper()
	if err != nil {
		return Result{Operation: prepared.req.Operation, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	svc := e.lifecycle(helper, prepared.facts)
	e.report(ProgressStage)
	added, err := call(svc, usecase.AddInput{
		Envelope: prepared.envelope, Client: prepared.client, Scope: domain.ScopeUser, Confirmed: true,
		InstallationID: prepared.req.InstallationID, OperationID: prepared.req.OperationID,
		BackendExecutable: prepared.req.ClientExecutable,
	})
	err = wrapLifecycleError(err)
	result := Result{Operation: prepared.req.Operation, InstallationID: added.InstallationID, Binding: prepared.facts}
	if added.Activation.UserActions != nil {
		result.ManualActions = append([]string(nil), added.Activation.UserActions...)
	}
	committed := false
	if state, loadErr := e.store.Load(); loadErr == nil {
		installationID := firstNonEmpty(added.InstallationID, prepared.req.InstallationID)
		if installation, ok := findInstall(state, installationID); ok {
			if binding, receipt, ok := findBinding(installation, prepared.client.ClientID); ok {
				committed = true
				result.InstallationID = firstNonEmpty(installation.InstallationID, installationID)
				result.Binding = BindingFacts{
					InstallationID: result.InstallationID, ClientID: string(prepared.client.ClientID),
					BindingID: binding.ClientBindingID, Scope: binding.Scope, TargetPath: binding.TargetLocator,
					DataRoot: receipt.Locator, DataReceiptID: binding.DataReceiptID,
					OperationID: prepared.req.OperationID, TreeDigest: prepared.plan.TreeDigest,
				}
				result.Client = ClientResult{
					ClientID: binding.ClientID, BindingID: binding.ClientBindingID,
					Materialization: string(binding.Materialization), Activation: string(binding.Activation),
					Authentication: string(binding.Authentication), Verification: string(binding.Verification),
					RequiredComponents: append([]string(nil), prepared.req.RequiredComponents...),
				}
			}
		}
	} else if err == nil && !added.NoChange {
		result.Outcome = OutcomeIncomplete
		result.Reason = "committed state is unknown"
		result.Recovery.Unknown = []PendingReceipt{{
			OperationID: prepared.req.OperationID, InstallationID: added.InstallationID,
		}}
		return result, loadErr
	}
	if errors.Is(err, ErrUpdateRequired) {
		result.Outcome = OutcomeConflict
		result.Reason = "update_required"
		return result, err
	}
	if added.NoChange {
		result.Outcome = OutcomeUnchanged
		result.NoChange = true
	} else if err == nil {
		result.Outcome = OutcomeCompleted
		_ = storeLiveProfile(result.Binding.DataRoot, result.Binding.ClientID, prepared.req.ClientConfigRoot)
		e.report(ProgressCommit)
		e.report(ProgressActivate)
		e.report(ProgressVerify)
		e.report(ProgressComplete)
	} else {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
		if committed {
			e.report(ProgressCommit)
		}
	}
	return result, err
}

func (e *Engine) applyRemove(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	if prepared.plan.NoChange {
		return Result{
			Operation: OpRemove, InstallationID: prepared.plan.InstallationID,
			Outcome: OutcomeUnchanged, Reason: "already_absent", NoChange: true,
			DataRetained: true, Binding: prepared.facts,
		}, nil
	}
	state, err := e.store.Load()
	if err != nil {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	installation, ok := findInstall(state, prepared.plan.InstallationID)
	if !ok {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: "installation is not installed"}, fmt.Errorf("%w: installation %s is not installed", ErrInvalidRequest, prepared.plan.InstallationID)
	}
	binding, receipt, ok := findBinding(installation, prepared.client.ClientID)
	if !ok {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: "client is not installed"}, fmt.Errorf("%w: client %s is not installed", ErrInvalidRequest, prepared.client.ClientID)
	}
	if err := e.removalPreflight(ctx, prepared.client, binding, receipt); err != nil {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	helper, _ := e.helper()
	svc := e.lifecycle(helper, prepared.facts)
	e.report(ProgressStage)
	removed, err := svc.Remove(ctx, usecase.RemoveInput{
		Selector: prepared.plan.InstallationID, Client: prepared.client, Scope: domain.ScopeUser,
		Confirmed: true, OperationID: prepared.req.OperationID, BackendExecutable: prepared.req.ClientExecutable,
		ExternalUninstalled: prepared.req.ExternalUninstalled,
	})
	result := Result{Operation: OpRemove, InstallationID: removed.InstallationID, Binding: prepared.facts}
	if err == nil {
		result.Outcome = OutcomeCompleted
		if !removed.Mutated {
			result.Outcome = OutcomeUnchanged
			result.NoChange = true
		} else {
			e.report(ProgressCommit)
			e.report(ProgressActivate)
			e.report(ProgressVerify)
			e.report(ProgressComplete)
		}
		if view, inspectErr := e.observe(); inspectErr == nil {
			for _, installation := range view.Installations {
				if installation.InstallationID == result.InstallationID {
					result.DataRetained = installation.DataRetained
				}
			}
		}
	} else {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
	}
	return result, err
}

func (e *Engine) confirmPreparedPlan(ctx context.Context, prepared *PreparedOperation) error {
	plan := prepared.plan
	if len(plan.Targets) > 1 {
		return e.confirmGroupPlan(ctx, prepared)
	}
	if plan.Operation == OpInstall {
		if err := e.refuseRecordedDigestRewrite(plan.InstallationID, plan.TreeDigest); err != nil {
			return err
		}
		if prepared.artifact != "" {
			target, err := (planner.Planner{ManagedRoot: e.cfg.ManagedRoot}).ResolveTarget(ctx, prepared.client, domain.ScopeUser, prepared.artifact)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrPlanChanged, err)
			}
			if plan.TargetPath != "" && target.ActivePath != plan.TargetPath {
				return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
			}
		}
	}
	state, err := e.store.Load()
	if err != nil {
		return nil
	}
	installation, ok := findInstall(state, plan.InstallationID)
	if !ok {
		if plan.Operation == OpRemove {
			return fmt.Errorf("%w: installation is not installed", ErrPlanChanged)
		}
		return nil
	}
	binding, _, ok := findBinding(installation, prepared.client.ClientID)
	if plan.Operation == OpRemove {
		if plan.NoChange {
			if ok {
				return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
			}
			return nil
		}
		if !ok {
			return fmt.Errorf("%w: client is not installed", ErrPlanChanged)
		}
		if plan.BindingID != "" && binding.ClientBindingID != plan.BindingID {
			return fmt.Errorf("%w: live binding does not match confirmed plan", ErrPlanChanged)
		}
		if plan.TargetPath != "" && binding.TargetLocator != plan.TargetPath {
			return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
		}
		return nil
	}
	if !ok {
		return nil
	}
	if plan.TargetPath != "" && binding.TargetLocator != "" && binding.TargetLocator != plan.TargetPath {
		return fmt.Errorf("%w: live target does not match confirmed plan", ErrPlanChanged)
	}
	if plan.BindingID != "" && binding.ClientBindingID != "" && binding.ClientBindingID != plan.BindingID {
		return fmt.Errorf("%w: live binding does not match confirmed plan", ErrPlanChanged)
	}
	return nil
}

func (e *Engine) liveBinding(prepared *PreparedOperation) (Result, domain.ClientBinding, bool) {
	result := Result{Operation: OpInstall, Binding: prepared.facts}
	state, err := e.store.Load()
	if err != nil {
		return result, domain.ClientBinding{}, false
	}
	installationID := firstNonEmpty(prepared.req.InstallationID, prepared.plan.InstallationID)
	installation, ok := findInstall(state, installationID)
	if !ok {
		return result, domain.ClientBinding{}, false
	}
	binding, receipt, ok := findBinding(installation, prepared.client.ClientID)
	if !ok {
		return result, domain.ClientBinding{}, false
	}
	result.InstallationID = firstNonEmpty(installation.InstallationID, installationID)
	result.Binding = BindingFacts{
		InstallationID: result.InstallationID, ClientID: string(prepared.client.ClientID),
		BindingID: binding.ClientBindingID, Scope: binding.Scope, TargetPath: binding.TargetLocator,
		DataRoot: receipt.Locator, DataReceiptID: binding.DataReceiptID,
		OperationID: prepared.req.OperationID, TreeDigest: prepared.plan.TreeDigest,
	}
	result.Client = ClientResult{
		ClientID: binding.ClientID, BindingID: binding.ClientBindingID,
		Materialization: string(binding.Materialization), Activation: string(binding.Activation),
		Authentication: string(binding.Authentication), Verification: string(binding.Verification),
		RequiredComponents: append([]string(nil), prepared.req.RequiredComponents...),
	}
	return result, binding, true
}

func (e *Engine) updateWithCompatibility(ctx context.Context, svc usecase.Service, req Request, in usecase.AddInput) (usecase.AddResult, error) {
	checks := e.compatibilityChecks(req, in)
	if len(checks) == 0 {
		return usecase.AddResult{}, ErrCompatibilityUnavailable
	}
	if len(checks) == 1 {
		return svc.Update(ctx, in)
	}
	got, err := svc.UpdateGroup(ctx, usecase.GroupInput{
		Targets:             []usecase.AddInput{in},
		CompatibilityChecks: checks,
		OperationGroupID:    firstNonEmpty(req.OperationID, "update"),
		DryRun:              in.DryRun,
		Confirmed:           in.Confirmed,
	})
	if err != nil {
		return usecase.AddResult{InstallationID: got.InstallationID}, err
	}
	for _, target := range got.Targets {
		if target.Plan.ClientID == in.Client.ClientID || target.Plan.ClientID == domain.ClientID(req.ClientID) {
			return target, nil
		}
	}
	if len(got.Targets) > 0 {
		return got.Targets[0], nil
	}
	return usecase.AddResult{InstallationID: got.InstallationID, Mutated: got.Mutated}, nil
}

func (e *Engine) compatibilityChecks(req Request, target usecase.AddInput) []usecase.AddInput {
	if req.InstallationID == "" {
		return []usecase.AddInput{target}
	}
	state, err := e.store.Load()
	if err != nil {
		return nil
	}
	installation, ok := findInstall(state, req.InstallationID)
	if !ok {
		return []usecase.AddInput{target}
	}
	var checks []usecase.AddInput
	for _, binding := range installation.Clients {
		if binding.Materialization == domain.MaterializationAbsent {
			continue
		}
		configRoot := req.ClientConfigRoot
		if binding.ClientID != req.ClientID {
			receipt := installation.DataReceipts[binding.DataReceiptID]
			configRoot = liveProfile(receipt.Locator, binding.ClientID)
			if configRoot == "" {
				return nil
			}
		}
		client, err := detectedClient(Request{
			Operation: OpUpdate, ClientID: binding.ClientID, ClientConfigRoot: configRoot,
			ClientExecutable: req.ClientExecutable,
		})
		if err != nil {
			return nil
		}
		check := target
		check.Client = client
		checks = append(checks, check)
	}
	return checks
}

func attachNextActions(result *Result) {
	if result == nil || len(result.NextActions) > 0 {
		return
	}
	switch {
	case result.Outcome == OutcomeRecovery:
		result.NextActions = []NextAction{{Kind: "recover", Reason: result.Reason}}
	case result.Reason == "update_required":
		result.NextActions = []NextAction{{Kind: "update", Reason: result.Reason}}
	case result.Reason == "plan_changed":
		result.NextActions = []NextAction{{Kind: "reprepare", Reason: result.Reason}}
	case result.Outcome == OutcomeIncomplete && result.Client.Materialization != "" && result.Client.Materialization != string(domain.MaterializationAbsent):
		result.NextActions = []NextAction{{Kind: "activate", Reason: result.Reason}}
	}
}
