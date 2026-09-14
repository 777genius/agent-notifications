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

// Prompter is the thin TTY port. It only fills Request fields; Run owns rules.
type Prompter interface {
	SelectAgents(context.Context) ([]string, error)
	SelectExistingAction(context.Context) (Action, error)
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

func (p *LinePrompt) SelectAgents(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(p.Out, "Install notifications for: 1) Claude Code  2) Codex  3) Both\nChoice: "); err != nil {
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
		agents, err := p.SelectAgents(ctx)
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
	if !req.Yes {
		ok, err := p.Confirm(ctx, "Proceed with "+string(req.Action)+"?")
		if err != nil {
			return req, err
		}
		req.Yes = ok
		if !ok {
			return req, ErrPromptCanceled
		}
	}
	return req, nil
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
	out.Command = RetryCommand(req)
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
	return cmd
}

func boolFlag(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
