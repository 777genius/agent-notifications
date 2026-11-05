package portablesetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/desktopinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Translate the protected kernel refusal while retaining its identity through
// coordinated publication, registration, binding and finalization wrappers.
func policyConflict(err error) error {
	if errors.Is(err, installruntime.ErrPolicyConflict) {
		return fmt.Errorf("%w: %w", ErrConcurrentChange, err)
	}
	return err
}

// Paths that reuse an existing reservation or consumer can bypass Commit;
// check their admission before locator/UAP effects as well.
func (s Service) checkPolicy(ctx context.Context, root string) error {
	if s.ExpectedPolicy == nil {
		return nil
	}
	current, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return err
	}
	if current.Preimage != *s.ExpectedPolicy {
		return fmt.Errorf("%w: stale explicit policy bytes", ErrConcurrentChange)
	}
	return nil
}

func decorate(env Envelope, name string) Envelope {
	out := env
	out.Args = []string{"portable-launch", "--locator", name}
	return out
}

func (s Service) preflight(b portable.Binding) error {
	if _, _, _, err := b.Registration(); err != nil {
		return err
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		return fmt.Errorf("%w: managed runtime prerequisite missing", ErrPreflight)
	}
	if snapshot.Recovery {
		return installruntime.ErrPolicyRecovery
	}
	if snapshot.Ledger.ID != b.ComponentID || snapshot.Ledger.Owner != b.Owner || snapshot.Ledger.RuntimeRoot != b.RuntimeRoot {
		return fmt.Errorf("%w: binding does not match managed runtime", ErrPreflight)
	}
	if err := b.CheckPrimaryFile(snapshot); err != nil {
		return fmt.Errorf("%w: selected runtime primary is not an owned executable: %v", ErrPreflight, err)
	}
	return nil
}

func (s Service) Install(ctx context.Context, req Request) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := s.preflight(req.Binding); err != nil {
		return portable.Binding{}, err
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.Binding.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	defer release()
	if _, err = installruntime.Recover(ctx, req.Binding.ControlRoot); err != nil {
		return portable.Binding{}, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	req.ExpectedGeneration = snap.Ledger.Generation
	gen, res, err := s.handoffForward(ctx, req, "install")
	if err != nil {
		return portable.Binding{}, err
	}
	req.ExpectedGeneration = gen
	req.Reservation = res
	name, err := req.Binding.Filename()
	if err != nil {
		return portable.Binding{}, err
	}
	if s.Stager == nil || s.Activator == nil {
		return portable.Binding{}, ErrPreflight
	}
	receipt, err := s.Stager.Stage(ctx, decorate(req.Envelope, name))
	if err != nil {
		return portable.Binding{}, err
	}
	if receipt.DataRoot != req.Binding.DataRoot || receipt.LocatorArg != name || receipt.BindingID != req.Binding.BindingID {
		return portable.Binding{}, fmt.Errorf("%w: materializer receipt does not match binding", ErrPreflight)
	}
	if _, err = s.CommitBinding(ctx, req); err != nil {
		return portable.Binding{}, err
	}
	if err = s.finishHandoff(ctx, req, res); err != nil {
		return portable.Binding{}, err
	}
	if err = s.Activator.Activate(ctx, receipt); err != nil {
		return portable.Binding{}, err
	}
	return req.Binding, nil
}

func (s Service) matchingReservation(req Request, action string) (*installruntime.PendingMutation, error) {
	if req.Reservation != nil {
		return req.Reservation, nil
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return nil, err
	}
	pending := snap.Ledger.PendingMutation
	if pending == nil {
		return nil, nil
	}
	if pending.Owner != req.Binding.Owner {
		return nil, fmt.Errorf("%w: pending reservation owned by %s", ErrPreflight, pending.Owner)
	}
	intent, err := ReadIntent(req.Binding.ControlRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: pending handoff intent missing: %v", ErrPreflight, err)
	}
	if !intentMatches(intent, pending.ID, action, string(req.Binding.Integration), req.SourceDigest, req.TreeDigest, req.HelperDigest, req.HelperVersion) {
		return nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
	}
	cp := *pending
	return &cp, nil
}

func (s Service) CommitBinding(ctx context.Context, req Request) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := s.preflight(req.Binding); err != nil {
		return portable.Binding{}, err
	}
	key, consumer, _, err := req.Binding.Registration()
	if err != nil {
		return portable.Binding{}, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if existing, ok := snap.Ledger.Consumers[key]; ok && reflect.DeepEqual(existing, consumer) {
		// Identical registration is not permission to bypass an unrelated
		// in-flight handoff or removal reservation.
		if _, err := s.matchingReservation(req, "install"); err != nil {
			return portable.Binding{}, err
		}
		if err := s.checkPolicy(ctx, req.Binding.ControlRoot); err != nil {
			return portable.Binding{}, err
		}
		if _, err = portable.Publish(req.Binding); err != nil {
			return portable.Binding{}, err
		}
		return req.Binding, nil
	}
	res, err := s.matchingReservation(req, "install")
	if err != nil {
		return portable.Binding{}, err
	}
	req.Reservation = res
	_, existed := snap.Ledger.Consumers[key]
	gen := req.ExpectedGeneration
	var fields map[string]json.RawMessage
	policy := s.ExpectedPolicy
	var after installruntime.Identity
	if req.Binding.Integration == portable.Cursor || req.Binding.Integration == portable.CopilotVSCode {
		// A new identity cannot inherit native leaves from a removed registration.
		// These false leaves share the registration transaction, before publication.
		if policy == nil {
			observed, e := installruntime.ReadRevocationSnapshot(ctx, req.Binding.ControlRoot)
			if e != nil {
				return portable.Binding{}, e
			}
			policy = &observed.Preimage
		}
		off := false
		var e error
		if req.Binding.Integration == portable.CopilotVSCode {
			fields, e = copilotvscodeinstall.PolicyPatch(req.Binding, copilotvscodeinstall.Choices{Desktop: &off, Webhook: &off, Manual: &off}, nil)
		} else {
			fields, e = copilotvscodeinstall.CursorPolicyPatch(req.Binding, copilotvscodeinstall.CursorChoices{Desktop: &off, Webhook: &off}, nil)
		}
		if e != nil {
			return portable.Binding{}, e
		}
		after, e = installruntime.PredictPolicyIdentity(req.Binding.ControlRoot, *policy, fields)
		if e != nil {
			return portable.Binding{}, policyConflict(e)
		}
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, Consumer: consumer, ExpectedGeneration: &gen, ExpectedPolicy: policy, PolicyFields: fields, RefreshOnly: false, Reservation: res,
	})
	if err != nil {
		return portable.Binding{}, policyConflict(err)
	}
	if req.Binding.Integration == portable.Cursor || req.Binding.Integration == portable.CopilotVSCode {
		// Advance only the request-local atom through this successful, frozen
		// false patch. Later observations may verify it, never replace it.
		policy = &after
		if s.ExpectedPolicy != nil {
			*s.ExpectedPolicy = after
		}
		verified, e := installruntime.ReadPolicySnapshot(ctx, req.Binding.ControlRoot)
		if e != nil {
			return portable.Binding{}, e
		}
		if verified.Installation.Recovery || !reflect.DeepEqual(verified.Installation.Ledger, ledger) || verified.Preimage != after || !portable.ExactCommittedBinding(verified.Installation.Ledger, req.Binding) {
			return portable.Binding{}, ErrConcurrentChange
		}
	}
	if _, err = portable.Publish(req.Binding); err != nil {
		if !existed {
			next := ledger.Generation
			_, _ = installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
				ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &next, ExpectedPolicy: policy, Reservation: res,
			})
		}
		return portable.Binding{}, err
	}
	return req.Binding, nil
}

func (s Service) RevokeBinding(ctx context.Context, req Request) error {
	if ctx == nil {
		return ErrPreflight
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	variants, err := portable.CommittedBindingVariants(snap.Ledger, req.Binding)
	if err != nil || len(variants) > 1 || (len(variants) == 1 && variants[0] != req.Binding) {
		return fmt.Errorf("%w: historical binding is ambiguous or differs from requested identity", ErrPreflight)
	}
	if _, err := portable.ExactLocator(req.Binding); err != nil {
		return err
	}
	res, err := s.matchingReservation(req, "uninstall")
	if err != nil {
		return err
	}
	req.Reservation = res
	return revokeExactBinding(ctx, req, len(variants) == 1)
}

// ReplaceCommittedBinding retires the old locator only after the replacement
// consumer and locator have both been committed. A retry may find the old
// consumer already gone while its exact locator still awaits deletion.
// req.Binding is the replacement; req.Reservation carries the install intent.
func (s Service) ReplaceCommittedBinding(ctx context.Context, old portable.Binding, req Request) error {
	if ctx == nil || old == req.Binding {
		return ErrPreflight
	}
	sameIdentity := old
	sameIdentity.GlobalConfig, sameIdentity.Primary = req.Binding.GlobalConfig, req.Binding.Primary
	if sameIdentity != req.Binding {
		return fmt.Errorf("%w: replacement changes UAP binding identity", ErrPreflight)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	variants, err := portable.CommittedBindingVariants(snap.Ledger, old)
	if err != nil || len(variants) == 0 || len(variants) > 2 {
		return fmt.Errorf("%w: replacement consumer set is invalid", ErrPreflight)
	}
	oldPresent, newPresent := false, false
	for _, variant := range variants {
		switch variant {
		case old:
			oldPresent = true
		case req.Binding:
			newPresent = true
		default:
			return fmt.Errorf("%w: third consumer for UAP binding", ErrPreflight)
		}
	}
	if !newPresent || !portable.ExactCommittedBinding(snap.Ledger, req.Binding) {
		return fmt.Errorf("%w: replacement consumer is not committed", ErrPreflight)
	}
	action, err := checkReplacementIntent(snap.Ledger, old, req)
	if err != nil {
		return err
	}
	newLocator, err := portable.ExactLocator(req.Binding)
	if err != nil || !newLocator {
		return fmt.Errorf("%w: replacement locator is not published", ErrPreflight)
	}
	if _, err := portable.ExactLocator(old); err != nil {
		return err
	}
	res, err := s.matchingReservation(req, action)
	if err != nil {
		return err
	}
	req.Binding, req.Reservation = old, res
	return revokeExactBinding(ctx, req, oldPresent)
}

func checkReplacementIntent(ledger installruntime.Ledger, old portable.Binding, req Request) (string, error) {
	pending := ledger.PendingMutation
	if pending == nil || req.Reservation == nil || !reflect.DeepEqual(*pending, *req.Reservation) {
		return "", fmt.Errorf("%w: replacement lacks a matching reserved intent", ErrPreflight)
	}
	intent, target, err := readMigrationIntentTarget(ledger, old.ControlRoot, old.Integration)
	if err != nil {
		return "", err
	}
	oldKey, _, _, oldErr := old.Registration()
	newKey, _, _, newErr := req.Binding.Registration()
	if oldErr != nil || newErr != nil || target.InstallationID != old.InstallationID || target.BindingID != old.BindingID || target.OldConsumerKey != oldKey || target.NewConsumerKey != newKey || *target.OldBinding != old || *target.NewBinding != req.Binding {
		return "", fmt.Errorf("%w: replacement intent target differs", ErrPreflight)
	}
	return intent.Action, nil
}

func readMigrationIntentTarget(ledger installruntime.Ledger, controlRoot string, integration portable.Integration) (Intent, IntentTarget, error) {
	pending := ledger.PendingMutation
	if pending == nil || pending.Owner != "existing-installer" || pending.IntentRef != IntentPath(controlRoot) {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration reservation missing", ErrPreflight)
	}
	path := IntentPath(controlRoot)
	wantFile, ok := ledger.Files[path]
	currentFile, err := installruntime.Fingerprint(path)
	if err != nil || !ok || !reflect.DeepEqual(wantFile, currentFile) {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration intent bytes changed", ErrPreflight)
	}
	intent, err := ReadIntent(controlRoot)
	if err != nil || intent.SetupIntentID != pending.ID || (intent.Action != "update" && intent.Action != "repair") {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration intent is missing", ErrPreflight)
	}
	var found IntentTarget
	matches := 0
	for _, target := range intent.Targets {
		if target.Client != string(integration) {
			continue
		}
		if target.DataReceiptID == "" || target.OldBinding == nil || target.NewBinding == nil {
			return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration target incomplete", ErrPreflight)
		}
		found = target
		matches++
	}
	if matches != 1 {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration target is missing or ambiguous", ErrPreflight)
	}
	return intent, found, nil
}

func revokeExactBinding(ctx context.Context, req Request, present bool) error {
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return err
	}
	if present {
		gen := req.ExpectedGeneration
		var files []installruntime.File
		if runtime.GOOS == "windows" && req.Binding.Integration == portable.Cursor {
			snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
			if err != nil {
				return err
			}
			executable, err := portable.ResolvePrimaryExecutable(snap.Ledger, req.Binding.Primary)
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			file, err := desktopinstall.StageCursorWindowsShortcut(key, home, executable, false, snap.Ledger)
			if err != nil {
				return err
			}
			if file != nil {
				files = append(files, *file)
			}
		}
		if len(files) != 0 {
			// Remove the owned shortcut through the existing file CAS transaction.
			// Consumer removal forbids file requests for portable consumers.
			removed, err := installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
				ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &gen, Reservation: req.Reservation, Files: files,
			})
			if err != nil {
				return err
			}
			gen = removed.Generation
		}
		if _, err = installruntime.Commit(ctx, installruntime.Request{
			ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
			ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &gen, Reservation: req.Reservation,
		}); err != nil {
			return err
		}
	}
	return portable.RevokeLocator(req.Binding)
}

func (s Service) Remove(ctx context.Context, req Request) error {
	if ctx == nil || s.Remover == nil {
		return ErrPreflight
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	defer release()
	if _, err = installruntime.Recover(ctx, req.Binding.ControlRoot); err != nil {
		return err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	req.ExpectedGeneration = snap.Ledger.Generation
	res, err := s.matchingReservation(req, "uninstall")
	if err != nil {
		return err
	}
	if res == nil {
		published, created, err := s.publishIntent(ctx, req, req.ExpectedGeneration, "uninstall", "revoke-locator", []string{"direct-mcp"})
		if err != nil {
			return err
		}
		req.ExpectedGeneration = published.Generation
		res = created
	}
	req.Reservation = res
	if err := s.RevokeBinding(ctx, req); err != nil {
		return err
	}
	if err := s.Remover.Remove(ctx, req.Binding.BindingID); err != nil {
		return err
	}
	if req.Discovery.ConfigPath == "" {
		return s.finishHandoff(ctx, req, res)
	}
	snap, err = installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	req.ExpectedGeneration = snap.Ledger.Generation
	if _, err = s.HandoffReverse(ctx, req); err != nil {
		return err
	}
	return s.finishHandoff(ctx, req, res)
}
