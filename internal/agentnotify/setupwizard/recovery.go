package setupwizard

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

// annotatePendingRecovery reports kernel and UAP recovery without recovering.
// Plan skips source-dependent Prepare while a journal is pending (§7.4.2, §9.2).
func annotatePendingRecovery(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, text string, out *Result) (string, bool) {
	if out == nil {
		return text, false
	}
	pending := false
	if snap.Recovery {
		text += " recovery-pending=kernel-journal"
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: "kernel-journal"})
		pending = true
	}
	view, err := inspectUAPState(ctx, req)
	if err != nil || !view.Recovery.Required {
		return text, pending
	}
	ids := recoveryIDs(view)
	reason := strings.Join(ids, ",")
	if reason == "" {
		reason = view.Recovery.Reason
	}
	if len(ids) > 0 {
		text += " recovery-pending=" + strings.Join(ids, ",")
	} else if view.Recovery.Reason != "" {
		text += " recovery-pending=untrusted"
	}
	out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: reason})
	return text, true
}

func recoverWizardJournals(ctx context.Context, mat portablesetup.Materializer, id portablesetup.Identity, req Request, agents []portable.Integration) error {
	if len(agents) == 0 {
		return nil
	}
	agent := agents[0]
	return mat.RecoverJournals(ctx, portablesetup.MaterializeRequest{
		LocalConfig:      req.LocalConfig,
		Identity:         id,
		Integration:      agent,
		PackageRoot:      req.PackageRoot,
		ClientConfigRoot: clientConfig(req, agent),
		ClientExecutable: clientExecutable(req, agent),
		HelperExecutable: req.Helper,
	})
}

func recoveryIDs(view uapinstaller.Inspection) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, journal := range view.Recovery.Journals {
		add(journal.OperationID)
	}
	for _, receipt := range view.Recovery.Receipts {
		add(receipt.OperationID)
	}
	return ids
}

func resumeFromPendingIntent(req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, out Result) (Request, []portable.Integration, Result, bool, error) {
	pending := snap.Ledger.PendingMutation
	if pending == nil || pending.Owner != "existing-installer" {
		return req, agents, out, false, nil
	}
	intent, err := portablesetup.ReadIntent(req.ControlRoot)
	if err != nil || intent.SetupIntentID != pending.ID {
		out.Outcome, out.Reason = "incomplete", "pending_intent_unreadable"
		if err == nil {
			err = ErrRefused
		}
		return req, agents, out, false, err
	}
	if intent.Action != string(req.Action) {
		conflict, _ := pendingIntentConflict(req, portablesetup.ErrIntentConflict, out)
		return req, agents, conflict, false, ErrRefused
	}
	req, agents, err = restoreOmittedFromIntent(req, agents, intent)
	if err != nil {
		if errors.Is(err, portablesetup.ErrIntentConflict) {
			conflict, _ := pendingIntentConflict(req, err, out)
			return req, agents, conflict, false, ErrRefused
		}
		out.Outcome, out.Reason = "invalid", err.Error()
		return req, agents, out, false, ErrRefused
	}
	return req, agents, out, len(agents) > 0, nil
}

func restoreOmittedFromIntent(req Request, agents []portable.Integration, intent portablesetup.Intent) (Request, []portable.Integration, error) {
	localSelectionOmitted := req.LocalConfig == nil

	intentAgents := intentClients(intent)
	if len(agents) == 0 {
		if len(intentAgents) == 0 {
			return req, agents, nil
		}
		req.Agents = intentAgents
		next, err := normalizeAgents(intentAgents)
		if err != nil {
			return req, agents, err
		}
		agents = next
	} else if len(intentAgents) > 0 {
		names := make([]string, len(agents))
		for i, agent := range agents {
			names[i] = string(agent)
		}
		if !sameStringSet(names, intentAgents) {
			return req, agents, portablesetup.ErrIntentConflict
		}
	}
	if req.PackageSHA256 == "" {
		req.PackageSHA256 = intent.SourceDigest
	} else if intent.SourceDigest != "" && req.PackageSHA256 != intent.SourceDigest {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.TreeDigest == "" {
		req.TreeDigest = intent.TreeDigest
	} else if intent.TreeDigest != "" && req.TreeDigest != intent.TreeDigest {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.HelperDigest == "" {
		req.HelperDigest = intent.HelperDigest
	} else if intent.HelperDigest != "" && req.HelperDigest != intent.HelperDigest {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.HelperVersion == "" {
		req.HelperVersion = intent.HelperVersion
	} else if intent.HelperVersion != "" && req.HelperVersion != intent.HelperVersion {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = intent.SourceRevision
	} else if intent.SourceRevision != "" && req.ReleaseVersion != intent.SourceRevision {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if intent.GlobalConfig != "" {
		if req.GlobalConfig == "" {
			req.GlobalConfig = intent.GlobalConfig
		} else {
			physical, err := installruntime.PhysicalPath(req.GlobalConfig)
			if err != nil || physical != intent.GlobalConfig {
				return req, agents, portablesetup.ErrIntentConflict
			}
			req.GlobalConfig = physical
		}
	}
	primary := intent.Primary
	if primary == "" {
		// Intents written before primary was persisted used the old default.
		// Keep that locator identity across an interrupted remove/retry.
		for _, target := range intent.Targets {
			for _, unit := range target.Units {
				if unit == "agent-notify" {
					primary = "primary"
				}
			}
		}
	}
	if req.Primary == "" {
		req.Primary = primary
	} else if primary != "" && req.Primary != primary {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if intent.ExternalUninstalled {
		req.ExternalUninstalled = true
	}
	for _, target := range intent.Targets {
		if req.Action == ActionUninstall && (target.OldBinding != nil || target.OldConsumerKey != "") {
			if target.OldBinding == nil || target.NewBinding != nil || target.NewConsumerKey != "" || target.DataReceiptID == "" {
				return req, agents, portablesetup.ErrIntentConflict
			}
			removal := RemovalBinding{Old: *target.OldBinding, OldConsumerKey: target.OldConsumerKey}
			if removal.validate(target.Client, target.InstallationID, target.BindingID) != nil {
				return req, agents, portablesetup.ErrIntentConflict
			}
			if req.RemovalBindings == nil {
				req.RemovalBindings = map[string]RemovalBinding{}
			}
			if existing, ok := req.RemovalBindings[target.Client]; ok && existing != removal {
				return req, agents, portablesetup.ErrIntentConflict
			}
			req.RemovalBindings[target.Client] = removal
		} else if target.OldBinding != nil || target.NewBinding != nil || target.OldConsumerKey != "" || target.NewConsumerKey != "" {
			if target.OldBinding == nil || target.NewBinding == nil {
				return req, agents, portablesetup.ErrIntentConflict
			}
			migration := MigrationBinding{Old: *target.OldBinding, New: *target.NewBinding, OldConsumerKey: target.OldConsumerKey, NewConsumerKey: target.NewConsumerKey}
			if migration.validate() != nil || string(migration.Old.Integration) != target.Client || migration.Old.InstallationID != target.InstallationID || migration.Old.BindingID != target.BindingID {
				return req, agents, portablesetup.ErrIntentConflict
			}
			if req.MigrationBindings == nil {
				req.MigrationBindings = map[string]MigrationBinding{}
			}
			if existing, ok := req.MigrationBindings[target.Client]; ok && existing != migration {
				return req, agents, portablesetup.ErrIntentConflict
			}
			req.MigrationBindings[target.Client] = migration
		}
		if target.InstallationID != "" {
			if req.InstallationID == "" {
				req.InstallationID = target.InstallationID
			} else if req.InstallationID != target.InstallationID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.BindingID != "" {
			if req.BindingIDs == nil {
				req.BindingIDs = map[string]string{}
			}
			if existing := req.BindingIDs[target.Client]; existing == "" {
				req.BindingIDs[target.Client] = target.BindingID
			} else if existing != target.BindingID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.DataReceiptID != "" {
			if req.DataReceiptIDs == nil {
				req.DataReceiptIDs = map[string]string{}
			}
			if existing := req.DataReceiptIDs[target.Client]; existing == "" {
				req.DataReceiptIDs[target.Client] = target.DataReceiptID
			} else if existing != target.DataReceiptID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.MCPConfig != "" {
			if req.MCPConfig == nil {
				req.MCPConfig = map[string]string{}
			}
			if existing := req.MCPConfig[target.Client]; existing == "" {
				req.MCPConfig[target.Client] = target.MCPConfig
			} else if existing != target.MCPConfig {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.Profile == "" {
			continue
		}
		switch target.Client {
		case "copilot-vscode":
			settings := filepath.Join(target.Profile, "settings.json")
			if req.LocalConfig == nil {
				req.LocalConfig = &vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: vscode.QualifiedDarwinTESTTuple()}
			} else if req.LocalConfig.ProfileSettingsPath != settings {
				return req, agents, portablesetup.ErrIntentConflict
			}
		case "codex":
			if req.CodexHome == "" {
				req.CodexHome = target.Profile
			} else if req.CodexHome != target.Profile {
				return req, agents, portablesetup.ErrIntentConflict
			}
		case "cursor":
			if req.CursorConfig == "" {
				req.CursorConfig = target.Profile
			} else if req.CursorConfig != target.Profile {
				return req, agents, portablesetup.ErrIntentConflict
			}
		case "pi":
			if target.PiChannels == nil {
				return req, agents, portablesetup.ErrIntentConflict
			}
			frozen := target.PiChannels
			if req.PiDesktop != nil && (frozen.Desktop == nil || *req.PiDesktop != *frozen.Desktop) || req.PiWebhook != nil && (frozen.Webhook == nil || *req.PiWebhook != *frozen.Webhook) || req.PiChannelPreimage != "" && req.PiChannelPreimage != frozen.Preimage {
				return req, agents, portablesetup.ErrIntentConflict
			}
			req.PiDesktop, req.PiWebhook, req.PiChannelPreimage = frozen.Desktop, frozen.Webhook, frozen.Preimage
			if req.PiAgentDir == "" {
				req.PiAgentDir = target.Profile
			} else if req.PiAgentDir != target.Profile {
				return req, agents, portablesetup.ErrIntentConflict
			}
		case "claude":
			if req.ClaudeConfig == "" {
				req.ClaudeConfig = target.Profile
			} else if req.ClaudeConfig != target.Profile {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
	}
	if req.LocalConfig != nil && req.LocalConfig.TargetShell.Shell == "" {
		req.LocalConfig.TargetShell.Shell = vscodelocalhooks.Shell(req.LocalConfig.QualifiedTuple.TargetShell)
	}
	if localSelectionOmitted && req.LocalConfig != nil {
		if units, specified := intentClientUnits(intent); specified {
			req = applyIntentUnits(req, units)
		}
	}
	req, err := restoreUnitsFromIntent(req, agents, intent)
	if err != nil {
		return req, agents, err
	}
	return req, agents, nil
}

func restoreUnitsFromIntent(req Request, agents []portable.Integration, intent portablesetup.Intent) (Request, error) {
	want, specified := intentClientUnits(intent)
	if !specified {
		return req, nil
	}
	if unitFlagsOmitted(req) {
		return applyIntentUnits(req, want), nil
	}
	got := requestClientUnits(req, agents)
	if !sameClientUnits(want, got) {
		return req, portablesetup.ErrIntentConflict
	}
	return req, nil
}

type unitSelection struct {
	hooks, notify                  bool
	local, nativeStop, mcp, skills bool
}

func intentClientUnits(intent portablesetup.Intent) (map[string]unitSelection, bool) {
	out := map[string]unitSelection{}
	specified := false
	for _, target := range intent.Targets {
		if target.Client == "" {
			continue
		}
		if len(target.Units) > 0 {
			specified = true
		}
		sel := out[target.Client]
		sel.local = target.Client == string(portable.CopilotVSCode)
		for _, unit := range target.Units {
			switch unit {
			case "native-stop":
				sel.nativeStop = true
			case "local-mcp":
				sel.mcp = true
			case "local-skills":
				sel.skills = true
			case "hooks":
				sel.hooks = true
			case "direct-mcp", "agent-notify", "mcp", "skills":
				sel.notify = true
			}
		}
		out[target.Client] = sel
	}
	return out, specified
}

func requestClientUnits(req Request, agents []portable.Integration) map[string]unitSelection {
	out := map[string]unitSelection{}
	for _, agent := range agents {
		sel := unitSelection{}
		if agent == portable.CopilotVSCode && req.LocalConfig != nil {
			sel.local = true
			sel.nativeStop = req.LocalConfig.NativeStop
			sel.mcp = len(req.LocalConfig.MCPServers) > 0
			sel.skills = len(req.LocalConfig.Skills) > 0
		}
		out[string(agent)] = sel
	}
	hooks, notify := selectedUnits(req, agents)
	for _, agent := range hooks {
		sel := out[string(agent)]
		sel.hooks = true
		out[string(agent)] = sel
	}
	for _, agent := range notify {
		sel := out[string(agent)]
		sel.notify = true
		out[string(agent)] = sel
	}
	return out
}

func sameClientUnits(want, got map[string]unitSelection) bool {
	if len(want) != len(got) {
		return false
	}
	for client, sel := range want {
		other, ok := got[client]
		if !ok || other != sel {
			return false
		}
	}
	return true
}

func unitsUniform(want map[string]unitSelection) (unitSelection, bool) {
	var first unitSelection
	seen := false
	for _, sel := range want {
		if !seen {
			first = sel
			seen = true
			continue
		}
		if sel != first {
			return unitSelection{}, false
		}
	}
	return first, seen
}

func applyIntentUnits(req Request, want map[string]unitSelection) Request {
	if req.LocalConfig != nil {
		cp := *req.LocalConfig
		req.LocalConfig = &cp
	}
	if uniform, ok := unitsUniform(want); ok && !uniform.local {
		req.Hooks = boolPtr(uniform.hooks)
		req.AgentNotify = boolPtr(uniform.notify)
		req.ClaudeHooks, req.CodexHooks = nil, nil
		req.ClaudeAgentNotify, req.CodexAgentNotify, req.CursorAgentNotify, req.PiAgentNotify = nil, nil, nil, nil
		return req
	}
	req.Hooks, req.AgentNotify = nil, nil
	req.ClaudeHooks, req.CodexHooks = nil, nil
	req.ClaudeAgentNotify, req.CodexAgentNotify, req.CursorAgentNotify, req.PiAgentNotify = nil, nil, nil, nil
	for client, sel := range want {
		hooks, notify := boolPtr(sel.hooks), boolPtr(sel.notify)
		switch client {
		case "claude":
			req.ClaudeHooks, req.ClaudeAgentNotify = hooks, notify
		case "codex":
			req.CodexHooks, req.CodexAgentNotify = hooks, notify
		case "copilot-vscode":
			if req.LocalConfig == nil {
				req.LocalConfig = &vscode.LocalConfig{QualifiedTuple: vscode.QualifiedDarwinTESTTuple()}
			}
			req.LocalConfig.TargetShell.Shell = vscodelocalhooks.Shell(req.LocalConfig.QualifiedTuple.TargetShell)
			req.LocalConfig.NativeStop = sel.nativeStop
			req.LocalConfig.MCPServers, req.LocalConfig.Skills = nil, nil
			if sel.mcp {
				req.LocalConfig.MCPServers = []string{"agent-notifications"}
			}
			if sel.skills {
				req.LocalConfig.Skills = []string{"agent-notifications"}
			}
		case "pi":
			req.PiAgentNotify = notify
		case "cursor":
			req.CursorAgentNotify = notify
		}
	}
	return req
}

func intentClients(intent portablesetup.Intent) []string {
	var out []string
	seen := map[string]bool{}
	for _, target := range intent.Targets {
		if target.Client == "" || seen[target.Client] {
			continue
		}
		seen[target.Client] = true
		out = append(out, target.Client)
	}
	return out
}

func unitFlagsOmitted(req Request) bool {
	return req.Hooks == nil && req.AgentNotify == nil && req.ClaudeHooks == nil && req.CodexHooks == nil && req.ClaudeAgentNotify == nil && req.CodexAgentNotify == nil && req.CursorAgentNotify == nil && req.PiAgentNotify == nil && req.LocalConfig == nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, item := range a {
		seen[item]++
	}
	for _, item := range b {
		if seen[item] == 0 {
			return false
		}
		seen[item]--
	}
	return true
}

func pendingIntentConflict(req Request, err error, out Result) (Result, bool) {
	if !errors.Is(err, portablesetup.ErrIntentConflict) {
		return out, false
	}
	out.Outcome, out.Reason = "conflict", "pending_intent_conflict"
	intent, readErr := portablesetup.ReadIntent(req.ControlRoot)
	if readErr != nil {
		return out, true
	}
	out.Command = localRetryCommand(retryRequestFromIntent(req, intent))
	return out, true
}

func retryRequestFromIntent(req Request, intent portablesetup.Intent) Request {
	if req.LocalConfig != nil {
		cp := *req.LocalConfig
		req.LocalConfig = &cp
	}
	retry := req
	retry.Action = Action(intent.Action)
	if clients := intentClients(intent); len(clients) > 0 {
		retry.Agents = clients
	}
	if intent.SourceDigest != "" {
		retry.PackageSHA256 = intent.SourceDigest
	}
	if intent.TreeDigest != "" {
		retry.TreeDigest = intent.TreeDigest
	}
	if intent.HelperDigest != "" {
		retry.HelperDigest = intent.HelperDigest
	}
	if intent.HelperVersion != "" {
		retry.HelperVersion = intent.HelperVersion
	}
	if intent.SourceRevision != "" {
		retry.ReleaseVersion = intent.SourceRevision
	}
	if intent.GlobalConfig != "" {
		retry.GlobalConfig = intent.GlobalConfig
	}
	if intent.ExternalUninstalled {
		retry.ExternalUninstalled = true
	}
	if want, specified := intentClientUnits(intent); specified {
		retry = applyIntentUnits(retry, want)
	}
	for _, target := range intent.Targets {
		if target.InstallationID != "" {
			retry.InstallationID = target.InstallationID
		}
		if target.BindingID != "" {
			if retry.BindingIDs == nil {
				retry.BindingIDs = map[string]string{}
			}
			retry.BindingIDs[target.Client] = target.BindingID
		}
		if target.DataReceiptID != "" {
			if retry.DataReceiptIDs == nil {
				retry.DataReceiptIDs = map[string]string{}
			}
			retry.DataReceiptIDs[target.Client] = target.DataReceiptID
		}
		switch target.Client {
		case "copilot-vscode":
			if retry.LocalConfig == nil {
				retry.LocalConfig = &vscode.LocalConfig{QualifiedTuple: vscode.QualifiedDarwinTESTTuple()}
			}
			retry.LocalConfig.ProfileSettingsPath = filepath.Join(target.Profile, "settings.json")
			retry.ScopeRoot = target.Profile
		case "codex":
			if target.Profile != "" {
				retry.CodexHome = target.Profile
			}
		case "cursor":
			if target.Profile != "" {
				retry.CursorConfig = target.Profile
				retry.ScopeRoot = target.Profile
			}
		case "pi":
			if target.Profile != "" {
				retry.PiAgentDir = target.Profile
			}
		case "claude":
			if target.Profile != "" {
				retry.ClaudeConfig = target.Profile
			}
		}
	}
	retry.Yes = true
	return retry
}
