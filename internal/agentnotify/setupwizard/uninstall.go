package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/shared"
)

func holdCodexUninstall(ctx context.Context, req *Request, mat portablesetup.Materializer, id portablesetup.Identity, generation uint64, notifyAgents []portable.Integration, runtimeRoot string, out Result) (Result, error) {
	if req.ExternalUninstalled || id.InstallationID == "" {
		return out, nil
	}
	for _, agent := range notifyAgents {
		if agent != portable.Codex || !liveNotifyClient(mat, id.InstallationID, string(agent)) {
			continue
		}
		snap, err := readWizardSnapshot(req.ControlRoot, out.reservation)
		if err != nil {
			return out, err
		}
		target, err := targetIdentity(id, *req, snap, mat, agent)
		if err != nil {
			return out, err
		}
		if attestCodexExternalUninstall(ctx, clientExecutable(*req, agent), req.CodexHome, managedCodexPluginSpec(mat, id.InstallationID)) {
			req.ExternalUninstalled = true
			_ = persistExternalUninstalled(ctx, *req, runtimeRoot, out.reservation)
			return out, nil
		}
		err = mat.Remove(ctx, portablesetup.MaterializeRequest{
			LocalConfig: req.LocalConfig,
			Identity:    target, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: clientConfig(*req, agent), ClientExecutable: clientExecutable(*req, agent),
			OperationID: wizardMutationID(ActionUninstall, agent, generation), ExternalUninstalled: req.ExternalUninstalled,
			HoldOnly: true, KeepReservation: true,
		})
		if err == nil {
			return out, nil
		}
		if conflict, handled := pendingIntentConflict(*req, err, out); handled {
			return conflict, err
		}
		if errors.Is(err, portablesetup.ErrAlreadyAbsent) {
			return out, nil
		}
		if errors.Is(err, portablesetup.ErrExternalUninstall) {
			return externalUninstallRequired(*req, agent, out), err
		}
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
		out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
		return out, err
	}
	return out, nil
}

func externalUninstallRequired(req Request, agent portable.Integration, out Result) Result {
	retry := req
	retry.ExternalUninstalled = true
	out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "external_uninstall_required"})
	out.Outcome, out.Reason = "incomplete", "external_uninstall_required"
	out.Command = localRetryCommand(retry)
	out.NextActions = []NextAction{{
		Kind: "external-uninstall", Agents: []string{string(agent)},
		Command: localRetryCommand(retry), Reason: "attest_codex_plugin_removed",
	}}
	return out
}

func uninstallManualPrerequisites(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, notifyAgents []portable.Integration) []string {
	if req.ExternalUninstalled {
		return nil
	}
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return nil
	}
	id, err := identity(req, snap, runtimeRoot, mat, false)
	if err != nil {
		return nil
	}
	if id.InstallationID == "" {
		state, loadErr := mat.Store.Load()
		if loadErr != nil || len(state.Installations) != 1 {
			return nil
		}
		id.InstallationID = state.Installations[0].InstallationID
	}
	var prereqs []string
	expectedSpec := managedCodexPluginSpec(mat, id.InstallationID)
	for _, agent := range notifyAgents {
		if agent != portable.Codex || !liveNotifyClient(mat, id.InstallationID, string(agent)) {
			continue
		}
		status := codexListUnknown
		if explicitAbs(clientExecutable(req, agent)) {
			status, _ = observeCodexPluginList(ctx, clientExecutable(req, agent), clientConfig(req, agent), expectedSpec)
		}
		if status != codexListAbsent {
			prereqs = append(prereqs, string(agent))
		}
	}
	return prereqs
}

func uninstall(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, out Result) (Result, error) {
	if snap.Ledger.PendingMutation != nil {
		reservation := *snap.Ledger.PendingMutation
		out.reservation = &reservation
	}
	mat := portablesetup.Materializer{}
	id := portablesetup.Identity{}
	if len(notifyAgents) > 0 {
		var err error
		mat, err = materializer(req, snap, runtimeRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		id, err = identity(req, snap, runtimeRoot, mat, false)
		if err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		if id.InstallationID == "" {
			state, loadErr := mat.Store.Load()
			if loadErr != nil {
				out.Outcome, out.Reason = "incomplete", loadErr.Error()
				return out, loadErr
			}
			if len(state.Installations) == 1 {
				id.InstallationID = state.Installations[0].InstallationID
			}
		}
		req.InstallationID = id.InstallationID
		req.Primary = id.Primary
		out.InstallationID = id.InstallationID
		// §7.5: Recover is an application step, not Prepare(remove).
		if err := recoverWizardJournals(ctx, mat, id, req, notifyAgents); err != nil {
			out.Outcome, out.Reason = "incomplete", "recovery_required"
			return out, err
		}
		if err := reserveClientBindings(&req, mat, notifyAgents); err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
	}
	retainedEmpty := false
	if id.InstallationID != "" {
		var emptyErr error
		retainedEmpty, emptyErr = mat.RetainedEmpty(id.InstallationID)
		if emptyErr != nil {
			out.Outcome, out.Reason = "incomplete", emptyErr.Error()
			return out, emptyErr
		}
	}
	if unitFlagsOmitted(req) {
		var managedHooks []portable.Integration
		for _, agent := range hookAgents {
			if hooksManaged(req, agent) {
				managedHooks = append(managedHooks, agent)
			}
		}
		hookAgents = managedHooks
		if id.InstallationID != "" && !retainedEmpty {
			var managedNotify []portable.Integration
			for _, agent := range notifyAgents {
				if liveNotifyClient(mat, id.InstallationID, string(agent)) {
					managedNotify = append(managedNotify, agent)
				}
			}
			notifyAgents = managedNotify
		}
		if len(hookAgents) == 0 && len(notifyAgents) == 0 {
			out.Outcome, out.Reason = "cancelled", "empty_units"
			reportProgress(req, "complete")
			return out, nil
		}
	}
	notifyIdentities := make(map[portable.Integration]portablesetup.Identity, len(notifyAgents))
	portablePresent := id.InstallationID != "" && !retainedEmpty
	if portablePresent {
		for _, agent := range notifyAgents {
			if !explicitAbs(clientConfig(req, agent)) {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
				out.Outcome, out.Reason = "incomplete", "client_config_required"
				return out, ErrRefused
			}
			if agent != portable.Pi && !explicitAbs(clientExecutable(req, agent)) {
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
		}
		if err := prepareUninstallBindings(&req, snap, mat, id, notifyAgents); err != nil {
			out.Outcome, out.Reason = "incomplete", "removal_identity_invalid"
			return out, err
		}
		for _, agent := range notifyAgents {
			target, err := targetIdentity(id, req, snap, mat, agent)
			if err != nil {
				out.Outcome, out.Reason = "incomplete", "binding_identity_invalid"
				return out, err
			}
			notifyIdentities[agent] = target
		}
	}
	reportProgress(req, "preflight")
	snap, reservation, err := publishWizardIntent(ctx, req, snap, runtimeRoot, hookAgents, notifyAgents, portablePresent)
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
	_ = persistExternalUninstalled(ctx, req, runtimeRoot, out.reservation)
	out.Generation = snap.Ledger.Generation
	if len(notifyAgents) > 0 {
		// §7.5: persist removal intent, then satisfy Codex attestation before
		// any sibling locator revoke or hooks effect.
		out, err = holdCodexUninstall(ctx, &req, mat, id, snap.Ledger.Generation, notifyAgents, runtimeRoot, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
	}
	removeHooks := func(out Result) (Result, error) {
		if len(hookAgents) == 0 {
			return out, nil
		}
		// Keep the hooks consumer (and its managed helper) alive until UAP has
		// finished removing the portable bindings. Hooks may be the final
		// runtime consumer and their removal can retire that helper.
		reportProgress(req, "hooks")
		current, err := readWizardSnapshot(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out, err = applyHooks(ctx, req, hookAgents, current, true, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		generation, err := rereadWizardGeneration(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		return out, nil
	}
	if len(notifyAgents) == 0 {
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		out.Outcome = "completed"
		reportProgress(req, "complete")
		return out, nil
	}
	if id.InstallationID == "" {
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		out.Outcome, out.Reason = "unchanged", "portable_absent"
		reportProgress(req, "complete")
		return out, nil
	}
	if retainedEmpty {
		for _, agent := range notifyAgents {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
		}
		if out.Outcome == "" {
			out.Outcome, out.Reason = "unchanged", "already_absent"
		}
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		out = markRetainedEmpty(mat, id.InstallationID, out)
		reportProgress(req, "complete")
		return out, nil
	}
	reportProgress(req, "agent-notify")
	generation := snap.Ledger.Generation
	if canGroupRemove(mat, id, req, notifyAgents) {
		dataRoots := make([]string, len(notifyAgents))
		var reqs []portablesetup.MaterializeRequest
		for i, agent := range notifyAgents {
			dataRoots[i] = liveDataRoot(mat, id.InstallationID, string(agent))
			reqs = append(reqs, portablesetup.MaterializeRequest{
				LocalConfig: req.LocalConfig,
				Identity:    notifyIdentities[agent], Integration: agent, ExpectedGeneration: generation,
				ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
				OperationID:         wizardMutationID(req.Action, "group", generation),
				ExternalUninstalled: req.ExternalUninstalled,
				KeepReservation:     true,
			})
		}
		got, err := mat.RemoveGroup(ctx, reqs)
		if err != nil {
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			if errors.Is(err, portablesetup.ErrExternalUninstall) {
				return externalUninstallRequired(req, portable.Codex, out), err
			}
			return groupRemoveFailed(notifyAgents, req, out, err), err
		}
		if len(got) != len(notifyAgents) {
			out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
			return out, fmt.Errorf("group remove returned %d results", len(got))
		}
		removed := 0
		for i, agent := range notifyAgents {
			if err := clearLiveProfile(dataRoots[i], string(agent)); err != nil {
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
			}
			if got[i].AlreadyAbsent {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
				continue
			}
			removed++
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "completed"})
		}
		generation, err = rereadWizardGeneration(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		if removed == 0 {
			if out.Reason == "" {
				out.Reason = "already_absent"
			}
			out.Outcome = "unchanged"
			out = markRetainedEmpty(mat, id.InstallationID, out)
			reportProgress(req, "complete")
			return out, nil
		}
		out.Outcome = "completed"
		out = markRetainedEmpty(mat, id.InstallationID, out)
		reportProgress(req, "complete")
		return out, nil
	}
	removed := 0
	expectedCodexSpec := managedCodexPluginSpec(mat, id.InstallationID)
	for _, agent := range notifyAgents {
		if agent == portable.Codex && !req.ExternalUninstalled {
			if attestCodexExternalUninstall(ctx, clientExecutable(req, agent), req.CodexHome, expectedCodexSpec) {
				req.ExternalUninstalled = true
				_ = persistExternalUninstalled(ctx, req, runtimeRoot, out.reservation)
			}
		}
		target, targetErr := targetIdentity(id, req, snap, mat, agent)
		if targetErr != nil {
			out.Outcome, out.Reason = "incomplete", "binding_identity_invalid"
			return out, targetErr
		}
		remove := portablesetup.MaterializeRequest{
			LocalConfig: req.LocalConfig,
			Identity:    target, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
			// User uninstall does not restore a retired direct MCP.
			OperationID: wizardMutationID(ActionUninstall, agent, generation), ExternalUninstalled: req.ExternalUninstalled,
			HoldOnly:        agent == portable.Codex && !req.ExternalUninstalled,
			KeepReservation: true,
		}
		dataRoot := liveDataRoot(mat, id.InstallationID, string(agent))
		err := mat.Remove(ctx, remove)
		if err != nil {
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			if errors.Is(err, portablesetup.ErrAlreadyAbsent) {
				if clearErr := clearLiveProfile(dataRoot, string(agent)); clearErr != nil {
					out.Outcome, out.Reason = "incomplete", clearErr.Error()
					return out, clearErr
				}
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
				continue
			}
			if errors.Is(err, portablesetup.ErrExternalUninstall) {
				return externalUninstallRequired(req, agent, out), err
			}
			return portableRemoveFailed(agent, req, out, err), err
		}
		if err := clearLiveProfile(dataRoot, string(agent)); err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		removed++
		generation, err = rereadWizardGeneration(req.ControlRoot, out.reservation)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "completed"})
	}
	out, err = removeHooks(out)
	if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
		return out, err
	}
	if removed == 0 {
		if out.Reason == "" {
			out.Reason = "already_absent"
		}
		out.Outcome = "unchanged"
		out = markRetainedEmpty(mat, id.InstallationID, out)
		reportProgress(req, "complete")
		return out, nil
	}
	out.Outcome = "completed"
	out = markRetainedEmpty(mat, id.InstallationID, out)
	reportProgress(req, "complete")
	return out, nil
}

func managedCodexPluginSpec(mat portablesetup.Materializer, installationID string) string {
	if installationID == "" {
		return ""
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID || strings.TrimSpace(installation.DeclaredName) == "" {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID != string(portable.Codex) || strings.TrimSpace(binding.PhysicalArtifact) == "" {
				continue
			}
			return installation.DeclaredName + "@" + shared.ManagedMarketplaceName(binding.PhysicalArtifact)
		}
	}
	return ""
}
