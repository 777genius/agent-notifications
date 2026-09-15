package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const setupWizardHelp = `Usage: claude-notifications setup-notifications wizard [OPTIONS]
Master for hooks plus portable MCP/skill.
TTY stdin prompts for agents, an existing-install action, and units when those flags are omitted, then shows a preflight plan and asks for confirmation.
--json never prompts. Progress phases go to stderr. No TTY and no --agents/--yes is invalid for mutation, not a hang.
Without --action, a new machine defaults to install; an existing portable
installation is offered inspect, add/reinstall, uninstall, update, or repair.
  --action install|uninstall|inspect|update|repair
  --agents claude,codex   Omit on inspect to report both clients
  --hooks true|false          Omit on install to include hooks; omit on uninstall to select all units
  --agent-notify true|false   Omit on install to include portable MCP+skill
  --claude-hooks true|false   Per-client override; mixed Claude/Codex opt-outs are not collapsed
  --codex-hooks true|false
  --claude-agent-notify true|false
  --codex-agent-notify true|false
  --yes                       Required for mutation without a TTY unless a matching pending intent exists
  --external-uninstalled      Codex native plugin already removed or never activated; --yes does not set this
  --json
  --package PATH              Local standard package root or same-release zip
  --plugin-root PATH          Existing plugin bundle for Codex hooks-only setup
  --control-root PATH         Existing managed control directory
  --runtime-root PATH         Existing managed runtime (default: ledger runtime)
  --global-config PATH
  --codex-home PATH           Explicit Codex UAP client config root; else CODEX_HOME once
  --claude-config PATH        Explicit Claude UAP client config root; else CLAUDE_CONFIG_DIR once
  --client-executable PATH    Selected client executable; never launched from PATH
  --claude-executable PATH    Claude executable when installing both clients
  --codex-executable PATH     Codex executable when installing both clients
  --helper PATH               Managed stdio helper (default: runtime primary)
  --scope-root PATH
  --installation-id ID
  --mcp-config PATH           Owned Codex MCP config to hand off (repeat not supported; use --claude-mcp-config)
  --claude-mcp-config PATH
Update and repair require an existing owned binding; missing files remain repairable. Groups remain unpublished.
`

func executeSetupWizard(ctx context.Context, args []string, out io.Writer) int {
	return executeSetupWizardWith(ctx, args, out, os.Stderr, os.Stdin, stdinIsCharDevice())
}

func executeSetupWizardWith(ctx context.Context, args []string, out, errOut io.Writer, in io.Reader, tty bool) int {
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
	if req.Action == "" && (!tty || jsonOut) {
		if jsonOut {
			_ = json.NewEncoder(out).Encode(setupwizard.Result{Outcome: "invalid", Reason: "invalid_arguments"})
		} else {
			_, _ = fmt.Fprintln(out, "invalid_arguments")
		}
		return 2
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = config.ConsumerVersion
	}
	if req.ReleaseDownloadRoot == "" {
		req.ReleaseDownloadRoot = portableasset.DefaultReleaseDownloadRoot
	}
	if errOut != nil {
		req.Progress = func(phase string) {
			_, _ = fmt.Fprintln(errOut, "phase", phase)
		}
	}
	needsPrompt := tty && !jsonOut && (req.Action == "" || (req.Action != setupwizard.ActionInspect && (len(req.Agents) == 0 || !req.Yes)))
	prompt := &setupwizard.LinePrompt{In: in, Out: out}
	if needsPrompt {
		req.DiscoverAgents = func() []setupwizard.AgentCapability {
			return setupwizard.DiscoverAgents(req)
		}
		filled, e := setupwizard.FillInteractive(ctx, req, prompt, func(agents []string) []string {
			return setupwizard.LiveNotifyClients(req.ControlRoot, agents)
		})
		if e != nil {
			return writeSetupWizardPromptError(out, jsonOut, req, e)
		}
		req = filled
	}
	if req.Action != setupwizard.ActionInspect && !req.Yes && tty && !jsonOut {
		plan, e := setupwizard.Plan(ctx, req)
		req = plan.Request
		if !plan.Ready {
			if plan.Text != "" {
				_, _ = fmt.Fprintln(out, plan.Text)
			}
			return writeSetupWizardResult(out, jsonOut, plan.Result, e)
		}
		ok, e := prompt.Confirm(ctx, plan.Text)
		if e != nil {
			return writeSetupWizardPromptError(out, jsonOut, req, e)
		}
		if !ok {
			result := setupwizard.Result{Action: string(req.Action), Outcome: "cancelled", Reason: "prompt_canceled"}
			return writeSetupWizardResult(out, jsonOut, result, nil)
		}
		req.Yes = true
	}
	result, err := setupwizard.Run(ctx, req)
	return writeSetupWizardResult(out, jsonOut, result, err)
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
		_, _ = fmt.Fprintf(out, "%s; reason=%s; generation=%d.\n", result.Outcome, result.Reason, result.Generation)
		for _, target := range result.Targets {
			_, _ = fmt.Fprintf(out, "%s %s: %s %s\n", target.Client, target.Unit, target.Outcome, target.Reason)
		}
		for _, fact := range result.Readiness {
			_, _ = fmt.Fprintf(out, "%s readiness: runtime=%s hooks=%s mcp=%s permission=%s restart=%s delivery=%s\n",
				fact.Client, fact.Runtime, fact.Hooks, fact.MCP, fact.Permission, fact.Restart, fact.Delivery)
		}
		for _, next := range result.NextActions {
			_, _ = fmt.Fprintf(out, "next %s: %s\n", next.Kind, strings.Join(quoteWizardArgs(next.Command), " "))
		}
		if len(result.Command) > 0 {
			_, _ = fmt.Fprintf(out, "retry: %s\n", strings.Join(quoteWizardArgs(result.Command), " "))
		}
	}
	if result.ExitCode() != 0 {
		return result.ExitCode()
	}
	if err != nil && result.Outcome != "completed" && result.Outcome != "unchanged" && result.Outcome != "cancelled" && result.Outcome != "ready" {
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
		"claude-hooks": true, "codex-hooks": true, "claude-agent-notify": true, "codex-agent-notify": true,
		"package": true, "plugin-root": true, "control-root": true, "runtime-root": true, "global-config": true,
		"codex-home": true, "claude-config": true, "client-executable": true, "helper": true,
		"scope-root": true, "installation-id": true, "mcp-config": true, "claude-mcp-config": true,
		"claude-executable": true, "codex-executable": true,
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
	req = setupwizard.ApplyEnvDefaults(req)
	req.ClientExecutable = values["client-executable"]
	if values["claude-executable"] != "" || values["codex-executable"] != "" {
		req.ClientExecutables = map[string]string{}
		if values["claude-executable"] != "" {
			req.ClientExecutables["claude"] = values["claude-executable"]
		}
		if values["codex-executable"] != "" {
			req.ClientExecutables["codex"] = values["codex-executable"]
		}
	}
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
	for _, p := range []string{req.PackageRoot, req.PluginRoot, req.ControlRoot, req.RuntimeRoot, req.GlobalConfig, req.CodexHome, req.ClaudeConfig, req.ClientExecutable, req.Helper, req.ScopeRoot, req.ClientExecutables["claude"], req.ClientExecutables["codex"]} {
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

func stdinIsCharDevice() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func quoteWizardArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"") {
			out[i] = strconv.Quote(arg)
			continue
		}
		out[i] = arg
	}
	return out
}
