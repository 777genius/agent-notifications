package setupwizard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

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
	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), "codex-hook-wrapper") {
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
	data, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	return err == nil && strings.Contains(string(data), "codex-hook-wrapper")
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
