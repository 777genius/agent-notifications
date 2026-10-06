package cursorinstall

import (
	"context"
	"fmt"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

// RefinePlan freezes the successful public pure Plan result and its actual
// original existence/raw bytes basis. No lock, directory or intent is created.
func (a *Adapter) RefinePlan(ctx context.Context, in clients.PlanInput, plan *domain.DeliveryPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if plan.Status == domain.PlanUnsupported {
		return a.Adapter.RefinePlan(ctx, in, plan)
	}
	if err := a.requireToken(in.Client); err != nil {
		return err
	}
	if plan.ClientID != domain.ClientCursor || plan.Scope != domain.ScopeUser || plan.NativeRegistryRoot != a.fixed.ProfileRoot {
		return fmt.Errorf("cursor selected plan profile/client/scope differs")
	}
	selected, err := a.plan(in.Envelope.TreeDigest, in.PreviousNativeObjects)
	if err != nil {
		return err
	}
	if err := selected.ValidatePlan(*plan, in.Envelope.TreeDigest); err != nil {
		return err
	}
	plan.SelectedDelivery = selected
	plan.PreviousNativeObjects = append([]domain.NativeObjectOwnership(nil), in.PreviousNativeObjects...)
	plan.Status = domain.PlanReady
	plan.Activation = domain.ActivationPrepared
	plan.Verification = domain.VerificationPackageValid
	return ctx.Err()
}

// plan is the vendor read/pure-plan boundary. Tests exercise this directly on
// unsupported filesystems; that is mechanics evidence, never installed proof.
func (a *Adapter) plan(canonical string, objects []domain.NativeObjectOwnership) (domain.SelectedDelivery, error) {
	if err := a.verifyInvocation(); err != nil {
		return domain.SelectedDelivery{}, err
	}
	f := a.facts(canonical)
	original, err := a.native.ReadExactFile(f.HooksPath)
	if err != nil {
		return domain.SelectedDelivery{}, err
	}
	previous, err := predecessor(f, objects)
	if err != nil {
		return domain.SelectedDelivery{}, err
	}
	result, err := a.purePlan(original, previous, previous != nil && cursorhooks.VerifyOwned(original.Body, previous) != nil)
	if err != nil {
		return domain.SelectedDelivery{}, err
	}
	f.PlannedReceipt = valueReceipt(result.Receipt)
	f.EntryDigest = f.PlannedReceipt.EntryDigest
	f.OriginalExists, f.OriginalRawDigest = original.Exists, digest(original.Body)
	return domain.NewCursorDelivery(f)
}

// predecessor validates the complete acknowledged object, independently of the
// changing planned receipt. Only the package directory may accompany it.
func predecessor(f domain.CursorDeliveryFacts, objects []domain.NativeObjectOwnership) (*cursorhooks.Receipt, error) {
	var previous *cursorhooks.Receipt
	for _, object := range objects {
		if object.Kind == "managed_package_directory" && object.CursorReceipt == (domain.CursorHookReceipt{}) {
			continue
		}
		r := object.CursorReceipt
		expected := domain.NativeObjectOwnership{ObjectID: f.ObjectID, Kind: "cursor_user_stop", Path: f.HooksPath,
			LogicalName: f.Selector, ManagedDigest: r.EntryDigest, ProtectionClass: "owned_selector", CursorReceipt: r}
		if previous != nil || object != expected || r.Version != 1 || r.Event != "stop" || r.Executable != f.Executable || r.Selector != f.Selector || r.Shell != f.Shell || f.EntryDigest != "" && f.EntryDigest != r.EntryDigest {
			return nil, fmt.Errorf("cursor original acknowledged ownership is malformed or differs")
		}
		previous = pureReceipt(r)
	}
	return previous, nil
}

func (a *Adapter) purePlan(original nativeconfig.FileSnapshot, previous *cursorhooks.Receipt, repair bool) (cursorhooks.Result, error) {
	if original.Exists && len(original.Body) == 0 {
		return cursorhooks.Result{}, fmt.Errorf("cursor present empty config is malformed")
	}
	op := cursorhooks.Install
	if previous != nil {
		op = cursorhooks.Update
		if repair {
			op = cursorhooks.Repair
		}
	}
	return cursorhooks.Plan(cursorhooks.Request{Document: original.Body, Operation: op,
		Specs: []cursorhooks.HookSpec{a.spec()}, Previous: previous, Shell: cursorhooks.LinuxUserShell32212, ExecutableVerified: true})
}

func compareBasis(original nativeconfig.FileSnapshot, f domain.CursorDeliveryFacts) error {
	if original.Exists != f.OriginalExists || digest(original.Body) != f.OriginalRawDigest {
		return fmt.Errorf("cursor original existence/raw basis changed: %w", nativeconfig.ErrConcurrentChange)
	}
	return nil
}

// validateFrozen consumes the original receipt/basis after BeginExactFile. It
// never repairs a stale grant by capturing a fresh receipt from current bytes.
func (a *Adapter) validateFrozen(original nativeconfig.FileSnapshot, f domain.CursorDeliveryFacts, objects []domain.NativeObjectOwnership) (cursorhooks.Result, error) {
	if err := compareBasis(original, f); err != nil {
		return cursorhooks.Result{}, err
	}
	previous, err := predecessor(f, objects)
	if err != nil {
		return cursorhooks.Result{}, err
	}
	// Ordinary Update permits foreign edits. Repair of absence requires the
	// original acknowledged remainder. Use Update when the entry still exists.
	result, err := a.purePlan(original, previous, previous != nil && cursorhooks.VerifyOwned(original.Body, previous) != nil)
	if err != nil {
		return result, err
	}
	if valueReceipt(result.Receipt) != f.PlannedReceipt {
		return result, fmt.Errorf("cursor successful plan receipt differs from frozen packet")
	}
	return result, nil
}
