package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func install(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, out Result) (Result, error) {
	if snap.Ledger.PendingMutation != nil {
		reservation := *snap.Ledger.PendingMutation
		out.reservation = &reservation
	}
	explicitGlobal, explicitPrimary := req.GlobalConfig, req.Primary
	var releasePackage func()
	defer func() {
		if releasePackage != nil {
			releasePackage()
		}
	}()
	id := portablesetup.Identity{}
	mat := portablesetup.Materializer{}
	if len(notifyAgents) > 0 {
		recordedPath, recordedVersion := desiredPackage(req)
		reportProgress(req, "prepare")
		if !explicitAbs(req.PackageRoot) && recordedPath == "" && recordedVersion != "" && req.ReleaseDownloadRoot == "" && req.PackageFetcher == nil {
			out.Outcome, out.Reason = "incomplete", "recorded_package_unavailable"
			return out, ErrRefused
		}
		packageRoot, release, err := resolvePackageRoot(ctx, req, recordedPath, recordedVersion)
		if err != nil {
			reason := "package_acquisition_failed"
			if strings.Contains(err.Error(), "package_required") {
				reason = "package_required"
			}
			out.Outcome, out.Reason = "incomplete", reason
			return out, err
		}
		releasePackage = release
		req.PackageRoot = packageRoot
		mat, err = materializer(req, snap, runtimeRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		id, err = identity(req, snap, runtimeRoot, mat, req.Action == ActionInstall)
		if err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		req.InstallationID = id.InstallationID
		req.GlobalConfig = id.GlobalConfig
		req.Primary = id.Primary
		out.InstallationID = id.InstallationID
		if req.Action == ActionUpdate || req.Action == ActionRepair {
			if mapped, bindErr := requireLiveNotifyBindings(req, mat, id, notifyAgents, out); bindErr != nil {
				return mapped, bindErr
			}
		}
		if req.Action == ActionRepair {
			bindRepairPackages(ctx, &req, snap, runtimeRoot, notifyAgents)
		}
		if err := recoverWizardJournals(ctx, mat, id, req, notifyAgents); err != nil {
			out.Outcome, out.Reason = "incomplete", "recovery_required"
			return out, err
		}
		if !retainedMetadataUpdate(mat, id, req.Action) {
			if err := reserveClientBindings(&req, mat, notifyAgents); err != nil {
				if mapped, handled := mapAmbiguous(err, out); handled {
					return mapped, err
				}
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
			}
			for _, agent := range notifyAgents {
				if !explicitAbs(clientConfig(req, agent)) {
					out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
					out.Outcome, out.Reason = "incomplete", "client_config_required"
					return out, ErrRefused
				}
				if !explicitAbs(clientExecutable(req, agent)) {
					out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_executable_required"})
					out.Outcome, out.Reason = "incomplete", "client_executable_required"
					return out, ErrRefused
				}
				if err := guardLiveProfile(mat, id.InstallationID, string(agent), clientConfig(req, agent)); err != nil {
					if mapped, handled := mapAmbiguous(err, out); handled {
						return mapped, err
					}
					out.Outcome, out.Reason = "incomplete", err.Error()
					return out, err
				}
				materialize := portablesetup.MaterializeRequest{
					LocalConfig: req.LocalConfig,
					Identity:    id, Integration: agent, ExpectedGeneration: snap.Ledger.Generation,
					PackageRoot: clientPackageRoot(req, agent), ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
					SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
					TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
					Discovery:   discovery(req, agent, runtimeRoot, snap),
					OperationID: wizardMutationID(req.Action, agent, snap.Ledger.Generation),
					Operation:   wizardPackageOp(req.Action),
				}
				if others, err := mat.OtherLiveClients(id.InstallationID, string(agent)); err != nil {
					out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
					out.Outcome, out.Reason = "incomplete", "portable_inspect_failed"
					return out, err
				} else if len(others) > 0 && req.Action == ActionInstall && !liveNotifyClient(mat, id.InstallationID, string(agent)) {
					if err := mat.GuardSecondClient(ctx, materialize); err != nil {
						if portablesetup.IsUpdateRequired(err) {
							return updateRequired(req, agent, others, nil, nil, out, err)
						}
						out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
						out.Outcome, out.Reason = "incomplete", "portable_install_failed"
						return out, err
					}
				}
			}
		}
	}
	if !retainedMetadataUpdate(mat, id, req.Action) && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair) && len(notifyAgents) > 0 {
		failed, reason, _, err := bindNotifyPreviews(ctx, &req, req, snap, runtimeRoot, notifyAgents)
		if err != nil {
			if reason == "" {
				return mapPreviewFailure(ctx, req, failed, mat, id, err, out)
			}
			out.Outcome, out.Reason = "incomplete", reason
			return out, err
		}
	}
	if (req.Action == ActionUpdate || req.Action == ActionRepair) && !retainedMetadataUpdate(mat, id, req.Action) {
		if err := prepareLegacyMigrations(&req, snap, mat, id, notifyAgents, explicitGlobal, explicitPrimary); err != nil {
			out.Outcome, out.Reason = "incomplete", "migration_preflight_failed"
			return out, err
		}
	}
	if len(notifyAgents) > 0 && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair) {
		if snap.Ledger.PendingMutation != nil {
			intent, err := portablesetup.ReadIntent(req.ControlRoot)
			if err != nil {
				out.Outcome, out.Reason = "incomplete", "pending_intent_unreadable"
				return out, err
			}
			if intent.GlobalConfig == "" {
				if err := (portablesetup.Service{ExpectedPolicy: req.BootstrapExpectedPolicy}).PatchIntentGlobalConfig(ctx, req.ControlRoot, runtimeRoot, snap.Ledger.Owner, id.InstallationID, id.GlobalConfig, out.reservation); err != nil {
					out.Outcome, out.Reason = "incomplete", "pending_intent_patch_failed"
					if errors.Is(err, portablesetup.ErrIntentConflict) {
						out.Outcome, out.Reason = "conflict", "pending_intent_conflict"
					}
					return out, err
				}
				snap, err = readWizardSnapshot(req.ControlRoot, out.reservation)
				if err != nil {
					out.Outcome, out.Reason = "incomplete", "pending_intent_unreadable"
					return out, err
				}
			}
		}
	}
	// Freeze every client identity before publishing intent or changing hooks.
	notifyIdentities := make(map[portable.Integration]portablesetup.Identity, len(notifyAgents))
	if len(notifyAgents) > 0 && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair) {
		for _, agent := range notifyAgents {
			target, err := targetIdentity(id, req, snap, mat, agent)
			if err != nil {
				out.Outcome, out.Reason = "incomplete", "binding_identity_invalid"
				return out, err
			}
			notifyIdentities[agent] = target
		}
		for _, agent := range notifyAgents {
			var err error
			path := notifyIdentities[agent].GlobalConfig
			_, migrating := req.MigrationBindings[string(agent)]
			if migrating || !liveNotifyClient(mat, id.InstallationID, string(agent)) {
				err = config.PrepareGlobalConfigParent(path)
			}
			if err == nil {
				err = portable.ValidateGlobalConfigParent(path)
			}
			if err != nil {
				out.Outcome, out.Reason = "incomplete", "global_config_parent_invalid"
				return out, err
			}
		}
	}
	reportProgress(req, "preflight")
	snap, reservation, err := publishWizardIntent(ctx, req, snap, runtimeRoot, hookAgents, notifyAgents, true)
	out.reservation = reservation
	if err != nil {
		if errors.Is(err, portablesetup.ErrConcurrentChange) {
			out.Outcome, out.Reason = "conflict", "concurrent_change"
			return out, err
		}
		if conflict, handled := pendingIntentConflict(req, err, out); handled {
			return conflict, err
		}
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	out.Generation = snap.Ledger.Generation
	if len(hookAgents) > 0 {
		reportProgress(req, "hooks")
		out, err = applyHooks(ctx, req, hookAgents, snap, false, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		current, err := readWizardSnapshot(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = current.Ledger.Generation
		snap = current
	}
	if len(notifyAgents) == 0 {
		out.Outcome = "completed"
		reportProgress(req, "complete")
		return out, nil
	}
	if retainedMetadataUpdate(mat, id, req.Action) {
		materialize := portablesetup.MaterializeRequest{
			LocalConfig: req.LocalConfig,
			Identity:    id, Integration: notifyAgents[0], ExpectedGeneration: snap.Ledger.Generation,
			PackageRoot: req.PackageRoot, ClientConfigRoot: clientConfig(req, notifyAgents[0]), ClientExecutable: clientExecutable(req, notifyAgents[0]),
			OperationID: wizardMutationID(req.Action, notifyAgents[0], snap.Ledger.Generation),
		}
		if switched, err := mat.SwitchRetained(ctx, materialize); err != nil {
			return mapSwitchRetainedFailure(err, out)
		} else {
			out.NextActions = append(out.NextActions, installerNextActions(switched.NextActions)...)
		}
		state, loadErr := mat.Store.Load()
		if loadErr != nil {
			out.Outcome, out.Reason = "incomplete", loadErr.Error()
			return out, loadErr
		}
		recorded := ""
		for _, installation := range state.Installations {
			if installation.InstallationID == id.InstallationID {
				recorded = installation.Source.TreeDigest
				if len(installation.Clients) != 0 {
					out.Outcome, out.Reason = "incomplete", "retained_source_not_durable"
					return out, fmt.Errorf("retained switch materialized a client")
				}
				break
			}
		}
		if recorded == "" {
			out.Outcome, out.Reason = "incomplete", "retained_source_not_durable"
			return out, fmt.Errorf("retained source digest is missing after switch")
		}
		out.InstallationID = id.InstallationID
		out.Outcome, out.Reason = "completed", "retained_source_updated"
		reportProgress(req, "complete")
		return out, nil
	}
	reportProgress(req, "agent-notify")
	generation := snap.Ledger.Generation
	if len(req.MigrationBindings) == 0 && canGroupNotify(mat, id, req, notifyAgents) {
		var reqs []portablesetup.MaterializeRequest
		for _, agent := range notifyAgents {
			reqs = append(reqs, portablesetup.MaterializeRequest{
				LocalConfig: req.LocalConfig,
				Identity:    notifyIdentities[agent], Integration: agent, ExpectedGeneration: generation,
				PackageRoot: clientPackageRoot(req, agent), ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
				SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
				TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
				Discovery:       discovery(req, agent, runtimeRoot, snap),
				OperationID:     wizardMutationID(req.Action, "group", snap.Ledger.Generation),
				KeepReservation: true,
				Operation:       wizardPackageOp(req.Action),
			})
		}
		got, err := mat.ApplyGroup(ctx, reqs)
		if err != nil {
			if portablesetup.IsUpdateRequired(err) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(notifyAgents[0]))
				return updateRequired(req, notifyAgents[0], others, liveUpdateAgents(ctx, req, mat, id, notifyAgents[0]), unboundSelectedAgents(req, mat, id, notifyAgents[0]), out, err)
			}
			if errors.Is(err, portablesetup.ErrSourceIdentityDrift) {
				out.Outcome, out.Reason = "incomplete", "source_identity_drift"
				return out, err
			}
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			return groupNotifyFailed(notifyAgents, req, out, err), err
		}
		if len(got) != len(notifyAgents) {
			out.Outcome, out.Reason = "incomplete", "portable_install_failed"
			return out, fmt.Errorf("group apply returned %d bindings", len(got))
		}
		generation, err = rereadWizardGeneration(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		for i, agent := range notifyAgents {
			out.Targets = append(out.Targets, completedNotifyTarget(mat, got[i].InstallationID, string(agent), got[i].BindingID))
			id.InstallationID = got[i].InstallationID
			_ = persistKnownReceipt(ctx, req, runtimeRoot, mat, id.InstallationID, string(agent), out.reservation)
			if err := recordLiveProfile(got[i].DataRoot, string(agent), clientConfig(req, agent)); err != nil {
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
			}
		}
		out.Outcome = "completed"
		reportProgress(req, "complete")
		return out, nil
	}
	for _, agent := range notifyAgents {
		migration, migrating := req.MigrationBindings[string(agent)]
		target, targetErr := targetIdentity(id, req, snap, mat, agent)
		if targetErr != nil {
			return portableInstallFailed(agent, req, out, targetErr), targetErr
		}
		materialize := portablesetup.MaterializeRequest{
			LocalConfig: req.LocalConfig,
			Identity:    target, Integration: agent, ExpectedGeneration: generation,
			PackageRoot: clientPackageRoot(req, agent), ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
			SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
			TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
			Discovery:       discovery(req, agent, runtimeRoot, snap),
			OperationID:     wizardMutationID(req.Action, agent, generation),
			KeepReservation: true,
			Operation:       wizardPackageOp(req.Action),
		}
		var got portable.Binding
		var err error
		if migrating {
			materialize.OperationID += "-projection"
			current, readErr := readWizardSnapshot(req.ControlRoot, out.reservation)
			if readErr != nil {
				return portableInstallFailed(agent, req, out, readErr), readErr
			}
			if portable.ExactCommittedBinding(current.Ledger, migration.New) {
				if portable.ExactCommittedBinding(current.Ledger, migration.Old) {
					// UAP can still be in prepared/failed activation after its
					// callback committed the new consumer. Re-enter its lifecycle.
					materialize.HandoffAction = string(req.Action)
					got, err = mat.RefreshProjection(ctx, materialize)
					if err == nil && got.Version != 0 && got != migration.New {
						err = fmt.Errorf("%w: refreshed binding differs from confirmed intent", ErrRefused)
					}
				}
				if err == nil {
					got = migration.New
				}
			} else if liveDigest := liveBindingTreeDigest(mat, id.InstallationID, string(agent)); liveDigest != "" && (req.TreeDigest == "" || req.TreeDigest == liveDigest) {
				if !liveManagedTargetsPresent(mat, id.InstallationID, []portable.Integration{agent}) {
					// Restore the exact old projection first when its managed package
					// vanished. RefreshProjection only accepts an intact package.
					oldRepair := materialize
					oldRepair.Identity = identityFromBinding(id, migration.Old)
					oldRepair.Operation = uapinstaller.OpRepair
					oldRepair.OperationID = wizardMutationID(req.Action, agent, generation) + "-restore"
					oldRepair.HandoffAction = string(req.Action)
					if _, err = mat.RestoreMigrationProjection(ctx, oldRepair); err != nil {
						return portableInstallFailed(agent, req, out, err), err
					}
					materialize.ExpectedGeneration, err = rereadWizardGeneration(req.ControlRoot, out.reservation)
					if err != nil {
						return portableInstallFailed(agent, req, out, err), err
					}
				}
				materialize.HandoffAction = string(req.Action)
				got, err = mat.RefreshProjection(ctx, materialize)
			} else if req.Action == ActionUpdate {
				got, err = mat.Update(ctx, materialize)
			} else {
				got, err = mat.Repair(ctx, materialize)
			}
		} else {
			switch req.Action {
			case ActionUpdate:
				got, err = mat.Update(ctx, materialize)
			case ActionRepair:
				got, err = mat.Repair(ctx, materialize)
			default:
				got, err = mat.Install(ctx, materialize)
			}
		}
		if err != nil {
			if req.Action == ActionRepair && (exactRepairRevision(err) || portablesetup.IsUpdateRequired(err)) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
				if len(others) > 0 {
					live := liveBindingTreeDigest(mat, id.InstallationID, string(agent))
					repairRetry := req
					repairRetry.Agents = []string{string(agent)}
					repairRetry.PackageRoot = ""
					updateRetry := req
					updateRetry.Action = ActionUpdate
					updateRetry.Agents = []string{string(agent)}
					out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "exact_revision_required", TreeDigest: live})
					out.NextActions = append(out.NextActions,
						NextAction{Kind: "repair", Agents: []string{string(agent)}, Reason: "exact_revision_required", Command: localRetryCommand(repairRetry)},
						NextAction{Kind: "update", Agents: []string{string(agent)}, Reason: "exact_revision_required", Command: localRetryCommand(updateRetry)},
					)
					continue
				}
			}
			if portablesetup.IsUpdateRequired(err) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
				return updateRequired(req, agent, others, liveUpdateAgents(ctx, req, mat, id, agent), unboundSelectedAgents(req, mat, id, agent), out, err)
			}
			if errors.Is(err, portablesetup.ErrSourceIdentityDrift) {
				out.Outcome, out.Reason = "incomplete", "source_identity_drift"
				return out, err
			}
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			if migrating {
				failed := portableInstallFailed(agent, req, out, err)
				if failed.Reason == "activation_incomplete" {
					failed.Reason = "migration_activation_pending"
					for i := range failed.NextActions {
						if failed.NextActions[i].Kind == "activate" && len(failed.NextActions[i].Command) > 0 {
							failed.NextActions[i].Reason = "migration_activation_pending"
						}
					}
				}
				return failed, err
			}
			return portableInstallFailed(agent, req, out, err), err
		}
		if migrating {
			current, readErr := readWizardSnapshot(req.ControlRoot, out.reservation)
			if readErr != nil || current.Ledger.PendingMutation == nil {
				if readErr == nil {
					readErr = fmt.Errorf("%w: migration reservation missing", ErrRefused)
				}
				return portableInstallFailed(agent, req, out, readErr), readErr
			}
			if ready, readyErr := migrationAlreadyProjected(current.Ledger, migration); readyErr != nil {
				return portableInstallFailed(agent, req, out, readyErr), readyErr
			} else if !ready && portable.ExactCommittedBinding(current.Ledger, migration.New) {
				reservation := *current.Ledger.PendingMutation
				if _, publishErr := mat.Kernel.CommitBinding(ctx, portablesetup.Request{Binding: migration.New, ExpectedGeneration: current.Ledger.Generation, Reservation: &reservation}); publishErr != nil {
					return portableInstallFailed(agent, req, out, publishErr), publishErr
				}
			}
			if err := replaceMigratedBinding(ctx, req, mat, migration, got); err != nil {
				return portableInstallFailed(agent, req, out, err), err
			}
		}
		generation, err = rereadWizardGeneration(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		out.Targets = append(out.Targets, completedNotifyTarget(mat, got.InstallationID, string(agent), got.BindingID))
		id.InstallationID = got.InstallationID
		_ = persistKnownReceipt(ctx, req, runtimeRoot, mat, id.InstallationID, string(agent), out.reservation)
		if err := recordLiveProfile(got.DataRoot, string(agent), clientConfig(req, agent)); err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
	}
	for _, target := range out.Targets {
		if target.Unit == "agent-notify" && target.Reason == "exact_revision_required" {
			out.Outcome, out.Reason = "incomplete", "exact_revision_required"
			reportProgress(req, "complete")
			return out, ErrRefused
		}
	}
	out.Outcome = "completed"
	reportProgress(req, "complete")
	return out, nil
}
