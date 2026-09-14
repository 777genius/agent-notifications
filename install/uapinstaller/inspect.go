package uapinstaller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/transaction"
)

// Inspect is read-only. It does not recover journals or invoke host seams.
func (e *Engine) Inspect(_ context.Context) (Inspection, error) {
	return e.observe()
}

func (e *Engine) observe() (Inspection, error) {
	out := Inspection{StateRoot: e.cfg.StateRoot}
	state, err := e.store.Load()
	if err != nil {
		out.Recovery.Required = true
		out.Recovery.Reason = err.Error()
		return out, err
	}
	bindingIndex := map[string]observedBinding{}
	for _, installation := range state.Installations {
		item := InspectedInstallation{InstallationID: installation.InstallationID}
		for _, binding := range installation.Clients {
			receipt := installation.DataReceipts[binding.DataReceiptID]
			item.Bindings = append(item.Bindings, InspectedBinding{
				ClientID: binding.ClientID, BindingID: binding.ClientBindingID, Scope: binding.Scope,
				TargetPath: binding.TargetLocator, DataRoot: receipt.Locator,
			})
			bindingIndex[binding.ClientBindingID] = observedBinding{
				InstallationID: installation.InstallationID,
				TargetPath:     binding.TargetLocator,
			}
		}
		out.Installations = append(out.Installations, item)
	}
	open, err := dirswap.Manager{JournalDir: e.cfg.OperationsDir}.ListOpen()
	if err != nil {
		out.Recovery.Required = true
		out.Recovery.Reason = err.Error()
		return out, err
	}
	openIDs := make(map[string]dirswap.Receipt, len(open))
	for _, journal := range open {
		openIDs[journal.OperationID] = journal
		located := bindingIndex[journal.ClientBindingID]
		out.Recovery.Journals = append(out.Recovery.Journals, PendingJournal{
			OperationID:    journal.OperationID,
			Digest:         journalDigest(journal),
			BindingID:      journal.ClientBindingID,
			InstallationID: located.InstallationID,
			TargetPath:     firstNonEmpty(journal.ActivePath, located.TargetPath),
			Phase:          journal.Phase,
		})
	}
	sort.Slice(out.Recovery.Journals, func(i, j int) bool {
		return out.Recovery.Journals[i].OperationID < out.Recovery.Journals[j].OperationID
	})
	out.Recovery.Receipts = unfinishedReceipts(state, openIDs, bindingIndex)
	out.Recovery.Required = len(out.Recovery.Journals) > 0 || len(out.Recovery.Receipts) > 0
	return out, nil
}

type observedBinding struct {
	InstallationID, TargetPath string
}

func unfinishedReceipts(state domain.StateFileV2, open map[string]dirswap.Receipt, bindings map[string]observedBinding) []PendingReceipt {
	var out []PendingReceipt
	add := func(receipt domain.MutationReceipt, installationID, targetPath string) {
		if receipt.Phase == "" || receipt.Phase == transaction.ReceiptPhaseCommitted {
			return
		}
		located := bindings[receipt.ClientBindingID]
		if installationID == "" {
			installationID = located.InstallationID
		}
		if targetPath == "" {
			targetPath = firstNonEmpty(receipt.ActivePath, located.TargetPath)
		}
		_, present := open[receipt.OperationID]
		out = append(out, PendingReceipt{
			OperationID: receipt.OperationID, BindingID: receipt.ClientBindingID,
			InstallationID: installationID, TargetPath: targetPath, Phase: receipt.Phase,
			JournalPresent: present,
		})
	}
	for _, receipt := range state.TransactionReceipts {
		add(receipt, "", "")
	}
	for _, installation := range state.Installations {
		for _, binding := range installation.Clients {
			for _, receipt := range binding.Receipts {
				add(receipt, installation.InstallationID, binding.TargetLocator)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OperationID != out[j].OperationID {
			return out[i].OperationID < out[j].OperationID
		}
		return out[i].Phase < out[j].Phase
	})
	return out
}

func journalDigest(receipt dirswap.Receipt) string {
	if receipt.PublishedDigest != "" {
		return receipt.PublishedDigest
	}
	sum := sha256.Sum256([]byte(receipt.OperationID + "\n" + receipt.Phase + "\n" + receipt.PublishedIdentity))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pendingIdentity(obs RecoveryObservation) []string {
	keys := make([]string, 0, len(obs.Journals)+len(obs.Receipts))
	for _, journal := range obs.Journals {
		keys = append(keys, "journal|"+journal.OperationID+"|"+journal.Phase+"|"+journal.Digest)
	}
	for _, receipt := range obs.Receipts {
		keys = append(keys, "receipt|"+receipt.OperationID+"|"+receipt.Phase+"|"+receipt.BindingID)
	}
	sort.Strings(keys)
	return keys
}

func liveWithinObserved(live, observed RecoveryObservation) bool {
	want := map[string]bool{}
	for _, key := range pendingIdentity(observed) {
		want[key] = true
	}
	for _, key := range pendingIdentity(live) {
		if !want[key] {
			return false
		}
	}
	return true
}

// Recover finishes already recorded UAP transactions for the observed scope.
// It does not install another revision or activate a client. A new pending
// operation that was not in observed returns ErrPlanChanged without recovery.
func (e *Engine) Recover(ctx context.Context, observed Inspection) (Result, error) {
	if ctx == nil {
		return Result{Outcome: OutcomeIncomplete, Reason: "context is required"}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := e.ensureDirs(); err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	svc := e.lifecycle(nil, BindingFacts{})
	release, err := svc.Lock.Acquire(ctx)
	if err != nil {
		return Result{Outcome: OutcomeIncomplete, Reason: err.Error()}, err
	}
	defer func() { _ = release() }()
	if observed.StateRoot != "" && observed.StateRoot != e.cfg.StateRoot {
		return Result{Outcome: OutcomeConflict, Reason: "plan_changed"}, ErrPlanChanged
	}
	live, err := e.observe()
	if err != nil {
		result := Result{Outcome: OutcomeRecovery, Reason: live.Recovery.Reason}
		if result.Reason == "" {
			result.Reason = err.Error()
		}
		return result, fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
	}
	if live.Recovery.Reason != "" {
		return Result{Outcome: OutcomeRecovery, Reason: live.Recovery.Reason}, ErrRecoveryRequired
	}
	if !liveWithinObserved(live.Recovery, observed.Recovery) {
		return Result{Outcome: OutcomeConflict, Reason: "plan_changed"}, ErrPlanChanged
	}
	if !live.Recovery.Required {
		return Result{Outcome: OutcomeUnchanged, Reason: "already_recovered"}, nil
	}
	if err := svc.Kernel.Recover(ctx); err != nil {
		return Result{Outcome: OutcomeRecovery, Reason: err.Error()}, fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
	}
	after, afterErr := e.observe()
	if afterErr != nil || after.Recovery.Required {
		reason := after.Recovery.Reason
		if afterErr != nil && reason == "" {
			reason = afterErr.Error()
		}
		if reason == "" {
			reason = "pending transactions remain"
		}
		return Result{Outcome: OutcomeRecovery, Reason: reason}, fmt.Errorf("%w: %s", ErrRecoveryRequired, reason)
	}
	return Result{Outcome: OutcomeCompleted}, nil
}

// RecoverCurrent inspects this root and recovers that exact live scope.
func (e *Engine) RecoverCurrent(ctx context.Context) (Result, error) {
	view, err := e.Inspect(ctx)
	if err != nil {
		reason := view.Recovery.Reason
		if reason == "" {
			reason = err.Error()
		}
		return Result{Outcome: OutcomeRecovery, Reason: reason}, err
	}
	return e.Recover(ctx, view)
}
