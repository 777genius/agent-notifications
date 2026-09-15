package setupwizard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrPromptCanceled    = errors.New("prompt canceled")
	ErrPromptUnavailable = errors.New("prompt unavailable; pass --agents and --yes")
	ErrPromptInputClosed = errors.New("prompt input closed before a complete answer")
)

// AgentCapability is the Claude/Codex surface shown by the TTY picker.
// Presence and bindings come from Engine.Discover; the renderer does not
// search PATH.
type AgentCapability struct {
	ID      string
	Present bool
	Path    string
	Bound   bool
	Profile string
}

// Prompter is the thin TTY port. It only fills Request fields; Run owns rules.
type Prompter interface {
	SelectAgents(context.Context, []AgentCapability) ([]string, error)
	SelectExistingAction(context.Context) (Action, error)
	SelectUnits(context.Context) (hooks, notify bool, err error)
	Confirm(context.Context, string) (bool, error)
}

// LinePrompt is a local adapter until UAP publishes the reusable P5 terminal UI.
type LinePrompt struct {
	In  io.Reader
	Out io.Writer
	br  *bufio.Reader
}

func (p *LinePrompt) reader() *bufio.Reader {
	if p.br == nil {
		p.br = bufio.NewReader(p.In)
	}
	return p.br
}

func (p *LinePrompt) SelectAgents(ctx context.Context, clients []AgentCapability) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	claude, codex := "Claude Code", "Codex"
	if len(clients) > 0 {
		claude = agentChoiceLabel(clients, "claude", claude)
		codex = agentChoiceLabel(clients, "codex", codex)
	}
	if _, err := io.WriteString(p.Out, "Install notifications for: 1) "+claude+"  2) "+codex+"  3) Both\nChoice: "); err != nil {
		return nil, err
	}
	line, err := readLine(ctx, p.reader())
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(line) {
	case "1":
		return []string{"claude"}, nil
	case "2":
		return []string{"codex"}, nil
	case "3":
		return []string{"claude", "codex"}, nil
	case "":
		return nil, ErrPromptCanceled
	default:
		return nil, fmt.Errorf("%w: invalid_choice", ErrPromptCanceled)
	}
}

func (p *LinePrompt) SelectExistingAction(ctx context.Context) (Action, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := io.WriteString(p.Out, "Existing agent-notify installation found.\n1) Inspect  2) Add/reinstall  3) Uninstall\nChoice: "); err != nil {
		return "", err
	}
	line, err := readLine(ctx, p.reader())
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(line) {
	case "1":
		return ActionInspect, nil
	case "2":
		return ActionInstall, nil
	case "3":
		return ActionUninstall, nil
	case "":
		return "", ErrPromptCanceled
	default:
		return "", fmt.Errorf("%w: invalid_choice", ErrPromptCanceled)
	}
}

func (p *LinePrompt) SelectUnits(ctx context.Context) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if _, err := io.WriteString(p.Out, "Units: 1) Hooks  2) Agent-initiated notify (MCP+skill)  3) Both\nChoice: "); err != nil {
		return false, false, err
	}
	line, err := readLine(ctx, p.reader())
	if err != nil {
		return false, false, err
	}
	switch strings.TrimSpace(line) {
	case "1":
		return true, false, nil
	case "2":
		return false, true, nil
	case "3":
		return true, true, nil
	case "":
		return false, false, ErrPromptCanceled
	default:
		return false, false, fmt.Errorf("%w: invalid_choice", ErrPromptCanceled)
	}
}

func (p *LinePrompt) Confirm(ctx context.Context, summary string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if summary == "" {
		summary = "Proceed with setup?"
	}
	if _, err := fmt.Fprintf(p.Out, "%s [y/N]: ", summary); err != nil {
		return false, err
	}
	line, err := readLine(ctx, p.reader())
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%w: invalid_choice", ErrPromptCanceled)
	}
}

// FillInteractive copies req and asks only for omitted mutation choices.
// existing reports live portable bindings for the selected agents; nil means
// treat the machine as new. Detection stays outside this adapter.
func FillInteractive(ctx context.Context, req Request, p Prompter, existing func([]string) []string) (Request, error) {
	if p == nil {
		return req, ErrPromptUnavailable
	}
	if ctx == nil {
		return req, ErrRefused
	}
	if req.Action == ActionInspect {
		return req, nil
	}
	if len(req.Agents) == 0 {
		var clients []AgentCapability
		if req.DiscoverAgents != nil {
			clients = req.DiscoverAgents()
		}
		agents, err := p.SelectAgents(ctx, clients)
		if err != nil {
			return req, err
		}
		req.Agents = agents
	}
	if len(req.Agents) == 0 {
		return req, ErrPromptCanceled
	}
	if req.Action == "" {
		if existing != nil && len(existing(req.Agents)) > 0 {
			action, err := p.SelectExistingAction(ctx)
			if err != nil {
				return req, err
			}
			req.Action = action
		} else {
			req.Action = ActionInstall
		}
	}
	if req.Action == ActionInspect {
		return req, nil
	}
	if unitFlagsOmitted(req) && !req.Yes {
		hooks, notify, err := p.SelectUnits(ctx)
		if err != nil {
			return req, err
		}
		req.Hooks = boolPtr(hooks)
		req.AgentNotify = boolPtr(notify)
	}
	return req, nil
}

func confirmPlan(req Request) string {
	action := string(req.Action)
	if action == "" {
		action = string(ActionInstall)
	}
	agents := strings.Join(req.Agents, ",")
	if agents == "" {
		agents = "none"
	}
	unit := func(flag *bool, installDefault string) string {
		if flag == nil {
			if req.Action == ActionUninstall {
				return "all-managed"
			}
			return installDefault
		}
		return boolFlag(*flag)
	}
	summary := fmt.Sprintf("Plan: action=%s agents=%s hooks=%s agent-notify=%s",
		action, agents, unit(req.Hooks, "on"), unit(req.AgentNotify, "on"))
	perClient := func(name string, flag *bool) {
		if flag == nil {
			return
		}
		summary += fmt.Sprintf(" %s=%s", name, boolFlag(*flag))
	}
	perClient("claude-hooks", req.ClaudeHooks)
	perClient("codex-hooks", req.CodexHooks)
	perClient("claude-agent-notify", req.ClaudeAgentNotify)
	perClient("codex-agent-notify", req.CodexAgentNotify)
	if req.CodexHome != "" {
		summary += " codex-profile=" + req.CodexHome
	}
	if req.ClaudeConfig != "" {
		summary += " claude-profile=" + req.ClaudeConfig
	}
	if req.ReleaseVersion != "" {
		summary += " revision=" + req.ReleaseVersion
	}
	if req.PackageSHA256 != "" {
		summary += " digest=" + req.PackageSHA256
	}
	if req.InstallationID != "" {
		summary += " installation-id=" + req.InstallationID
	}
	switch req.Action {
	case ActionUninstall:
		summary += " required=none permission-dialog=skipped"
	case ActionInstall, "":
		summary += " required=restart,request-permission,test-notification permission-dialog=explicit delivery=not_verified"
	}
	return summary + ". Proceed?"
}

func agentChoiceLabel(clients []AgentCapability, id, name string) string {
	for _, client := range clients {
		if client.ID != id {
			continue
		}
		switch {
		case client.Present && client.Bound:
			return agentInstalledLabel(name+" (executable present, installed)", client.Profile)
		case client.Present:
			return name + " (executable present)"
		case client.Bound:
			return agentInstalledLabel(name+" (executable not found, installed)", client.Profile)
		default:
			return name + " (executable not found)"
		}
	}
	return name
}

func agentInstalledLabel(base, profile string) string {
	if profile == "" {
		return base
	}
	return base + " profile=" + profile
}

func readLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) {
		if strings.TrimSpace(line) == "" {
			return "", ErrPromptInputClosed
		}
		err = nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func attachCommand(req Request, out Result) Result {
	switch out.Outcome {
	case "completed", "cancelled", "unchanged":
		return out
	}
	if len(out.Command) == 0 {
		out.Command = RetryCommand(req)
	}
	return out
}

// RetryCommand is the structured argv that repeats this wizard request.
func RetryCommand(req Request) []string {
	cmd := []string{"setup-notifications", "wizard"}
	if req.Action != "" {
		cmd = append(cmd, "--action", string(req.Action))
	}
	if len(req.Agents) > 0 {
		cmd = append(cmd, "--agents", strings.Join(req.Agents, ","))
	}
	if req.Hooks != nil {
		cmd = append(cmd, "--hooks", boolFlag(*req.Hooks))
	}
	if req.AgentNotify != nil {
		cmd = append(cmd, "--agent-notify", boolFlag(*req.AgentNotify))
	}
	if req.ClaudeHooks != nil {
		cmd = append(cmd, "--claude-hooks", boolFlag(*req.ClaudeHooks))
	}
	if req.CodexHooks != nil {
		cmd = append(cmd, "--codex-hooks", boolFlag(*req.CodexHooks))
	}
	if req.ClaudeAgentNotify != nil {
		cmd = append(cmd, "--claude-agent-notify", boolFlag(*req.ClaudeAgentNotify))
	}
	if req.CodexAgentNotify != nil {
		cmd = append(cmd, "--codex-agent-notify", boolFlag(*req.CodexAgentNotify))
	}
	appendPath := func(flag, value string) {
		if value != "" {
			cmd = append(cmd, flag, value)
		}
	}
	appendPath("--package", req.PackageRoot)
	appendPath("--plugin-root", req.PluginRoot)
	appendPath("--control-root", req.ControlRoot)
	appendPath("--runtime-root", req.RuntimeRoot)
	appendPath("--global-config", req.GlobalConfig)
	appendPath("--codex-home", req.CodexHome)
	appendPath("--claude-config", req.ClaudeConfig)
	appendPath("--client-executable", req.ClientExecutable)
	if req.ClientExecutables != nil {
		appendPath("--claude-executable", req.ClientExecutables["claude"])
		appendPath("--codex-executable", req.ClientExecutables["codex"])
	}
	appendPath("--helper", req.Helper)
	appendPath("--scope-root", req.ScopeRoot)
	if req.InstallationID != "" {
		cmd = append(cmd, "--installation-id", req.InstallationID)
	}
	if req.MCPConfig != nil {
		appendPath("--mcp-config", req.MCPConfig["codex"])
		appendPath("--claude-mcp-config", req.MCPConfig["claude"])
	}
	if req.Action != ActionInspect {
		cmd = append(cmd, "--yes")
	}
	if req.ExternalUninstalled {
		cmd = append(cmd, "--external-uninstalled")
	}
	return cmd
}

func boolFlag(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
