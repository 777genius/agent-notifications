package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
When Claude and Codex already have different units, the TTY shows both and does not collapse omission to one bool.
--json never prompts. Progress phases go to stderr. No TTY and no --agents/--yes is invalid for mutation, not a hang. Omitted --agents with --yes is still invalid unless a matching pending intent restores them.
Inspect exit 0 means the report was read; readiness stays in result fields.
Without --action, a new machine defaults to install; an existing portable
installation is offered inspect, add/reinstall, uninstall, update, or repair.
  --action install|uninstall|inspect|update|repair
  --install-or-update       Bootstrap-only: install selected clients or update owned bindings first
  --agents claude,codex   Omit on inspect to report both clients
  --hooks true|false          Omit on install of new targets to include hooks; omit on update/repair to keep live units; omit on uninstall to select all units
  --agent-notify true|false   Omit on install of new targets to include portable MCP+skill; omit on update/repair to keep live units
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
  --mcp-config PATH           Owned Codex MCP config to hand off; omit to use an existing config.toml in the selected Codex profile
  --claude-mcp-config PATH    Owned Claude MCP config to hand off; omit to use an existing .claude.json in the selected Claude profile
Update and repair require an existing owned binding; missing files remain repairable. Two Claude+Codex notify installs when both are unbound, and update/repair of two live same-revision bindings, use one group apply. Install onto mixed live revisions requires Update of the behind sibling, not group Add. Install of both when one client is already live on an older revision is Update of that client then Add of the missing one. Mixed Codex uninstall still needs --external-uninstalled before Claude is removed with it.
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
	args, installOrUpdate, flagErr := stripInstallOrUpdate(args)
	req, jsonOut, err := parseSetupWizard(args)
	if flagErr != nil && err == nil {
		err = flagErr
	}
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
		req.DefaultReleaseVersion = config.ConsumerVersion
	}
	if req.ReleaseDownloadRoot == "" {
		req.ReleaseDownloadRoot = portableasset.DefaultReleaseDownloadRoot
	}
	if installOrUpdate && !validInstallOrUpdateRequest(req) {
		return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, nil)
	}
	if errOut != nil {
		req.Progress = func(phase string) {
			_, _ = fmt.Fprintln(errOut, "phase", phase)
		}
	}
	needsPrompt := tty && !jsonOut && (req.Action == "" || (req.Action != setupwizard.ActionInspect && (len(req.Agents) == 0 || !req.Yes)))
	var prompt setupwizard.Prompter
	if tty && !jsonOut {
		prompt, err = setupwizard.NewPublicPrompt(in, out)
		if err != nil {
			return writeSetupWizardPromptError(out, jsonOut, req, err)
		}
	}
	if needsPrompt {
		req.DiscoverAgents = func() []setupwizard.AgentCapability {
			return setupwizard.DiscoverAgents(req)
		}
		filled, e := setupwizard.FillInteractive(ctx, req, prompt, func(agents []string) []string {
			return setupwizard.LiveSetupClients(req, agents)
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
	var result setupwizard.Result
	if installOrUpdate {
		result, err = runInstallOrUpdate(ctx, req)
	} else {
		result, err = setupwizard.Run(ctx, req)
	}
	return writeSetupWizardResult(out, jsonOut, result, err)
}

// The bootstrap flag is intentionally outside the public wizard Request. It
// changes orchestration, not the meaning of any explicit wizard action.
func stripInstallOrUpdate(args []string) ([]string, bool, error) {
	filtered := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg != "--install-or-update" {
			filtered = append(filtered, arg)
			continue
		}
		if found {
			return filtered, false, errors.New("invalid_arguments")
		}
		found = true
	}
	return filtered, found, nil
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
		if (agent != "claude" && agent != "codex") || seen[agent] {
			return false
		}
		seen[agent] = true
	}
	return true
}

func runInstallOrUpdate(ctx context.Context, req setupwizard.Request) (setupwizard.Result, error) {
	inspect := req
	inspect.Action = setupwizard.ActionInspect
	before, inspectErr := setupwizard.Run(ctx, inspect)
	if inspectErr != nil {
		return before, inspectErr
	}
	for _, next := range before.NextActions {
		if next.Kind == "recover" || next.Kind == "resume" {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required", NextActions: before.NextActions}, setupwizard.ErrRefused
		}
	}
	plan, err := setupwizard.Plan(ctx, req)
	if plan.Ready && (plan.Request.Action != req.Action || strings.Join(plan.Request.Agents, ",") != strings.Join(req.Agents, ",")) {
		return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required"}, setupwizard.ErrRefused
	}
	if plan.Ready {
		result, runErr := setupwizard.Run(ctx, plan.Request)
		if runErr != nil || result.ExitCode() != 0 {
			return result, runErr
		}
		return verifyInstallOrUpdate(ctx, req, result)
	}
	if plan.Result.Reason != "update_required" || !validInstallOrUpdateActions(req.Agents, plan.Result.NextActions) {
		return plan.Result, err
	}
	var last setupwizard.Result
	for _, next := range plan.Result.NextActions {
		phase := req
		phase.Action = setupwizard.Action(next.Kind)
		phase.Agents = append([]string(nil), next.Agents...)
		phasePlan, planErr := setupwizard.Plan(ctx, phase)
		if !phasePlan.Ready {
			return phasePlan.Result, planErr
		}
		last, err = setupwizard.Run(ctx, phasePlan.Request)
		if err != nil || last.ExitCode() != 0 {
			return last, err
		}
	}
	return verifyInstallOrUpdate(ctx, req, last)
}

func validInstallOrUpdateActions(selected []string, next []setupwizard.NextAction) bool {
	if len(next) < 1 || len(next) > 2 || next[0].Kind != "update" ||
		(len(next) == 2 && next[1].Kind != "install") {
		return false
	}
	allowed := map[string]bool{}
	for _, agent := range selected {
		allowed[agent] = true
	}
	seen := map[string]bool{}
	for i, action := range next {
		if len(action.Agents) == 0 {
			return false
		}
		phaseSeen := map[string]bool{}
		for _, agent := range action.Agents {
			if !allowed[agent] || phaseSeen[agent] {
				return false
			}
			phaseSeen[agent] = true
			// A retained-empty installation needs a metadata-only update,
			// then an install of the same selected client to restore delivery.
			if seen[agent] && !(i == 1 && next[0].Reason == "update_existing_before_add" && action.Reason == "add_after_update") {
				return false
			}
			seen[agent] = true
		}
	}
	return true
}

func verifyInstallOrUpdate(ctx context.Context, req setupwizard.Request, result setupwizard.Result) (setupwizard.Result, error) {
	inspect := req
	inspect.Action = setupwizard.ActionInspect
	view, err := setupwizard.Run(ctx, inspect)
	if err != nil {
		return view, err
	}
	installed := map[string]bool{}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			installed[target.Client] = true
		}
	}
	for _, agent := range req.Agents {
		if !installed[agent] {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "post_install_incomplete", Targets: view.Targets}, setupwizard.ErrRefused
		}
	}
	return result, nil
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
		line := fmt.Sprintf("%s; reason=%s; generation=%d", result.Outcome, result.Reason, result.Generation)
		if result.InstallationID != "" {
			line += " installation-id=" + result.InstallationID
		}
		if result.DataRetained {
			line += " data_retained=true"
		}
		_, _ = fmt.Fprintln(out, line+".")
		for _, target := range result.Targets {
			line := target.Client + " " + target.Unit + ": " + target.Outcome
			if target.Reason != "" {
				line += " " + target.Reason
			}
			if target.Profile != "" {
				line += " profile=" + target.Profile
			}
			if target.TreeDigest != "" {
				line += " digest=" + target.TreeDigest
			}
			if target.ConfigPath != "" {
				line += " mcp=" + target.ConfigPath
			}
			_, _ = fmt.Fprintln(out, line)
		}
		for _, fact := range result.Readiness {
			_, _ = fmt.Fprintf(out, "%s readiness: runtime=%s hooks=%s mcp=%s permission=%s restart=%s delivery=%s\n",
				fact.Client, fact.Runtime, fact.Hooks, fact.MCP, fact.Permission, fact.Restart, fact.Delivery)
		}
		for _, next := range result.NextActions {
			_, _ = fmt.Fprintf(out, "next %s: %s\n", next.Kind, strings.Join(quoteWizardArgs(wizardPrintableCommand(next.Command)), " "))
		}
		if len(result.Command) > 0 {
			_, _ = fmt.Fprintf(out, "retry: %s\n", strings.Join(quoteWizardArgs(wizardPrintableCommand(result.Command)), " "))
		}
	}
	if result.ExitCode() != 0 {
		return result.ExitCode()
	}
	if result.Action == string(setupwizard.ActionInspect) {
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
	out := make([]string, 0, len(command)+1)
	out = append(out, executable)
	return append(out, command...)
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
	env := setupwizard.ApplyEnvDefaults(setupwizard.Request{})
	req.EnvCodexHome = env.CodexHome
	req.EnvClaudeConfig = env.ClaudeConfig
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
	for _, p := range []string{req.PackageRoot, req.PluginRoot, req.ControlRoot, req.RuntimeRoot, req.GlobalConfig, req.CodexHome, req.ClaudeConfig, req.EnvCodexHome, req.EnvClaudeConfig, req.ClientExecutable, req.Helper, req.ScopeRoot, req.ClientExecutables["claude"], req.ClientExecutables["codex"]} {
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
