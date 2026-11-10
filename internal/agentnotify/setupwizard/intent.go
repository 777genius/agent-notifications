package setupwizard

import (
	"context"
	"errors"
	"fmt"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func wizardMutationID(action Action, agent portable.Integration, generation uint64) string {
	if action == "" {
		action = ActionInstall
	}
	return fmt.Sprintf("wizard-%s-%s-%d", action, agent, generation)
}

func wizardPackageOp(action Action) uapinstaller.Operation {
	switch action {
	case ActionUpdate:
		return uapinstaller.OpUpdate
	case ActionRepair:
		return uapinstaller.OpRepair
	default:
		return uapinstaller.OpInstall
	}
}

// A refresh may advance generation for this operation, but cannot acquire a
// reservation from an unrelated invocation. The initial token is immutable.
func readWizardSnapshot(controlRoot string, reservation *installruntime.PendingMutation) (installruntime.InstalledSnapshot, error) {
	snap, err := installruntime.ReadInstalledSnapshot(controlRoot)
	if err != nil {
		return snap, err
	}
	pending := snap.Ledger.PendingMutation
	if (pending == nil) != (reservation == nil) || (pending != nil && *pending != *reservation) {
		return snap, portablesetup.ErrConcurrentChange
	}
	return snap, nil
}

func rereadWizardGeneration(controlRoot string, reservation *installruntime.PendingMutation) (uint64, error) {
	snap, err := readWizardSnapshot(controlRoot, reservation)
	if err != nil {
		return 0, err
	}
	return snap.Ledger.Generation, nil
}

func wizardWillMutate(hookAgents, notifyAgents []portable.Integration, portablePresent bool, install bool) bool {
	for _, agent := range hookAgents {
		if agent == portable.Codex {
			return true
		}
	}
	if len(notifyAgents) == 0 {
		return false
	}
	if install {
		return true
	}
	return portablePresent
}

func wizardIntentTargets(req Request, hookAgents, notifyAgents []portable.Integration) []portablesetup.IntentTarget {
	byClient := map[string]*portablesetup.IntentTarget{}
	var order []string
	add := func(agent portable.Integration, unit string) {
		id := string(agent)
		target, ok := byClient[id]
		if !ok {
			target = &portablesetup.IntentTarget{
				Client:         id,
				InstallationID: req.InstallationID,
				BindingID:      req.BindingIDs[id],
				DataReceiptID:  req.DataReceiptIDs[id],
				Profile:        clientConfig(req, agent),
			}
			byClient[id] = target
			order = append(order, id)
		}
		target.Units = append(target.Units, unit)
		if unit == "agent-notify" {
			if agent == portable.Pi {
				target.PiChannels = &piauthority.Choices{Desktop: req.PiDesktop, Webhook: req.PiWebhook, Preimage: req.PiChannelPreimage}
			}
			target.MCPConfig = discoveryConfigPath(req, agent)
			if removal, ok := req.RemovalBindings[id]; ok {
				oldBinding := removal.Old
				target.OldBinding, target.OldConsumerKey = &oldBinding, removal.OldConsumerKey
			}
			if migration, ok := req.MigrationBindings[id]; ok {
				oldBinding, newBinding := migration.Old, migration.New
				target.OldBinding, target.NewBinding = &oldBinding, &newBinding
				target.OldConsumerKey, target.NewConsumerKey = migration.OldConsumerKey, migration.NewConsumerKey
			}
		}
	}
	for _, agent := range hookAgents {
		add(agent, "hooks")
	}
	for _, agent := range notifyAgents {
		add(agent, "agent-notify")
		if agent == portable.CopilotVSCode && req.LocalConfig != nil {
			if req.LocalConfig.NativeStop {
				add(agent, "native-stop")
			}
			if len(req.LocalConfig.MCPServers) != 0 {
				add(agent, "local-mcp")
			}
			if len(req.LocalConfig.Skills) != 0 {
				add(agent, "local-skills")
			}
		}
	}
	out := make([]portablesetup.IntentTarget, 0, len(order))
	for _, id := range order {
		out = append(out, *byClient[id])
	}
	return out
}

func attachKnownReceipts(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, targets []portablesetup.IntentTarget) []portablesetup.IntentTarget {
	if len(targets) == 0 || req.InstallationID == "" {
		return targets
	}
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return targets
	}
	for i, target := range targets {
		if target.DataReceiptID != "" {
			continue
		}
		id := target.InstallationID
		if id == "" {
			id = req.InstallationID
		}
		if receipt := knownReceiptID(mat, id, target.Client); receipt != "" {
			targets[i].DataReceiptID = receipt
		}
	}
	return targets
}

func persistExternalUninstalled(ctx context.Context, req Request, runtimeRoot string, reservation *installruntime.PendingMutation) error {
	if !req.ExternalUninstalled {
		return nil
	}
	return (portablesetup.Service{ExpectedPolicy: req.BootstrapExpectedPolicy}).PatchIntentExternalUninstalled(ctx, req.ControlRoot, runtimeRoot, "", reservation)
}

func persistKnownReceipt(ctx context.Context, req Request, runtimeRoot string, mat portablesetup.Materializer, installationID, client string, reservation *installruntime.PendingMutation) error {
	receipt := knownReceiptID(mat, installationID, client)
	if receipt == "" {
		return nil
	}
	return (portablesetup.Service{ExpectedPolicy: req.BootstrapExpectedPolicy}).PatchIntentReceipt(ctx, req.ControlRoot, runtimeRoot, "", client, receipt, reservation)
}

func knownReceiptID(mat portablesetup.Materializer, installationID, clientID string) string {
	clientID = portablesetup.ClientID(portablesetup.Integration(clientID))
	if installationID == "" || clientID == "" {
		return ""
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID && binding.DataReceiptID != "" {
				return binding.DataReceiptID
			}
		}
	}
	return ""
}

func publishWizardIntent(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, portablePresent bool) (installruntime.InstalledSnapshot, *installruntime.PendingMutation, error) {
	if snap.Ledger.PendingMutation != nil {
		reservation := *snap.Ledger.PendingMutation
		return snap, &reservation, nil
	}
	if !wizardWillMutate(hookAgents, notifyAgents, portablePresent, req.Action == ActionInstall) {
		return snap, nil, nil
	}
	targets := wizardIntentTargets(req, hookAgents, notifyAgents)
	targets = attachKnownReceipts(req, snap, runtimeRoot, targets)
	if len(targets) == 0 {
		return snap, nil, nil
	}
	_, reservation, err := (portablesetup.Service{ExpectedPolicy: req.BootstrapExpectedPolicy}).PublishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{
		ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner,
		ExpectedGeneration: snap.Ledger.Generation, ExpectedPolicy: req.BootstrapExpectedPolicy, Action: string(req.Action), Stage: "confirmed",
		SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
		TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
		Primary:             req.Primary,
		GlobalConfig:        req.GlobalConfig,
		ExternalUninstalled: req.ExternalUninstalled,
		Targets:             targets,
	})
	if err != nil {
		return snap, nil, err
	}
	// Another invocation may complete this intent and publish a new one before
	// the refresh. Keep the token returned by publication and reject substitution.
	current, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return snap, reservation, err
	}
	if reservation == nil || current.Ledger.PendingMutation == nil || *current.Ledger.PendingMutation != *reservation {
		return snap, reservation, portablesetup.ErrConcurrentChange
	}
	return current, reservation, nil
}

func shouldFinishWizardIntent(out Result) bool {
	switch out.Outcome {
	case "completed", "unchanged":
		return true
	case "incomplete":
		// §7.4.1: UAP binding, locator, and runtime consumer are consistent.
		// Manual/failed activation is a next action, not an unfinished handoff.
		// Mixed-revision Repair advertises a different package/agent retry
		// after the matching sibling succeeded, so leftover intent would
		// conflict with that next action.
		return out.Reason == "activation_incomplete" || out.Reason == "exact_revision_required"
	default:
		return false
	}
}

func finishWizardIntent(ctx context.Context, req Request, runtimeRoot string, out Result, err error) (Result, error) {
	if errors.Is(err, portablesetup.ErrConcurrentChange) {
		out.Outcome, out.Reason = "conflict", "concurrent_change"
		return out, err
	}
	if !shouldFinishWizardIntent(out) {
		return out, err
	}
	snap, readErr := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if readErr != nil {
		out.Outcome, out.Reason = "incomplete", readErr.Error()
		return out, readErr
	}
	if snap.Ledger.PendingMutation == nil {
		return out, err
	}
	if out.reservation == nil || *snap.Ledger.PendingMutation != *out.reservation {
		out.Outcome, out.Reason = "conflict", "concurrent_change"
		return out, ErrRefused
	}
	cp := *out.reservation
	gen, finished, finishErr := (portablesetup.Service{ExpectedPolicy: req.BootstrapExpectedPolicy}).FinishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{
		ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner,
	}, &cp)
	if finishErr != nil {
		out.Outcome, out.Reason = "incomplete", "intent_cleanup_failed"
		return out, finishErr
	}
	if !finished {
		out.Outcome, out.Reason = "conflict", "concurrent_change"
		return out, ErrRefused
	}
	out.Generation = gen
	return out, err
}
