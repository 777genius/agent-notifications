package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

// The bootstrap flag is intentionally outside the public wizard Request. It
// changes orchestration, not the meaning of any explicit wizard action.
func stripInstallOrUpdate(args []string) ([]string, bool, bool, error) {
	filtered := make([]string, 0, len(args))
	found, preserve := false, false
	for _, arg := range args {
		if arg != "--install-or-update" && arg != "--preserve-existing-units" {
			filtered = append(filtered, arg)
			continue
		}
		if arg == "--preserve-existing-units" {
			if preserve {
				return filtered, false, false, errors.New("invalid_arguments")
			}
			preserve = true
			continue
		}
		if found {
			return filtered, false, false, errors.New("invalid_arguments")
		}
		found = true
	}
	return filtered, found, preserve, nil
}

func validInstallOrUpdateRequest(req setupwizard.Request) bool {
	if req.Action != setupwizard.ActionInstall || !req.Yes || req.Hooks == nil || *req.Hooks ||
		req.AgentNotify == nil || !*req.AgentNotify || req.ExternalUninstalled ||
		req.ClaudeHooks != nil || req.CodexHooks != nil ||
		req.ClaudeAgentNotify != nil || req.CodexAgentNotify != nil ||
		len(req.Agents) == 0 || len(req.Agents) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, agent := range req.Agents {
		if (agent != "claude" && agent != "codex" && agent != "pi" && ((agent != "cursor" && agent != "copilot-vscode") || len(req.Agents) != 1)) || seen[agent] {
			return false
		}
		seen[agent] = true
	}
	return true
}

func writeSetupWizardPromptError(out io.Writer, jsonOut bool, req setupwizard.Request, e error) int {
	reason, outcome := "prompt_canceled", "cancelled"
	if errors.Is(e, setupwizard.ErrPromptInputClosed) {
		reason = "prompt_closed"
	} else if errors.Is(e, setupwizard.ErrPromptUnavailable) {
		reason, outcome = "prompt_unavailable", "invalid"
	} else if !errors.Is(e, setupwizard.ErrPromptCanceled) {
		reason, outcome = "prompt_failed", "invalid"
	} else if strings.Contains(e.Error(), "invalid_choice") {
		reason, outcome = "invalid_choice", "invalid"
	}
	result := setupwizard.Result{Action: string(req.Action), Outcome: outcome, Reason: reason}
	return writeSetupWizardResult(out, jsonOut, result, e)
}

func writeSetupWizardResult(out io.Writer, jsonOut bool, result setupwizard.Result, err error) int {
	if jsonOut {
		if e := json.NewEncoder(out).Encode(result); e != nil {
			return 1
		}
	} else {
		writeSetupWizardSummary(out, result)
	}

	if result.ExitCode() != 0 {
		return result.ExitCode()
	}
	if result.Action == string(setupwizard.ActionInspect) {
		if err != nil {
			return 1
		}
		return 0
	}
	if err != nil && result.Outcome != "completed" && result.Outcome != "unchanged" && result.Outcome != "cancelled" && result.Outcome != "ready" {
		return 1
	}
	return 0
}

func wizardPrintableCommand(command []string) []string {
	if len(command) == 0 || command[0] != "setup-notifications" {
		return append([]string(nil), command...)
	}
	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		executable = "claude-notifications"
	}
	return append([]string{executable}, command...)
}

func parseSetupWizard(args []string) (setupwizard.Request, bool, error) {
	var req setupwizard.Request
	if len(args) > 64 {
		return req, false, errors.New("invalid_arguments")
	}
	total := 0
	for _, arg := range args {
		total += len(arg)
		if len(arg) > 4096 || !utf8.ValidString(arg) || strings.IndexFunc(arg, unicode.IsControl) >= 0 {
			return req, false, errors.New("invalid_arguments")
		}
	}
	if total > 16384 {
		return req, false, errors.New("invalid_arguments")
	}
	values := map[string]string{}
	jsonOut := false
	allowed := map[string]bool{
		"action": true, "agents": true, "hooks": true, "agent-notify": true,
		"local-settings": true, "local-native-stop": true, "local-mcp": true, "local-skills": true,
		"local-mcp-names": true, "local-skill-names": true,
		"claude-hooks": true, "codex-hooks": true, "claude-agent-notify": true, "codex-agent-notify": true,
		"package": true, "plugin-root": true, "control-root": true, "runtime-root": true, "global-config": true,
		"codex-home": true, "claude-config": true, "client-executable": true, "helper": true,
		"scope-root": true, "installation-id": true, "mcp-config": true, "claude-mcp-config": true,
		"claude-executable": true, "codex-executable": true, "pi-executable": true, "pi-agent-dir": true, "pi-desktop": true, "pi-webhook": true, "pi-channel-preimage": true,
	}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--yes" {
			if req.Yes {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			req.Yes = true
			continue
		}
		if token == "--external-uninstalled" {
			if req.ExternalUninstalled {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			req.ExternalUninstalled = true
			continue
		}
		if token == "--json" {
			if jsonOut {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			jsonOut = true
			continue
		}
		if !strings.HasPrefix(token, "--") {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		key, value, inline := strings.Cut(token[2:], "=")
		if !allowed[key] {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		if _, ok := values[key]; ok {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		if !inline {
			i++
			if i >= len(args) {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			value = args[i]
		}
		if value == "" || strings.HasPrefix(value, "--") {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		values[key] = value
	}
	for _, key := range []string{"package", "plugin-root", "control-root", "runtime-root", "global-config", "codex-home", "claude-config", "client-executable", "helper", "scope-root", "mcp-config", "claude-mcp-config", "claude-executable", "codex-executable", "pi-executable", "pi-agent-dir", "local-settings"} {
		if values[key] == "" {
			continue
		}
		normalized, err := normalizeSetupPhysicalPath(values[key])
		if err != nil {
			return req, jsonOut, err
		}
		values[key] = normalized
	}
	switch values["action"] {
	case "install":
		req.Action = setupwizard.ActionInstall
	case "uninstall":
		req.Action = setupwizard.ActionUninstall
	case "inspect":
		req.Action = setupwizard.ActionInspect
	case "update":
		req.Action = setupwizard.ActionUpdate
	case "repair":
		req.Action = setupwizard.ActionRepair
	case "":
	default:
		return req, jsonOut, errors.New("invalid_arguments")
	}
	if agents := values["agents"]; agents != "" {
		req.Agents = strings.Split(agents, ",")
	}
	if v, ok := values["hooks"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.Hooks = &on
	}
	if v, ok := values["agent-notify"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.AgentNotify = &on
	}
	if v, ok := values["claude-hooks"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.ClaudeHooks = &on
	}
	if v, ok := values["codex-hooks"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.CodexHooks = &on
	}
	if v, ok := values["claude-agent-notify"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.ClaudeAgentNotify = &on
	}
	if v, ok := values["codex-agent-notify"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.CodexAgentNotify = &on
	}
	req.PackageRoot = values["package"]
	req.PluginRoot = values["plugin-root"]
	req.ControlRoot = values["control-root"]
	req.RuntimeRoot = values["runtime-root"]
	req.GlobalConfig = values["global-config"]
	req.CodexHome = values["codex-home"]
	req.ClaudeConfig = values["claude-config"]
	req.PiAgentDir = values["pi-agent-dir"]
	home, homeErr := os.UserHomeDir()
	cwd, cwdErr := os.Getwd()
	if (len(req.Agents) == 0 || containsProduct(req.Agents, "pi")) && req.PiAgentDir == "" {
		if homeErr != nil || cwdErr != nil {
			if containsProduct(req.Agents, "pi") {
				return req, jsonOut, errors.New("invalid_pi_profile")
			}
		} else {
			var err error
			req.EnvPiAgentDir, err = setupwizard.ResolvePiAgentDir(home, os.Getenv("PI_CODING_AGENT_DIR"), cwd)
			if err != nil && containsProduct(req.Agents, "pi") {
				return req, jsonOut, err
			}
		}
	}
	for key, target := range map[string]**bool{"pi-desktop": &req.PiDesktop, "pi-webhook": &req.PiWebhook} {
		if value, exists := values[key]; exists {
			on, err := parseBoolFlag(value)
			if err != nil {
				return req, jsonOut, err
			}
			*target = &on
		}
	}
	req.PiChannelPreimage = values["pi-channel-preimage"]
	env := setupwizard.ApplyEnvDefaults(setupwizard.Request{})
	req.EnvCodexHome = env.CodexHome
	req.EnvClaudeConfig = env.ClaudeConfig
	for _, path := range []*string{&req.EnvCodexHome, &req.EnvClaudeConfig} {
		if runtime.GOOS == "windows" {
			*path = strings.ReplaceAll(*path, "/", `\`)
		}
		if !filepath.IsAbs(*path) {
			*path = ""
			continue
		}
		*path = filepath.Clean(*path)
	}
	req.ClientExecutable = values["client-executable"]
	if values["claude-executable"] != "" || values["codex-executable"] != "" || values["pi-executable"] != "" {
		req.ClientExecutables = map[string]string{}
		if values["pi-executable"] != "" {
			req.ClientExecutables["pi"] = values["pi-executable"]
		}
		if values["claude-executable"] != "" {
			req.ClientExecutables["claude"] = values["claude-executable"]
		}
		if values["codex-executable"] != "" {
			req.ClientExecutables["codex"] = values["codex-executable"]
		}
	}
	req.Helper = values["helper"]
	req.ScopeRoot = values["scope-root"]
	if containsProduct(req.Agents, "cursor") {
		if len(req.Agents) != 1 || req.ScopeRoot == "" || req.ClientExecutable == "" {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		req.CursorConfig = req.ScopeRoot
		req.ClientExecutables = map[string]string{"cursor": req.ClientExecutable}
	}
	req.InstallationID = values["installation-id"]
	localSelection := values["local-settings"] != "" || values["local-native-stop"] != "" || values["local-mcp"] != "" || values["local-skills"] != "" ||
		values["local-mcp-names"] != "" || values["local-skill-names"] != ""
	if containsProduct(req.Agents, "copilot-vscode") {
		if len(req.Agents) != 1 || req.ScopeRoot == "" || req.ClientExecutable == "" {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		req.ClientExecutables = map[string]string{"copilot-vscode": req.ClientExecutable}
		if localSelection {
			if values["local-settings"] != filepath.Join(req.ScopeRoot, "settings.json") {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			req.LocalConfig = localWizardConfig(values["local-settings"])
			for key, dest := range map[string]*bool{"local-native-stop": &req.LocalConfig.NativeStop} {
				if value := values[key]; value != "" {
					on, e := parseBoolFlag(value)
					if e != nil {
						return req, jsonOut, e
					}
					*dest = on
				}
			}
			for key, dest := range map[string]*[]string{"local-mcp": &req.LocalConfig.MCPServers, "local-skills": &req.LocalConfig.Skills} {
				on := false
				if value := values[key]; value != "" {
					var e error
					on, e = parseBoolFlag(value)
					if e != nil {
						return req, jsonOut, e
					}
					if on {
						name := "agent-notify"
						if key == "local-skills" {
							name = "agent-notifications"
						}
						*dest = []string{name}
					}
				}
				nameFlag := "local-mcp-names"
				if key == "local-skills" {
					nameFlag = "local-skill-names"
				}
				if value := values[nameFlag]; value != "" {
					var names []string
					if !on || json.Unmarshal([]byte(value), &names) != nil || len(names) == 0 {
						return req, jsonOut, errors.New("invalid_arguments")
					}
					seen := make(map[string]bool, len(names))
					for _, name := range names {
						if name == "" || seen[name] {
							return req, jsonOut, errors.New("invalid_arguments")
						}
						seen[name] = true
					}
					*dest = names
				}
			}
		}
	} else if localSelection {
		return req, jsonOut, errors.New("invalid_arguments")
	}
	if values["mcp-config"] != "" || values["claude-mcp-config"] != "" {
		req.MCPConfig = map[string]string{}
		if values["mcp-config"] != "" {
			req.MCPConfig["codex"] = values["mcp-config"]
		}
		if values["claude-mcp-config"] != "" {
			req.MCPConfig["claude"] = values["claude-mcp-config"]
		}
	}
	return req, jsonOut, nil
}

// MSYS hands native Windows processes absolute paths using forward slashes.
// Canonicalize only that separator form; Clean must still reject traversal and
// nonphysical paths before the values reach the installer or its ACL checks.
func normalizeSetupPhysicalPath(path string) (string, error) {
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", `\`)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("invalid_arguments")
	}
	return path, nil
}

func parseBoolFlag(v string) (bool, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("invalid_arguments")
	}
}

func stdinIsCharDevice() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func quoteWizardArgs(args []string) []string {
	if runtime.GOOS == "windows" {
		return quotePowerShellArgs(args)
	}
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return out
}

func quotePowerShellArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		// Single-quoted PowerShell strings preserve $, backticks, and all other
		// metacharacters; apostrophes are represented by two apostrophes.
		out[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
	}
	if len(out) > 0 {
		out[0] = "& " + out[0]
	}
	return out
}

// Private admission accepts only the bootstrap command. Native product/config
// writers have no new flag. An installed same-release copy can read the original
// private stage while retaining exact helper version/source/digest provenance.
func stripBootstrapIntentFile(args []string) ([]string, string, error) {
	out := []string{}
	path := ""
	for n := 0; n < len(args); n++ {
		key, value, inline := strings.Cut(args[n], "=")
		if key != "--bootstrap-intent-file" {
			out = append(out, args[n])
			continue
		}
		if path != "" {
			return out, path, errors.New("invalid_arguments")
		}
		if !inline {
			n++
			if n >= len(args) {
				return out, path, errors.New("invalid_arguments")
			}
			value = args[n]
		}
		if !validProductPath(value) {
			return out, path, errors.New("invalid_arguments")
		}
		path = value
	}
	return out, path, nil
}
func needsWizardInteraction(r setupwizard.Request) bool { return !r.Yes || len(r.Agents) == 0 }

// The same finite UI grammar applies to direct wizard questions. Complete
// --yes/inspect/JSON paths validate it without constructing a terminal.
func stripWizardUIMode(args []string) ([]string, string, error) {
	remaining, uiArgs := []string{}, []string{"select"}
	for n := 0; n < len(args); n++ {
		key, _, inline := strings.Cut(args[n], "=")
		if key != "--ui" && key != "--plain" {
			remaining = append(remaining, args[n])
			continue
		}
		uiArgs = append(uiArgs, args[n])
		if key == "--ui" && !inline {
			n++
			if n >= len(args) {
				return remaining, "", errors.New("invalid_arguments")
			}
			uiArgs = append(uiArgs, args[n])
		}
	}
	parsed, err := parseSetupProducts(uiArgs)
	return remaining, parsed.Mode, err
}
