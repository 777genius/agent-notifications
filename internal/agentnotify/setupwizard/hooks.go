package setupwizard

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/codexsetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const claudeHooksReason = "claude_hooks_remain_on_plugin_install"

func inspectHooks(req Request, agent portable.Integration, out Result) Result {
	home := clientConfig(req, agent)
	if !explicitAbs(home) {
		return out
	}
	if agent == portable.Claude {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "hooks", Outcome: "unchanged", Reason: claudeHooksReason})
		return out
	}
	path := filepath.Join(home, "hooks.json")
	if codexsetup.HasManagedHooks(home) {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "hooks", Outcome: "installed", Reason: path})
		return out
	}
	out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "hooks", Outcome: "absent"})
	return out
}

func hooksManaged(req Request, agent portable.Integration) bool {
	if agent == portable.Claude {
		return false
	}
	home := clientConfig(req, agent)
	if !explicitAbs(home) {
		return false
	}
	return codexsetup.HasManagedHooks(home)
}

// LiveClientUnits reports currently managed hooks/notify for each selected
// client. A missing binding is off, not a collapsed global default.
func LiveClientUnits(req Request, agents []string) []ClientUnits {
	notify := map[string]bool{}
	for _, id := range LiveNotifyClients(req.ControlRoot, agents) {
		notify[id] = true
	}
	out := make([]ClientUnits, 0, len(agents))
	for _, agent := range agents {
		if agent == "" {
			continue
		}
		out = append(out, ClientUnits{
			Client: agent,
			Hooks:  hooksManaged(req, portable.Integration(agent)),
			Notify: notify[agent],
		})
	}
	return out
}

// LiveSetupClients returns selected agents that already have a portable
// binding or managed Codex hooks. Missing state is "none".
func LiveSetupClients(req Request, agents []string) []string {
	found := LiveNotifyClients(req.ControlRoot, agents)
	seen := map[string]bool{}
	for _, id := range found {
		seen[id] = true
	}
	for _, agent := range agents {
		if agent == "" || seen[agent] {
			continue
		}
		if hooksManaged(req, portable.Integration(agent)) {
			found = append(found, agent)
		}
	}
	return found
}

func applyHooks(ctx context.Context, req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, remove bool, out Result) (Result, error) {
	var reservation *installruntime.PendingMutation
	if snap.Ledger.PendingMutation != nil {
		cp := *snap.Ledger.PendingMutation
		reservation = &cp
	}
	for _, agent := range agents {
		switch agent {
		case portable.Claude:
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "hooks", Outcome: "unchanged", Reason: claudeHooksReason})
		case portable.Codex:
			target, err := applyCodexHooks(ctx, req, reservation, remove)
			out.Targets = append(out.Targets, target)
			if err != nil {
				out.Outcome, out.Reason = "incomplete", target.Reason
				return out, err
			}
		}
	}
	return out, nil
}

func applyCodexHooks(ctx context.Context, req Request, reservation *installruntime.PendingMutation, remove bool) (TargetResult, error) {
	target := TargetResult{Client: "codex", Unit: "hooks"}
	pluginRoot := req.PluginRoot
	if pluginRoot == "" {
		pluginRoot = req.RuntimeRoot
	}
	if !explicitAbs(pluginRoot) {
		target.Outcome, target.Reason = "incomplete", "plugin_root_required"
		return target, ErrRefused
	}
	if !remove && !codexBundle(pluginRoot) {
		target.Outcome, target.Reason = "incomplete", "plugin_root_required"
		return target, ErrRefused
	}
	if !explicitAbs(req.CodexHome) {
		target.Outcome, target.Reason = "incomplete", "client_config_required"
		return target, ErrRefused
	}
	result, err := codexsetup.Run(codexsetup.Options{
		Context: ctx, ControlRoot: req.ControlRoot, CodexHome: req.CodexHome,
		PluginRoot: pluginRoot, Remove: remove, Reservation: reservation,
	})
	if err != nil {
		reason := "hooks_failed"
		var init *codexsetup.InitializationError
		if errors.As(err, &init) {
			reason = "hooks_config_init"
		}
		target.Outcome, target.Reason = "incomplete", reason
		return target, err
	}
	target.Outcome, target.Reason = "completed", result.HooksPath
	return target, nil
}

func codexBundle(root string) bool {
	_, sh := os.Stat(filepath.Join(root, "bin", "codex-hook-wrapper.sh"))
	_, cmd := os.Stat(filepath.Join(root, "bin", "codex-hook-wrapper.cmd"))
	return sh == nil || cmd == nil
}
