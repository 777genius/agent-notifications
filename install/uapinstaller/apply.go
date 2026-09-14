package uapinstaller

import (
	"context"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
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
		return result, err
	}
	if op == OpInstall {
		if _, err = e.helper(); err != nil {
			result = Result{Operation: OpInstall, Outcome: OutcomeIncomplete, Reason: err.Error()}
			return result, err
		}
	}
	if err = e.ensureDirs(); err != nil {
		result = Result{Operation: op, Outcome: OutcomeIncomplete, Reason: err.Error()}
		return result, err
	}
	switch op {
	case OpInstall:
		result, err = e.applyInstall(ctx, prepared)
	case OpRemove:
		result, err = e.applyRemove(ctx, prepared)
	default:
		err = fmt.Errorf("%w: %s", ErrUnsupported, op)
		return Result{}, err
	}
	return result, err
}

func (e *Engine) applyInstall(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	helper, err := e.helper()
	if err != nil {
		return Result{Operation: OpInstall, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	svc := e.lifecycle(helper, prepared.facts)
	added, err := svc.Add(ctx, usecase.AddInput{
		Envelope: prepared.envelope, Client: prepared.client, Scope: domain.ScopeUser, Confirmed: true,
		InstallationID: prepared.req.InstallationID, OperationID: prepared.req.OperationID,
		BackendExecutable: prepared.req.ClientExecutable,
	})
	result := Result{Operation: OpInstall, InstallationID: added.InstallationID, Binding: prepared.facts}
	if added.NoChange {
		result.Outcome = OutcomeUnchanged
		result.NoChange = true
	} else if err == nil {
		result.Outcome = OutcomeCompleted
	} else {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
	}
	if added.Activation.UserActions != nil {
		result.ManualActions = append([]string(nil), added.Activation.UserActions...)
	}
	state, loadErr := e.store.Load()
	if loadErr == nil {
		if installation, ok := findInstall(state, added.InstallationID); ok {
			if binding, receipt, ok := findBinding(installation, prepared.client.ClientID); ok {
				result.Binding = BindingFacts{
					InstallationID: added.InstallationID, ClientID: string(prepared.client.ClientID),
					BindingID: binding.ClientBindingID, Scope: binding.Scope, TargetPath: binding.TargetLocator,
					DataRoot: receipt.Locator, DataReceiptID: binding.DataReceiptID,
					OperationID: prepared.req.OperationID, TreeDigest: prepared.plan.TreeDigest,
				}
			}
		}
	}
	return result, err
}

func (e *Engine) applyRemove(ctx context.Context, prepared *PreparedOperation) (Result, error) {
	state, err := e.store.Load()
	if err != nil {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	installation, ok := findInstall(state, prepared.plan.InstallationID)
	if !ok {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: "installation is not installed"}, fmt.Errorf("%w: installation %s is not installed", ErrInvalidRequest, prepared.plan.InstallationID)
	}
	binding, _, ok := findBinding(installation, prepared.client.ClientID)
	if !ok {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: "client is not installed"}, fmt.Errorf("%w: client %s is not installed", ErrInvalidRequest, prepared.client.ClientID)
	}
	if err := e.removalPreflight(ctx, prepared.client, binding); err != nil {
		return Result{Operation: OpRemove, Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	helper, _ := e.helper()
	svc := e.lifecycle(helper, prepared.facts)
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
		}
	} else {
		result.Outcome = OutcomeIncomplete
		result.Reason = err.Error()
	}
	return result, err
}
