package cursorinstall

import (
	"context"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// ReconcileNativeIntent consumes the full durable packet. UAP validates its
// binding, package and frozen physical authority on both sides of this callback.
// This adapter only observes; neither registration nor removal is resent.
func (a *Adapter) ReconcileNativeIntent(ctx context.Context, intent domain.PendingNativeIntent) (domain.ActivationOutcome, error) {
	out := outcome(domain.AuthenticationNotRequired, domain.PolicyAllowed)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if intent.AttemptID == "" || intent.LocalEntryObservation != nil || intent.ProfileAuthority == nil || intent.ProfileAuthority.IsZero() || intent.ProfileAuthority.Facts().CanonicalRoot != a.fixed.ProfileRoot {
		return out, fmt.Errorf("cursor recovery requires the recorded attempt and physical authority")
	}
	objects, err := a.reconcile(intent)
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.NativeObjects = objects
	return out, nil
}

// reconcile is the read-only vendor boundary, usable in real-FS mechanics
// tests without substituting an affirmative physical-authority implementation.
func (a *Adapter) reconcile(intent domain.PendingNativeIntent) ([]domain.NativeObjectOwnership, error) {
	f, err := a.recorded(intent.Delivery)
	if err != nil {
		return nil, err
	}
	if intent.AttemptID == "" || f.ProjectionDigest == "" {
		return nil, fmt.Errorf("cursor recovery packet is incomplete")
	}
	var objects []domain.NativeObjectOwnership
	if intent.PreviousCursorObject != (domain.NativeObjectOwnership{}) {
		objects = []domain.NativeObjectOwnership{intent.PreviousCursorObject}
	}
	previous, err := predecessor(f, objects)
	if err != nil {
		return nil, err
	}
	current, err := a.native.ReadExactFile(f.HooksPath)
	if err != nil {
		return nil, err
	}
	switch intent.Direction {
	case domain.NativeIntentRegister:
		if intent.RemoveOwnedEntry {
			return nil, fmt.Errorf("cursor register intent has reverse authority")
		}
		if err := a.verifyInvocation(); err != nil {
			return nil, err
		}
		// Only the recorded successful Plan receipt supplies first-register
		// authority. VerifyOwned cannot invent a current receipt/remainder.
		if !current.Exists {
			return nil, cursorhooks.ErrAbsenceUnproven
		}
		if err := cursorhooks.VerifyOwned(current.Body, pureReceipt(f.PlannedReceipt)); err != nil {
			return nil, err
		}
		return []domain.NativeObjectOwnership{intent.Delivery.CursorOwnership(f.PlannedReceipt)}, nil
	case domain.NativeIntentRemove:
		if intent.RemoveOwnedEntry != (previous != nil) {
			return nil, fmt.Errorf("cursor removal intent lacks original acknowledged receipt")
		}
		if previous != nil {
			return nil, a.proveRemoved(current, previous)
		}
		// An unowned reverse decision cannot claim a matching foreign command.
		if err := a.verifyInvocation(); err != nil {
			return nil, err
		}
		_, err := a.purePlan(current, nil, false)
		return nil, err
	default:
		return nil, fmt.Errorf("cursor recorded intent direction is invalid")
	}
}

func (a *Adapter) ValidateBindingProfile(root string, binding domain.ClientBinding) error {
	if binding.SelectedDelivery.IsZero() {
		return a.Adapter.ValidateBindingProfile(root, binding)
	}
	f, err := a.recorded(binding.SelectedDelivery)
	if err != nil {
		return err
	}
	if root != f.ProfileRoot || binding.NativeProfileRoot != root {
		return fmt.Errorf("cursor binding profile differs from recorded selection")
	}
	if _, err := predecessor(f, binding.NativeObjects); err != nil {
		return err
	}
	return a.Adapter.ValidateBindingProfile(root, binding)
}

// InspectNativeRegistry is the existing read-only removal/admission capability.
// Its receipt always comes from the binding; a proposed packet is not ownership.
func (a *Adapter) InspectNativeRegistry(ctx context.Context, env clients.Env, c domain.DetectedClient, plan domain.DeliveryPlan, binding *domain.ClientBinding) (clients.RegistryFinding, error) {
	if plan.SelectedDelivery.IsZero() {
		return a.Adapter.InspectNativeRegistry(ctx, env, c, plan, binding)
	}
	if err := ctx.Err(); err != nil {
		return clients.RegistryIndeterminate, err
	}
	if err := a.requireToken(c); err != nil {
		return clients.RegistryIndeterminate, err
	}
	selected, objects := plan.SelectedDelivery, plan.PreviousNativeObjects
	if binding != nil {
		if !selected.SameSelection(binding.SelectedDelivery) {
			return clients.RegistryIndeterminate, fmt.Errorf("cursor registry binding selection differs")
		}
		selected, objects = binding.SelectedDelivery, binding.NativeObjects
	}
	f, err := a.recorded(selected)
	if err != nil {
		return clients.RegistryIndeterminate, err
	}
	previous, err := predecessor(f, objects)
	if err != nil {
		return clients.RegistryIndeterminate, err
	}
	current, err := env.NativeConfig.ReadExactFile(f.HooksPath)
	if err != nil {
		return clients.RegistryIndeterminate, err
	}
	if previous != nil {
		if err := cursorhooks.VerifyOwned(current.Body, previous); err != nil {
			return clients.RegistryIndeterminate, err
		}
		return clients.RegistryExpected, nil
	}
	if err := a.verifyInvocation(); err != nil {
		return clients.RegistryIndeterminate, err
	}
	if _, err := a.purePlan(current, nil, false); err != nil {
		return clients.RegistryIndeterminate, err
	}
	return clients.RegistryClear, nil
}
