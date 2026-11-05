package setupwizard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func portableInstallFailed(agent portable.Integration, req Request, out Result, err error) Result {
	var finalized portablesetup.ResultError
	if errors.As(err, &finalized) && finalized.Finalization {
		return portableFinalizationFailed([]portable.Integration{agent}, req, out, finalized)
	}
	if errors.Is(err, uapinstaller.ErrNotInstalled) {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "not_installed"})
		out.Outcome, out.Reason = "incomplete", "not_installed"
		return out
	}
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		return siblingCompatibilityUnavailable(agent, req, out)
	}
	out.Outcome, out.Reason = "incomplete", "portable_install_failed"
	var persisted portablesetup.ResultError
	if errors.As(err, &persisted) {
		out.Targets = append(out.Targets, notifyFailureTarget(string(agent), groupClientResult(persisted.Result, string(agent)), err))
	} else {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
	}
	if errors.As(err, &persisted) && (persisted.Result.Client.Materialization == string(domain.MaterializationMaterialized) || persisted.Result.Client.Materialization == string(domain.MaterializationDegraded)) {
		out.Reason = "activation_incomplete"
		out.NextActions = append(out.NextActions, activationRecovery(req, []string{string(agent)}, persisted.Result.Reason))
	}
	return out
}

// A successful UAP result is evidence about that operation, not proof that
// all host finalization completed. Keep its channel facts without relabeling
// an active registration as failed activation or returning overall success.
func portableFinalizationFailed(agents []portable.Integration, req Request, out Result, persisted portablesetup.ResultError) Result {
	out.Outcome, out.Reason = "incomplete", "portable_finalization_incomplete"
	out.DataRetained = out.DataRetained || persisted.Result.DataRetained
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		name := string(agent)
		names = append(names, name)
		item := groupClientResult(persisted.Result, name)
		out.Targets = append(out.Targets, TargetResult{Client: name, Unit: "agent-notify", Outcome: "incomplete", Reason: "finalization_incomplete",
			Materialization: item.Materialization, Activation: item.Activation, GroupPhase: item.GroupPhase})
	}
	out.NextActions = append(out.NextActions, operationRecovery(req, names, out.Reason, "resume"))
	return out
}

func siblingCompatibilityUnavailable(agent portable.Integration, req Request, out Result) Result {
	out.Outcome, out.Reason = "incomplete", "sibling_compatibility_unavailable"
	if agent != "" {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: out.Reason})
	}
	// The known two-client recovery applies only to Claude/Codex. An SDK
	// compatibility refusal for Pi, Cursor or Local must never propose
	// mutating unrelated, unselected Claude/Codex bindings.
	knownPair := agent == portable.Claude || agent == portable.Codex
	for _, selected := range req.Agents {
		if selected != string(portable.Claude) && selected != string(portable.Codex) {
			knownPair = false
			break
		}
	}
	if !knownPair {
		// A failed compatibility preflight must not carry an earlier
		// recommendation to mutate unselected clients, whether it came
		// from an individual Plan preview or a grouped Apply.
		out.NextActions = nil
		out.Command = nil
		return out
	}
	retry := req
	retry.Action = ActionUpdate
	retry.Yes = true
	retry.Agents = []string{"claude", "codex"}
	out.NextActions = []NextAction{{
		Kind: "update", Agents: retry.Agents, Reason: out.Reason, Command: localRetryCommand(retry),
	}}
	return out
}

func groupNotifyFailed(agents []portable.Integration, req Request, out Result, err error) Result {
	var handoff portablesetup.GroupHandoffError
	if errors.As(err, &handoff) {
		out = portableInstallFailed(handoff.Integration, req, out, err)
		names := make([]string, 0, len(agents))
		for _, agent := range agents {
			names = append(names, string(agent))
		}
		if next := operationRecovery(req, names, out.Reason, "resume"); len(next.Command) != 0 {
			out.NextActions = append(out.NextActions, next)
		}
		return out
	}
	var persisted portablesetup.ResultError
	if errors.As(err, &persisted) && persisted.Finalization {
		return portableFinalizationFailed(agents, req, out, persisted)
	}
	// A group preflight may report a bare SDK error with no persisted
	// per-client facts. Even then, the entire selected group must be checked
	// before offering a legacy Claude/Codex recovery.
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		unsupportedSelection := false
		for _, agent := range agents {
			if agent != portable.Claude && agent != portable.Codex {
				unsupportedSelection = true
				break
			}
		}
		if unsupportedSelection {
			// The selection controls whether recovery is safe, but the
			// reported failed client still owns the diagnostic target.
			failed := portable.Integration("")
			if len(agents) != 0 {
				failed = agents[0]
			}
			if errors.As(err, &persisted) {
				for _, agent := range agents {
					if string(agent) == persisted.Result.Client.ClientID {
						failed = agent
						break
					}
				}
			}
			return siblingCompatibilityUnavailable(failed, req, out)
		}
	}
	if !errors.As(err, &persisted) || (len(persisted.Result.Targets) == 0 && persisted.Result.Client.ClientID == "") {
		if len(agents) == 0 {
			return portableInstallFailed("", req, out, err)
		}
		return portableInstallFailed(agents[0], req, out, err)
	}
	if errors.Is(err, uapinstaller.ErrNotInstalled) {
		for _, agent := range agents {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "not_installed"})
		}
		out.Outcome, out.Reason = "incomplete", "not_installed"
		return out
	}
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		if len(agents) == 0 {
			return siblingCompatibilityUnavailable("", req, out)
		}
		return siblingCompatibilityUnavailable(agents[0], req, out)
	}
	committed := false
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, string(agent))
		item := groupClientResult(persisted.Result, string(agent))
		target := notifyFailureTarget(string(agent), item, err)
		if item.Materialization == string(domain.MaterializationMaterialized) || item.Materialization == string(domain.MaterializationDegraded) {
			committed = true
		}
		out.Targets = append(out.Targets, target)
	}
	out.Outcome, out.Reason = "incomplete", "portable_install_failed"
	if committed {
		out.Reason = "activation_incomplete"
		out.NextActions = append(out.NextActions, activationRecovery(req, names, persisted.Result.Reason))
	}
	return out
}

// Keep saved registration separate from the current external operation. An
// active state from an earlier invocation cannot turn a failed attempt green.
func notifyFailureTarget(client string, item uapinstaller.ClientResult, err error) TargetResult {
	target := TargetResult{Client: client, Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error(),
		Materialization: item.Materialization, Activation: item.Activation, GroupPhase: item.GroupPhase}
	if item.Failure != nil {
		target.FailureStage = item.Failure.Stage
		target.Reason = item.Failure.Message
	}
	switch item.GroupPhase {
	case "external_completed":
		if item.Materialization == string(domain.MaterializationMaterialized) || item.Materialization == string(domain.MaterializationDegraded) {
			target.Outcome, target.Reason = "completed", ""
		} else {
			// The external attempt succeeded, but a missing saved-state read
			// cannot prove the independently committed registration.
			target.Reason = "state_requires_inspection"
		}
	case "external_not_attempted":
		target.Outcome = "activation_not_attempted"
	case "external_failed":
		target.Outcome = "activation_failed"
		switch target.FailureStage {
		case "timeout":
			target.Outcome = "activation_timeout"
		case "canceled":
			target.Outcome = "activation_canceled"
		case "deadline":
			target.Outcome = "activation_deadline"
		}
	default:
		if item.Materialization == string(domain.MaterializationMaterialized) || item.Materialization == string(domain.MaterializationDegraded) {
			target.Reason = "activation_incomplete"
		}
	}
	return target
}

func activationRecovery(req Request, names []string, reason string) NextAction {
	return operationRecovery(req, names, reason, "activate")
}

// Both activation and finalization resume only the existing, verified intent.
// The action label does not grant authority to retry any side effect here.
func operationRecovery(req Request, names []string, reason, kind string) NextAction {
	next := NextAction{Kind: kind, Agents: names, Reason: "recovery_unavailable"}
	if !explicitAbs(req.ControlRoot) {
		return next
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	intent, err := portablesetup.ReadIntent(req.ControlRoot)
	policy, snapshotErr := installruntime.ReadPolicySnapshot(ctx, req.ControlRoot)
	snap := policy.Installation
	if err != nil || snapshotErr != nil || snap.Recovery || snap.Ledger.PendingMutation == nil || snap.Ledger.PendingMutation.Owner != "existing-installer" || snap.Ledger.PendingMutation.ID != intent.SetupIntentID {
		return next
	}
	if req.BootstrapExpectedPolicy != nil && *req.BootstrapExpectedPolicy != policy.Preimage {
		return next
	}
	selected := req.Agents
	if len(selected) == 0 {
		selected = names
	}
	agents, err := normalizeAgents(selected)
	if err != nil {
		return next
	}
	retry, _, err := restoreOmittedFromIntent(req, agents, intent)
	if err != nil {
		return next
	}
	retry.Yes = true
	if retry.RuntimeRoot == "" {
		retry.RuntimeRoot = snap.Ledger.RuntimeRoot
	}
	if retry.Helper == "" {
		retry.Helper = filepath.Join(retry.RuntimeRoot, intent.Primary)
	}
	// Presentation validates the normalized acquired directory read-only. It
	// cannot fetch or extract another archive, even on cancellation.
	info, err := os.Lstat(retry.PackageRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || validatePendingPackage(ctx, retry) != nil {
		return next
	}
	for _, agent := range agents {
		executable := clientExecutable(retry, agent)
		info, err := os.Stat(executable)
		if !explicitAbs(executable) || err != nil || !info.Mode().IsRegular() {
			return next
		}
	}
	next.Reason, next.Command = reason, localRetryCommand(retry)
	return next
}

func groupClientResult(result uapinstaller.Result, clientID string) uapinstaller.ClientResult {
	clientID = portablesetup.ClientID(portablesetup.Integration(clientID))
	for _, item := range result.Targets {
		if item.ClientID == clientID {
			return item
		}
	}
	if result.Client.ClientID == clientID {
		return result.Client
	}
	return uapinstaller.ClientResult{}
}

func groupRemoveFailed(agents []portable.Integration, req Request, out Result, err error) Result {
	out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
	names := make([]string, 0, len(agents))
	var persisted portablesetup.ResultError
	hasPersist := errors.As(err, &persisted)
	if hasPersist && persisted.Finalization {
		out.Reason = "portable_finalization_incomplete"
		out.DataRetained = out.DataRetained || persisted.Result.DataRetained
	}
	for _, agent := range agents {
		names = append(names, string(agent))
		target := TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()}
		if hasPersist {
			item := groupClientResult(persisted.Result, string(agent))
			target.Materialization, target.Activation, target.GroupPhase = item.Materialization, item.Activation, item.GroupPhase
			// Successful single and group removals can leave Materialization
			// empty in the SDK. Success proves absence only for clients explicitly
			// named by that result, not for an unrelated requested client.
			clientID := portablesetup.ClientID(agent)
			removed := persisted.Finalization && persisted.Result.Operation == uapinstaller.OpRemove &&
				(persisted.Result.Outcome == uapinstaller.OutcomeCompleted || persisted.Result.Outcome == uapinstaller.OutcomeUnchanged) &&
				clientID != "" && (persisted.Result.Binding.ClientID == clientID || item.ClientID == clientID)
			if removed || item.ClientID != "" && item.Materialization == string(domain.MaterializationAbsent) {
				target.Outcome, target.Reason = "unchanged", "already_absent"
				target.Materialization = string(domain.MaterializationAbsent)
				// An explicit absent target was absent before this group
				// operation. Otherwise a completed removal performed the work.
				if removed && persisted.Result.Outcome == uapinstaller.OutcomeCompleted && item.Materialization != string(domain.MaterializationAbsent) {
					target.Outcome, target.Reason = "completed", ""
				}
			}
		}
		out.Targets = append(out.Targets, target)
	}
	retry := req
	retry.Action = ActionUninstall
	retry.Yes = true
	if explicitAbs(req.ControlRoot) {
		if intent, readErr := portablesetup.ReadIntent(req.ControlRoot); readErr == nil {
			retry = retryRequestFromIntent(retry, intent)
		}
	}
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "uninstall", Agents: names, Reason: out.Reason,
		Command: localRetryCommand(retry),
	})
	return out
}

func portableRemoveFailed(agent portable.Integration, req Request, out Result, err error) Result {
	var persisted portablesetup.ResultError
	if errors.As(err, &persisted) {
		return groupRemoveFailed([]portable.Integration{agent}, req, out, err)
	}
	out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
	out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
	return out
}

func mapSwitchRetainedFailure(err error, out Result) (Result, error) {
	var persisted portablesetup.ResultError
	if errors.As(err, &persisted) && persisted.Finalization {
		out.Outcome, out.Reason = "incomplete", "portable_finalization_incomplete"
		out.DataRetained = out.DataRetained || persisted.Result.DataRetained
		return out, err
	}
	if errors.As(err, &persisted) && persisted.Result.Reason != "" {
		out.Outcome, out.Reason = string(persisted.Result.Outcome), persisted.Result.Reason
		if out.Outcome == "" {
			out.Outcome = "incomplete"
		}
		return out, err
	}
	if errors.Is(err, uapinstaller.ErrAssessmentRejected) {
		out.Outcome, out.Reason = "incomplete", "assessment_rejected"
		return out, err
	}
	out.Outcome, out.Reason = "incomplete", "portable_preflight_failed"
	return out, err
}
