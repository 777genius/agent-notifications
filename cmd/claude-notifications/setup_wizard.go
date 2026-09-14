package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const setupWizardHelp = `Usage: claude-notifications setup-notifications wizard [OPTIONS]
Noninteractive master for hooks plus portable MCP/skill. It does not prompt.
  --action install|uninstall|inspect
  --agents claude,codex
  --hooks true|false          Omit on install to include hooks; omit on uninstall to select all units
  --agent-notify true|false   Omit on install to include portable MCP+skill
  --yes                       Required for mutation
  --json
  --package PATH              Local standard package root (plugin.json + MCP/skills)
  --plugin-root PATH          Existing plugin bundle for Codex hooks-only setup
  --control-root PATH         Existing managed control directory
  --runtime-root PATH         Existing managed runtime (default: ledger runtime)
  --global-config PATH
  --codex-home PATH           Explicit Codex UAP client config root
  --claude-config PATH        Explicit Claude UAP client config root
  --client-executable PATH    Selected client executable; never launched from PATH
  --helper PATH               Managed stdio helper (default: runtime primary)
  --scope-root PATH
  --installation-id ID
  --mcp-config PATH           Owned Codex MCP config to hand off (repeat not supported; use --claude-mcp-config)
  --claude-mcp-config PATH
Update and repair are not published in this checkpoint.
`

func executeSetupWizard(ctx context.Context, args []string, out io.Writer) int {
	if len(args) == 1 && args[0] == "--help" {
		_, err := io.WriteString(out, setupWizardHelp)
		if err != nil {
			return 1
		}
		return 0
	}
	req, jsonOut, err := parseSetupWizard(args)
	if err != nil {
		if jsonOut {
			_ = json.NewEncoder(out).Encode(setupwizard.Result{Outcome: "invalid", Reason: "invalid_arguments"})
		} else {
			_, _ = fmt.Fprintln(out, "invalid_arguments")
		}
		return 2
	}
	if req.ControlRoot == "" {
		root, e := installruntime.ControlRoot()
		if e != nil {
			return 1
		}
		req.ControlRoot = root
	}
	result, err := setupwizard.Run(ctx, req)
	if jsonOut {
		if e := json.NewEncoder(out).Encode(result); e != nil {
			return 1
		}
	} else {
		_, _ = fmt.Fprintf(out, "%s; reason=%s; generation=%d.\n", result.Outcome, result.Reason, result.Generation)
		for _, target := range result.Targets {
			_, _ = fmt.Fprintf(out, "%s %s: %s %s\n", target.Client, target.Unit, target.Outcome, target.Reason)
		}
	}
	if result.ExitCode() != 0 {
		return result.ExitCode()
	}
	if err != nil && result.Outcome != "completed" && result.Outcome != "unchanged" && result.Outcome != "cancelled" {
		return 1
	}
	return 0
}

func parseSetupWizard(args []string) (setupwizard.Request, bool, error) {
	var req setupwizard.Request
	if len(args) > 48 {
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
		"package": true, "plugin-root": true, "control-root": true, "runtime-root": true, "global-config": true,
		"codex-home": true, "claude-config": true, "client-executable": true, "helper": true,
		"scope-root": true, "installation-id": true, "mcp-config": true, "claude-mcp-config": true,
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
		return req, jsonOut, errors.New("invalid_arguments")
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
	req.PackageRoot = values["package"]
	req.PluginRoot = values["plugin-root"]
	req.ControlRoot = values["control-root"]
	req.RuntimeRoot = values["runtime-root"]
	req.GlobalConfig = values["global-config"]
	req.CodexHome = values["codex-home"]
	req.ClaudeConfig = values["claude-config"]
	req.ClientExecutable = values["client-executable"]
	req.Helper = values["helper"]
	req.ScopeRoot = values["scope-root"]
	req.InstallationID = values["installation-id"]
	if values["mcp-config"] != "" || values["claude-mcp-config"] != "" {
		req.MCPConfig = map[string]string{}
		if values["mcp-config"] != "" {
			req.MCPConfig["codex"] = values["mcp-config"]
		}
		if values["claude-mcp-config"] != "" {
			req.MCPConfig["claude"] = values["claude-mcp-config"]
		}
	}
	for _, p := range []string{req.PackageRoot, req.PluginRoot, req.ControlRoot, req.RuntimeRoot, req.GlobalConfig, req.CodexHome, req.ClaudeConfig, req.ClientExecutable, req.Helper, req.ScopeRoot} {
		if p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p) {
			return req, jsonOut, errors.New("invalid_arguments")
		}
	}
	return req, jsonOut, nil
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
