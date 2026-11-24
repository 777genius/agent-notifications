package portablesetup

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// PublishConfirmedIntent records the confirmed SetupIntent and kernel
// reservation before live hooks/MCP mutation. It takes the coordinator lease.
func (s Service) PublishConfirmedIntent(ctx context.Context, req ConfirmedIntent) (installruntime.Ledger, *installruntime.PendingMutation, error) {
	if req.ExpectedPolicy == nil {
		req.ExpectedPolicy = s.ExpectedPolicy
	}
	if ctx == nil {
		return installruntime.Ledger{}, nil, ErrPreflight
	}
	if req.ControlRoot == "" || req.RuntimeRoot == "" || req.Owner == "" || req.Action == "" || len(req.Targets) == 0 {
		return installruntime.Ledger{}, nil, fmt.Errorf("%w: incomplete confirmed intent", ErrPreflight)
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	defer release()
	if _, err = installruntime.Recover(ctx, req.ControlRoot); err != nil {
		return installruntime.Ledger{}, nil, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	if snap.Ledger.Generation != req.ExpectedGeneration {
		return installruntime.Ledger{}, nil, ErrConcurrentChange
	}
	if pending := snap.Ledger.PendingMutation; pending != nil {
		if err := (Service{ExpectedPolicy: req.ExpectedPolicy}).checkPolicy(ctx, req.ControlRoot); err != nil {
			return installruntime.Ledger{}, nil, err
		}
		intent, readErr := ReadIntent(req.ControlRoot)
		if readErr != nil || !intentMatches(intent, pending.ID, req.Action, req.Targets[0].Client, req.SourceDigest, req.TreeDigest, req.HelperDigest, req.HelperVersion) || (intent.Primary != "" && req.Primary != "" && intent.Primary != req.Primary) || (intent.GlobalConfig != "" && req.GlobalConfig != "" && intent.GlobalConfig != req.GlobalConfig) {
			return installruntime.Ledger{}, nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
		}
		cp := *pending
		return snap.Ledger, &cp, nil
	}
	consumerID, commitRoot, err := intentConsumer(snap.Ledger, req.RuntimeRoot)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	intentID, err := newIntentID()
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	stage := req.Stage
	if stage == "" {
		stage = "confirmed"
	}
	intent := Intent{
		Version:             intentVersion,
		SetupIntentID:       intentID,
		Action:              req.Action,
		Stage:               stage,
		ExpectedGeneration:  snap.Ledger.Generation,
		SourceRevision:      req.SourceRevision,
		SourceDigest:        req.SourceDigest,
		TreeDigest:          req.TreeDigest,
		HelperDigest:        req.HelperDigest,
		HelperVersion:       req.HelperVersion,
		Primary:             req.Primary,
		GlobalConfig:        req.GlobalConfig,
		ExternalUninstalled: req.ExternalUninstalled,
		Targets:             req.Targets,
	}
	payload, err := marshalIntent(intent)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	res := reservationFrom(intent, req.ControlRoot)
	path := IntentPath(req.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	gen := snap.Ledger.Generation
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.ControlRoot, Owner: req.Owner, RuntimeRoot: commitRoot,
		ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: req.ExpectedPolicy, Reservation: &res,
		Files: []installruntime.File{{Path: path, Before: before, Data: payload, Mode: 0600}},
	})
	if err != nil {
		return installruntime.Ledger{}, nil, fmt.Errorf("%w: publish confirmed intent: %w", ErrPreflight, policyConflict(err))
	}
	return ledger, &res, nil
}

// PatchIntentReceipt records a known UAP data receipt on the pending host
// intent without allocating a new SetupIntentID.
func (s Service) PatchIntentReceipt(ctx context.Context, controlRoot, runtimeRoot, owner, client, receiptID string, expected *installruntime.PendingMutation) error {
	if receiptID == "" || client == "" {
		return nil
	}
	return s.patchIntent(ctx, controlRoot, runtimeRoot, owner, func(intent *Intent, ledger installruntime.Ledger) (bool, error) {
		if expected == nil || ledger.PendingMutation == nil || *ledger.PendingMutation != *expected {
			return false, ErrConcurrentChange
		}
		changed := false
		for i, target := range intent.Targets {
			if target.Client != client {
				continue
			}
			if target.DataReceiptID == receiptID {
				return false, nil
			}
			intent.Targets[i].DataReceiptID = receiptID
			changed = true
		}
		return changed, nil
	})
}

// PatchIntentExternalUninstalled records a confirmed Codex native-plugin
// attestation on the pending intent so resume does not require the flag again.
func (s Service) PatchIntentExternalUninstalled(ctx context.Context, controlRoot, runtimeRoot, owner string, expected *installruntime.PendingMutation) error {
	return s.patchIntent(ctx, controlRoot, runtimeRoot, owner, func(intent *Intent, ledger installruntime.Ledger) (bool, error) {
		if expected == nil || ledger.PendingMutation == nil || *ledger.PendingMutation != *expected {
			return false, ErrConcurrentChange
		}
		if intent.ExternalUninstalled {
			return false, nil
		}
		intent.ExternalUninstalled = true
		return true, nil
	})
}

// PatchIntentGlobalConfig freezes the path before the first portable consumer
// is committed. Legacy or hooks-only intents may omit it; a later retry must
// match the frozen path exactly.
func (s Service) PatchIntentGlobalConfig(ctx context.Context, controlRoot, runtimeRoot, owner, installationID, path string, expected *installruntime.PendingMutation) error {
	if installationID == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrPreflight
	}
	return s.patchIntent(ctx, controlRoot, runtimeRoot, owner, func(intent *Intent, ledger installruntime.Ledger) (bool, error) {
		if expected == nil || ledger.PendingMutation == nil || *ledger.PendingMutation != *expected {
			return false, ErrConcurrentChange
		}
		installed, found, err := portable.InstalledGlobalConfig(ledger, installationID, controlRoot)
		if err != nil || (found && installed != path) {
			return false, ErrIntentConflict
		}
		if intent.GlobalConfig != "" {
			if intent.GlobalConfig != path {
				return false, ErrIntentConflict
			}
			return false, nil
		}
		intent.GlobalConfig = path
		return true, nil
	})
}

func (s Service) patchIntent(ctx context.Context, controlRoot, runtimeRoot, owner string, mutate func(*Intent, installruntime.Ledger) (bool, error)) error {
	if ctx == nil || controlRoot == "" || mutate == nil {
		return nil
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, controlRoot)
	if err != nil {
		return err
	}
	defer release()
	return s.patchIntentLocked(ctx, controlRoot, runtimeRoot, owner, mutate)
}

// patchIntentLocked is called only while the coordinator lease is held by the
// enclosing materializer. It avoids reacquiring that lease during recovery.
func (s Service) patchIntentLocked(ctx context.Context, controlRoot, runtimeRoot, owner string, mutate func(*Intent, installruntime.Ledger) (bool, error)) error {
	if ctx == nil || controlRoot == "" || mutate == nil {
		return ErrPreflight
	}
	if _, err := installruntime.Recover(ctx, controlRoot); err != nil {
		return err
	}
	snap, err := installruntime.ReadInstalledSnapshot(controlRoot)
	if err != nil {
		return err
	}
	pending := snap.Ledger.PendingMutation
	if pending == nil {
		return nil
	}
	intent, err := ReadIntent(controlRoot)
	if err != nil {
		return err
	}
	if intent.SetupIntentID != pending.ID {
		return fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
	}
	changed, err := mutate(&intent, snap.Ledger)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	payload, err := marshalIntent(intent)
	if err != nil {
		return err
	}
	consumerID, commitRoot, err := intentConsumer(snap.Ledger, runtimeRoot)
	if err != nil {
		return err
	}
	path := IntentPath(controlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	if owner == "" {
		owner = snap.Ledger.Owner
	}
	gen := snap.Ledger.Generation
	res := *pending
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: controlRoot, Owner: owner, RuntimeRoot: commitRoot,
		ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: s.ExpectedPolicy, Reservation: &res,
		Files: []installruntime.File{{Path: path, Before: before, Data: payload, Mode: 0600}},
	})
	if err != nil {
		return fmt.Errorf("%w: patch confirmed intent: %w", ErrPreflight, policyConflict(err))
	}
	return nil
}

// FinishConfirmedIntent removes a matching reservation after the whole wizard
// mutation finished. Incomplete HoldOnly/external uninstall leaves it in place.
func (s Service) FinishConfirmedIntent(ctx context.Context, req ConfirmedIntent, res *installruntime.PendingMutation) (uint64, bool, error) {
	if ctx == nil || res == nil {
		return 0, false, nil
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.ControlRoot)
	if err != nil {
		return 0, false, err
	}
	defer release()
	if _, err = installruntime.Recover(ctx, req.ControlRoot); err != nil {
		return 0, false, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return 0, false, err
	}
	if snap.Ledger.PendingMutation == nil {
		return snap.Ledger.Generation, false, nil
	}
	if *snap.Ledger.PendingMutation != *res {
		return snap.Ledger.Generation, false, nil
	}
	consumerID := "reservation-finalizer"
	commitRoot := req.RuntimeRoot
	if len(snap.Ledger.Consumers) != 0 {
		consumerID, commitRoot, err = intentConsumer(snap.Ledger, req.RuntimeRoot)
		if err != nil {
			return 0, false, err
		}
	}
	path := IntentPath(req.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return 0, false, err
	}
	var files []installruntime.File
	if before.Exists {
		files = []installruntime.File{{Path: path, Before: before, Remove: true}}
	}
	gen := snap.Ledger.Generation
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.ControlRoot, Owner: req.Owner, RuntimeRoot: commitRoot,
		ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: s.ExpectedPolicy, Reservation: res, ClearReservation: true,
		Files: files,
	})
	if err != nil {
		return 0, false, fmt.Errorf("%w: clear confirmed intent: %w", ErrPreflight, policyConflict(err))
	}
	return ledger.Generation, true, nil
}

func (s Service) publishIntent(ctx context.Context, req Request, gen uint64, action, stage string, units []string) (installruntime.Ledger, *installruntime.PendingMutation, error) {
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	intentID, err := newIntentID()
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	targets := req.IntentTargets
	if len(targets) == 0 {
		target := IntentTarget{
			Client: string(req.Binding.Integration), BindingID: req.Binding.BindingID,
			InstallationID: req.Binding.InstallationID, DataReceiptID: req.DataReceiptID,
			Profile: req.Profile, Units: units,
		}
		if action == "uninstall" {
			binding := req.Binding
			target.OldBinding, target.OldConsumerKey = &binding, key
		}
		targets = []IntentTarget{target}
	}
	intent := Intent{
		Version:            intentVersion,
		SetupIntentID:      intentID,
		Action:             action,
		Stage:              stage,
		ExpectedGeneration: gen,
		SourceRevision:     req.SourceRevision,
		SourceDigest:       req.SourceDigest,
		TreeDigest:         req.TreeDigest,
		HelperDigest:       req.HelperDigest,
		HelperVersion:      req.HelperVersion,
		Targets:            targets,
	}
	payload, err := marshalIntent(intent)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	res := reservationFrom(intent, req.Binding.ControlRoot)
	path := IntentPath(req.Binding.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: s.ExpectedPolicy, Reservation: &res,
		Files: []installruntime.File{{Path: path, Before: before, Data: payload, Mode: 0600}},
	})
	if err != nil {
		return installruntime.Ledger{}, nil, fmt.Errorf("%w: publish %s reservation: %w", ErrPreflight, action, policyConflict(err))
	}
	return ledger, &res, nil
}

func (s Service) finishHandoff(ctx context.Context, req Request, res *installruntime.PendingMutation) error {
	if res == nil {
		return nil
	}
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	if snap.Ledger.PendingMutation == nil {
		return nil
	}
	path := IntentPath(req.Binding.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	var files []installruntime.File
	if before.Exists {
		files = []installruntime.File{{Path: path, Before: before, Remove: true}}
	}
	gen := snap.Ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: s.ExpectedPolicy, Reservation: res, ClearReservation: true,
		Files: files,
	})
	if err != nil {
		return fmt.Errorf("%w: clear handoff reservation: %w", ErrPreflight, policyConflict(err))
	}
	return nil
}

func discoveryProvider(i portable.Integration) (registration.Provider, error) {
	switch i {
	case portable.Codex:
		return registration.Codex, nil
	case portable.Claude:
		return registration.Claude, nil
	default:
		return "", ErrPreflight
	}
}

func SharedDataSibling(dataRoot, otherBindingID string) string {
	return filepath.Join(dataRoot, otherBindingID)
}
