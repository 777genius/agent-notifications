package cursorinstall

import (
	"context"
	"errors"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func (a *Adapter) AutomaticallyActivates(_ clients.Env, req domain.ActivationRequest) bool {
	_, selected := req.Plan.SelectedDelivery.CursorFacts()
	return selected && req.Plan.InstallIntent == domain.InstallIntentAutomatic
}

func (a *Adapter) VerifierAvailable(_ domain.DetectedClient, plan domain.DeliveryPlan, _ string) bool {
	_, selected := plan.SelectedDelivery.CursorFacts()
	return selected
}

func (a *Adapter) PreflightActivation(env clients.Env, req domain.ActivationRequest) error {
	if req.Plan.SelectedDelivery.IsZero() {
		return nil // Historical Cursor has no activation preflight.
	}
	f, err := a.recorded(req.Plan.SelectedDelivery)
	if err != nil {
		return err
	}
	if err := a.requireToken(req.Client); err != nil {
		return err
	}
	if err := req.Plan.SelectedDelivery.ValidatePlan(req.Plan, f.CanonicalDigest); err != nil {
		return err
	}
	if err := a.verifyInvocation(); err != nil {
		return err
	}
	original, err := env.NativeConfig.ReadExactFile(f.HooksPath)
	if err != nil {
		return err
	}
	objects := req.Plan.PreviousNativeObjects
	previous, err := predecessor(f, objects)
	if err != nil {
		return err
	}
	// UAP dry-run preflight also sets VerifyOnly, including before first install.
	// A read-only retry consumes acknowledged ownership; a new plan still needs
	// the frozen exact basis and complete pure receipt before intent is granted.
	if req.VerifyOnly && previous != nil && cursorhooks.VerifyOwned(original.Body, previous) == nil {
		return nil
	}
	_, err = a.validateFrozen(original, f, objects)
	return err
}

func (a *Adapter) Activate(ctx context.Context, env clients.Env, req domain.ActivationRequest) (domain.ActivationOutcome, error) {
	if req.Plan.SelectedDelivery.IsZero() {
		return a.Adapter.Activate(ctx, env, req)
	}
	out := outcome(req.Plan.Authentication, req.Plan.Policy)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := a.PreflightActivation(env, req); err != nil {
		return out, err
	}
	f, _ := req.Plan.SelectedDelivery.CursorFacts()
	if f.ProjectionDigest == "" || req.Delivery.ArtifactDigest != f.ProjectionDigest || req.Delivery.ClientID != domain.ClientCursor || req.Delivery.ActivePath != req.Plan.ActivePath {
		return out, fmt.Errorf("cursor activation staged projection differs")
	}
	previous, err := predecessor(f, req.PreviousNativeObjects)
	if err != nil {
		return out, err
	}
	if req.VerifyOnly {
		if previous == nil {
			return out, fmt.Errorf("cursor verification requires acknowledged ownership")
		}
		current, err := env.NativeConfig.ReadExactFile(f.HooksPath)
		if err == nil {
			err = cursorhooks.VerifyOwned(current.Body, previous)
		}
		if err == nil {
			out.NativeObjects = []domain.NativeObjectOwnership{req.Plan.SelectedDelivery.CursorOwnership(valueReceipt(previous))}
		}
		return out, err
	}
	if req.Plan.InstallIntent == domain.InstallIntentPrepare {
		return out, nil
	}
	objects, effect, err := a.applyRegistration(ctx, env.NativeConfig, req.Plan.SelectedDelivery, req.PreviousNativeObjects)
	out.NativeEffect, out.NativeObjects = effect, objects
	return out, err
}

// applyRegistration is one existing ExactFile scope: Original compare, frozen
// Plan compare, CAS/readback, actual owned verification, then receipt. Errors
// after a possible effect retain uncertainty; the UAP pending owner recovers it.
func (a *Adapter) applyRegistration(ctx context.Context, kernel nativeconfig.Kernel, selected domain.SelectedDelivery, previous []domain.NativeObjectOwnership) (objects []domain.NativeObjectOwnership, effect domain.NativeEffectState, err error) {
	effect = domain.NativeEffectUnchanged
	if err := ctx.Err(); err != nil {
		return nil, effect, err
	}
	f, err := a.recorded(selected)
	if err != nil {
		return nil, effect, err
	}
	file, err := kernel.BeginExactFile(f.HooksPath)
	if err != nil {
		return nil, effect, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	result, err := a.validateFrozen(file.Original(), f, previous)
	if err != nil {
		return nil, effect, err
	}
	if err := a.verifyInvocation(); err != nil {
		return nil, effect, err
	}
	if err := ctx.Err(); err != nil {
		return nil, effect, err
	}
	if !result.NoOp {
		if err := file.Apply(result.Desired); err != nil {
			// The existing kernel owns bounded rollback and the observed effect.
			rollbackErr := file.Rollback()
			return nil, domain.NativeEffectUncertain, errors.Join(err, rollbackErr)
		}
		effect = domain.NativeEffectCommitted
	}
	current, err := kernel.ReadExactFile(f.HooksPath)
	if err != nil || !current.Exists {
		return nil, domain.NativeEffectUncertain, errors.Join(err, fmt.Errorf("cursor owned readback unavailable"))
	}
	if err := cursorhooks.VerifyOwned(current.Body, pureReceipt(f.PlannedReceipt)); err != nil {
		return nil, domain.NativeEffectUncertain, err
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.NativeEffectUncertain, err
	}
	return []domain.NativeObjectOwnership{selected.CursorOwnership(valueReceipt(result.Receipt))}, effect, nil
}

func (a *Adapter) Deactivate(ctx context.Context, env clients.Env, req domain.DeactivationRequest) (domain.DeactivationOutcome, error) {
	if req.SelectedDelivery.IsZero() {
		return a.Adapter.Deactivate(ctx, env, req)
	}
	out := domain.DeactivationOutcome{Activation: domain.ActivationNotRequired}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	f, err := a.recorded(req.SelectedDelivery)
	if err != nil {
		return out, err
	}
	if err := a.requireToken(req.Client); err != nil {
		return out, err
	}
	previous, err := predecessor(f, req.NativeObjects)
	if err != nil || req.RemoveOwnedEntry != (previous != nil) {
		return out, errors.Join(err, fmt.Errorf("cursor removal differs from original acknowledged ownership"))
	}
	if !req.Confirmed {
		return out, nil
	}
	if previous != nil {
		if err := a.remove(ctx, env.NativeConfig, f.HooksPath, previous); err != nil {
			return out, err
		}
	} else {
		current, err := env.NativeConfig.ReadExactFile(f.HooksPath)
		if err != nil {
			return out, err
		}
		if err := a.verifyInvocation(); err != nil {
			return out, err
		}
		if _, err := a.purePlan(current, nil, false); err != nil {
			return out, err
		}
	}
	out.ArtifactRemovalAllowed, out.ExternalRemovalComplete = true, true
	return out, ctx.Err()
}

func (a *Adapter) remove(ctx context.Context, kernel nativeconfig.Kernel, path string, previous *cursorhooks.Receipt) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := kernel.BeginExactFile(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	original := file.Original()
	if err := cursorhooks.VerifyOwned(original.Body, previous); err != nil {
		return a.proveRemoved(original, previous)
	}
	result, err := cursorhooks.Plan(cursorhooks.Request{Document: original.Body, Operation: cursorhooks.Remove, Previous: previous, Shell: previous.Shell})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Apply(result.Desired); err != nil {
		return errors.Join(err, file.Rollback())
	}
	return ctx.Err() // ExactFile.Apply already verified the exact removal output.
}

// Absence can be proven only from the original acknowledged remainder. Repair
// is pure here: its successful absent-entry result is never applied or recorded
// as new ownership. Missing/changed foreign remainder refuses recovery.
func (a *Adapter) proveRemoved(current nativeconfig.FileSnapshot, previous *cursorhooks.Receipt) error {
	if !errors.Is(cursorhooks.VerifyOwned(current.Body, previous), cursorhooks.ErrAbsenceUnproven) {
		return fmt.Errorf("cursor owned absence is unproved")
	}
	if err := a.verifyInvocation(); err != nil {
		return err
	}
	_, err := a.purePlan(current, previous, true)
	return err
}

func outcome(auth domain.AuthenticationState, policy domain.PolicyState) domain.ActivationOutcome {
	return domain.ActivationOutcome{Activation: domain.ActivationPrepared, Authentication: auth,
		Policy: policy, Verification: domain.VerificationPackageValid, NativeEffect: domain.NativeEffectUnchanged}
}
