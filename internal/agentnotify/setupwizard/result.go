package setupwizard

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
)

func installerNextActions(actions []uapinstaller.NextAction) []NextAction {
	out := make([]NextAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, NextAction{Kind: action.Kind, Reason: action.Reason})
	}
	return out
}

func attachReadiness(req Request, agents []portable.Integration, out Result, mutationInstall bool) Result {
	if out.Outcome == "invalid" || out.Outcome == "cancelled" {
		return out
	}
	runtime := "installed"
	if out.Reason == "managed_runtime_required" {
		runtime = "absent"
	}
	restartPending := false
	for _, agent := range agents {
		// Inspect has no per-client MCP receipt authority: it cannot turn a
		// delivered notification into a setup verification, or claim failure.
		delivery := "not_verified"
		if req.Action == ActionInspect {
			delivery = "not_tracked"
		}
		fact := ReadinessFact{
			Client:     string(agent),
			Runtime:    runtime,
			Hooks:      "not_checked",
			MCP:        "not_checked",
			Permission: "not_checked",
			Restart:    "not_required",
			Delivery:   delivery,
		}
		for _, target := range out.Targets {
			if target.Client != string(agent) {
				continue
			}
			switch target.Unit {
			case "hooks":
				fact.Hooks = targetReadiness(req.Action, target)
			case "agent-notify":
				fact.MCP = targetReadiness(req.Action, target)
			}
		}
		if mutationInstall && fact.MCP == "installed" && out.Outcome == "completed" {
			fact.Restart = "pending"
			restartPending = true
		}
		out.Readiness = append(out.Readiness, fact)
	}
	if out.Outcome == "completed" || out.Outcome == "unchanged" {
		out = offerPostSetupActions(req, agents, out, restartPending)
	}
	return out
}

func targetReadiness(action Action, target TargetResult) string {
	if action == ActionUninstall && (target.Outcome == "completed" || target.Outcome == "unchanged" && target.Reason == "already_absent") {
		return "absent"
	}
	if target.Outcome == "completed" {
		return "installed"
	}
	return target.Outcome
}

func offerPostSetupActions(req Request, agents []portable.Integration, out Result, restartPending bool) Result {
	if req.Action == ActionUninstall {
		return out
	}
	notifyReady := make(map[string]bool)
	for _, target := range out.Targets {
		if target.Unit == "agent-notify" && (target.Outcome == "completed" || target.Outcome == "installed") {
			notifyReady[target.Client] = true
		}
	}
	names := make([]string, len(agents))
	notifyNames := make([]string, 0, len(agents))
	for i, agent := range agents {
		names[i] = string(agent)
		if notifyReady[names[i]] {
			notifyNames = append(notifyNames, names[i])
		}
	}
	has := func(kind string) bool {
		for _, next := range out.NextActions {
			if next.Kind == kind {
				return true
			}
		}
		return false
	}
	if has("recover") || has("external-uninstall") || has("resume") || has("activate") {
		return out
	}
	if restartPending && !has("restart-client") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "restart-client", Agents: names, Reason: "pending_client_restart",
		})
	}
	if len(notifyNames) == 0 {
		return out
	}
	if req.Action != ActionInspect && explicitAbs(req.ControlRoot) && out.Generation != 0 && !has("request-permission") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind:   "request-permission",
			Agents: notifyNames,
			Command: []string{
				"setup-notifications", "request-permission",
				"--control-root", req.ControlRoot,
				"--expected-generation", fmt.Sprintf("%d", out.Generation),
			},
			Reason: "permission_explicit_phase",
		})
	}
	// Read-only inspection cannot tell if a separate chat already delivered.
	if req.Action == ActionInspect {
		return out
	}
	if !has("test-notification") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "test-notification", Agents: notifyNames, Command: []string{"notify"},
			Reason: "delivery_not_verified",
		})
	}
	return out
}

func reportProgress(req Request, phase string) {
	if req.Progress != nil {
		req.Progress(phase)
	}
}

// Keep Local's exact selection on all Plan/Run fallback commands as well as
// target-specific next actions. Legacy retry construction remains unchanged.
func attachWizardCommand(req Request, out Result) Result {
	authoritative := len(out.Command) != 0
	out = attachCommand(req, out)
	if !authoritative && req.LocalConfig != nil && len(out.Command) != 0 {
		if req.InstallationID == "" {
			req.InstallationID = out.InstallationID
		}
		out.Command = localRetryCommand(req)
	}
	return out
}

// localRetryCommand extends the existing retry argv only for the explicit Local recipe.
func localRetryCommand(req Request) []string {
	if req.LocalConfig == nil {
		return RetryCommand(req)
	}
	retry := req
	retry.ClientExecutable = clientExecutable(req, portable.CopilotVSCode)
	retry.ScopeRoot = filepath.Dir(req.LocalConfig.ProfileSettingsPath)
	command := RetryCommand(retry)
	command = append(command, "--local-settings", req.LocalConfig.ProfileSettingsPath,
		"--local-native-stop", boolFlag(req.LocalConfig.NativeStop),
		"--local-mcp", boolFlag(len(req.LocalConfig.MCPServers) > 0),
		"--local-skills", boolFlag(len(req.LocalConfig.Skills) > 0))
	for _, selection := range []struct {
		flag  string
		names []string
	}{{"--local-mcp-names", req.LocalConfig.MCPServers}, {"--local-skill-names", req.LocalConfig.Skills}} {
		if len(selection.names) > 0 {
			// String slices have no unsupported JSON values. Keep exact names;
			// a boolean alone would reselect the legacy CLI defaults.
			names, _ := json.Marshal(selection.names)
			command = append(command, selection.flag, string(names))
		}
	}
	return command
}
