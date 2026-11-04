package setupwizard

import (
	"context"
	"errors"
	"fmt"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
)

func boolPtr(v bool) *bool {
	return &v
}

// BootstrapAutoTargets preserves absent MCP units when an installation or
// owned direct registration already exists. Historical state has no durable
// per-client opt-out bit, so an absent sibling requires an explicit Add.
// A read error or ambiguous state must never be interpreted as a fresh install.
func BootstrapAutoTargets(ctx context.Context, req Request, before Result) (selected, skipped []string, err error) {
	if ctx == nil {
		return nil, nil, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("MCP inspection interrupted: %w", err)
	}
	view, err := inspectUAPState(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("MCP inspection interrupted: %w", err)
	}
	if view.Recovery.Required || len(view.Installations) > 1 {
		return nil, nil, fmt.Errorf("existing portable installation requires inspection")
	}
	live := map[string]bool{}
	for _, target := range before.Targets {
		if target.Unit != "direct-mcp" && target.Unit != "agent-notify" {
			continue
		}
		if target.Outcome == "unknown" {
			return nil, nil, bootstrapMCPInspectionError(target)
		}
		if target.Outcome == "installed" {
			live[target.Client] = true
		}
	}
	if len(view.Installations) == 0 && len(live) == 0 {
		return append([]string(nil), req.Agents...), nil, nil
	}
	for _, agent := range req.Agents {
		if live[agent] {
			selected = append(selected, agent)
		} else {
			skipped = append(skipped, agent)
		}
	}
	return selected, skipped, nil
}

func liveUpdateAgents(ctx context.Context, req Request, mat portablesetup.Materializer, id portablesetup.Identity, fallback portable.Integration) []string {
	if !liveNotifyClient(mat, id.InstallationID, string(fallback)) {
		return nil
	}
	root := clientPackageRoot(req, fallback)
	if root == "" {
		root = req.PackageRoot
	}
	desired, err := retainedUpdateTreeDigest(ctx, mat, root)
	if err != nil || desired == "" {
		return []string{string(fallback)}
	}
	selected := req.Agents
	if len(selected) == 0 {
		selected = []string{string(fallback)}
	}
	var agents []string
	for _, name := range selected {
		if !liveNotifyClient(mat, id.InstallationID, name) {
			continue
		}
		got := liveBindingTreeDigest(mat, id.InstallationID, name)
		if got != "" && got != desired {
			agents = append(agents, name)
		}
	}
	if len(agents) == 0 {
		return []string{string(fallback)}
	}
	return agents
}

func unboundSelectedAgents(req Request, mat portablesetup.Materializer, id portablesetup.Identity, fallback portable.Integration) []string {
	selected := req.Agents
	if len(selected) == 0 {
		selected = []string{string(fallback)}
	}
	var unbound []string
	for _, name := range selected {
		if !liveNotifyClient(mat, id.InstallationID, name) {
			unbound = append(unbound, name)
		}
	}
	return unbound
}

func updateRequired(req Request, adding portable.Integration, others []string, liveUpdate, unbound []string, out Result, err error) (Result, error) {
	out.Targets = append(out.Targets, TargetResult{Client: string(adding), Unit: "agent-notify", Outcome: "incomplete", Reason: "update_required"})
	out.Outcome, out.Reason = "incomplete", "update_required"
	updateReq := req
	updateReq.Action = ActionUpdate
	if len(liveUpdate) > 0 {
		updateReq.Agents = append([]string(nil), liveUpdate...)
		reason := "update_existing"
		if len(unbound) > 0 {
			reason = "update_existing_before_add"
		}
		out.NextActions = []NextAction{
			{Kind: "update", Agents: updateReq.Agents, Command: localRetryCommand(updateReq), Reason: reason},
		}
		if len(unbound) > 0 {
			addReq := req
			addReq.Agents = append([]string(nil), unbound...)
			out.NextActions = append(out.NextActions, NextAction{
				Kind: "install", Agents: addReq.Agents, Command: localRetryCommand(addReq), Reason: "add_after_update",
			})
		}
		return out, err
	}
	updateAgents := others
	if len(updateAgents) == 0 {
		updateAgents = []string{string(adding)}
	}
	updateReq.Agents = updateAgents
	addReq := req
	addReq.Agents = []string{string(adding)}
	out.NextActions = []NextAction{
		{Kind: "update", Agents: updateAgents, Command: localRetryCommand(updateReq), Reason: "update_existing_before_add"},
		{Kind: "install", Agents: []string{string(adding)}, Command: localRetryCommand(addReq), Reason: "add_after_update"},
	}
	return out, err
}

func retainedUpdateTreeDigest(ctx context.Context, mat portablesetup.Materializer, packageRoot string) (string, error) {
	eng, err := installerEngine(mat)
	if err != nil {
		return "", err
	}
	return eng.LocalPackageTreeDigest(ctx, packageRoot)
}

func retainedPackageIdentityConflict(controlRoot, installationID, packageRoot string) bool {
	desired := packageDeclaredName(packageRoot)
	if desired == "" || installationID == "" {
		return false
	}
	state, err := loadUAPState(controlRoot)
	if err != nil {
		return false
	}
	for _, installation := range state.Installations {
		if installation.InstallationID == installationID && installation.DeclaredName != "" && installation.DeclaredName != desired {
			return true
		}
	}
	return false
}

func normalizeAgents(agents []string) ([]portable.Integration, error) {
	var out []portable.Integration
	seen := map[portable.Integration]bool{}
	for _, item := range agents {
		var id portable.Integration
		switch item {
		case "claude":
			id = portable.Claude
		case "codex":
			id = portable.Codex
		case "copilot-vscode":
			id = portable.CopilotVSCode
		case "pi":
			id = portable.Pi
		case "cursor":
			id = portable.Cursor
		default:
			return nil, errors.New("invalid_agents")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func selectedUnits(req Request, agents []portable.Integration) (hooks, notify []portable.Integration) {
	defaultOn := req.Action != ActionInspect
	for _, agent := range agents {
		if agent == portable.CopilotVSCode {
			if req.LocalConfig != nil && (req.LocalConfig.NativeStop || len(req.LocalConfig.MCPServers) > 0 || len(req.LocalConfig.Skills) > 0 || (req.Action == ActionUpdate || req.Action == ActionRepair || req.Action == ActionUninstall) && req.InstallationID != "" && req.BindingIDs[string(agent)] != "") {
				notify = append(notify, agent)
			}
			continue
		}
		if agent != portable.Cursor && agent != portable.CopilotVSCode && agent != portable.Pi && agentUnit(req.Hooks, perClientHooks(req, agent), defaultOn) {
			hooks = append(hooks, agent)
		}
		if agentUnit(req.AgentNotify, perClientNotify(req, agent), defaultOn) {
			notify = append(notify, agent)
		}
	}
	return hooks, notify
}

// repairRequestsMissingHooks reports a Codex hooks unit that repair would add.
// Claude hooks are never managed by this wizard. A missing notify binding is
// not_installed from requireLiveNotifyBindings, not this check.
func repairRequestsMissingHooks(req Request, hookAgents []portable.Integration) (portable.Integration, bool) {
	if req.Action != ActionRepair {
		return "", false
	}
	for _, agent := range hookAgents {
		if agent == portable.Claude {
			continue
		}
		if !hooksManaged(req, agent) {
			return agent, true
		}
	}
	return "", false
}

// preserveLiveUnits keeps omitted update/repair unit flags on the currently
// managed units. Omission must not add a unit the user left off.
func preserveLiveUnits(ctx context.Context, req Request, agents []portable.Integration) (Request, error) {
	allUnitsOmitted := unitFlagsOmitted(req)
	preserveHooks := false
	preserveNotify := false
	for _, agent := range agents {
		if (agent == portable.Claude || agent == portable.Codex) && req.Hooks == nil && perClientHooks(req, agent) == nil {
			preserveHooks = true
		}
		if agent != portable.CopilotVSCode && req.AgentNotify == nil && perClientNotify(req, agent) == nil {
			preserveNotify = true
		}
	}
	if !preserveHooks && !preserveNotify {
		return req, nil
	}
	notifyLive := map[string]bool{}
	if preserveNotify {
		view, err := inspectUAPState(ctx, req)
		if err != nil {
			return req, err
		}
		for _, installation := range view.Installations {
			if req.InstallationID != "" && installation.InstallationID != req.InstallationID {
				continue
			}
			for _, binding := range installation.Bindings {
				if binding.ClientID != "" {
					notifyLive[binding.ClientID] = true
				}
			}
		}
	}
	for _, agent := range agents {
		if req.Hooks == nil && perClientHooks(req, agent) == nil {
			switch agent {
			case portable.Claude:
				req.ClaudeHooks = boolPtr(hooksManaged(req, agent))
			case portable.Codex:
				req.CodexHooks = boolPtr(hooksManaged(req, agent))
			}
		}
		if req.AgentNotify != nil || perClientNotify(req, agent) != nil {
			continue
		}
		hasHooks := (agent == portable.Claude || agent == portable.Codex) &&
			agentUnit(req.Hooks, perClientHooks(req, agent), false)
		if !allUnitsOmitted && !notifyLive[portablesetup.ClientID(agent)] && !hasHooks {
			// A notification-only request still needs the existing missing-binding
			// or retained-source preflight. Absence alone is not an explicit opt-out.
			continue
		}
		notify := boolPtr(notifyLive[portablesetup.ClientID(agent)])
		switch agent {
		case portable.Claude:
			req.ClaudeAgentNotify = notify
		case portable.Codex:
			req.CodexAgentNotify = notify
		case portable.Pi:
			req.PiAgentNotify = notify
		case portable.Cursor:
			req.CursorAgentNotify = notify
		}
	}
	return req, nil
}

func perClientHooks(req Request, agent portable.Integration) *bool {
	switch agent {
	case portable.Claude:
		return req.ClaudeHooks
	case portable.Codex:
		return req.CodexHooks
	default:
		return nil
	}
}

func perClientNotify(req Request, agent portable.Integration) *bool {
	switch agent {
	case portable.Claude:
		return req.ClaudeAgentNotify
	case portable.Codex:
		return req.CodexAgentNotify
	case portable.Pi:
		return req.PiAgentNotify
	case portable.Cursor:
		return req.CursorAgentNotify
	default:
		return nil
	}
}

func agentUnit(global, perClient *bool, defaultOn bool) bool {
	if perClient != nil {
		return *perClient
	}
	return unitOn(global, defaultOn)
}

func unitOn(flag *bool, defaultOn bool) bool {
	if flag == nil {
		return defaultOn
	}
	return *flag
}
