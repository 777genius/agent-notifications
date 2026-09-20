package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/777genius/plugin-kit-ai/cli/installerui"
)

// PublicPrompt is the Notifications adapter for the neutral UAP terminal UI.
// It maps product choices to opaque IDs and keeps all wizard policy in this
// package; installerui never knows about hooks, MCP, or agent-notify.
type PublicPrompt struct {
	ui  *installerui.UI
	out io.Writer
}

func NewPublicPrompt(in io.Reader, out io.Writer) (*PublicPrompt, error) {
	ui, err := installerui.New(installerui.Config{Input: in, Output: out})
	if err != nil {
		return nil, normalizePublicError(err)
	}
	return &PublicPrompt{ui: ui, out: out}, nil
}

func (p *PublicPrompt) SelectAgents(ctx context.Context, clients []AgentCapability) ([]string, error) {
	options := []installerui.Option{
		{ID: "claude", Label: agentChoiceLabel(clients, "claude", "Claude Code")},
		{ID: "codex", Label: agentChoiceLabel(clients, "codex", "Codex")},
	}
	result, err := p.ui.SelectMany(ctx, installerui.SelectRequest{Title: "Install notifications for", Options: options})
	if err != nil {
		return nil, normalizePublicError(err)
	}
	if result.Cancelled {
		return nil, ErrPromptCanceled
	}
	return result.IDs, nil
}

func (p *PublicPrompt) SelectExistingAction(ctx context.Context) (Action, error) {
	_, _ = io.WriteString(p.out, "Existing agent-notify installation found.\n")
	result, err := p.ui.SelectOne(ctx, installerui.SelectRequest{Title: "Existing agent-notify installation found", Options: []installerui.Option{
		{ID: string(ActionInspect), Label: "Inspect"},
		{ID: string(ActionInstall), Label: "Add/reinstall"},
		{ID: string(ActionUninstall), Label: "Uninstall"},
		{ID: string(ActionUpdate), Label: "Update"},
		{ID: string(ActionRepair), Label: "Repair"},
	}})
	if err != nil {
		return "", normalizePublicError(err)
	}
	if result.Cancelled {
		return "", ErrPromptCanceled
	}
	return Action(result.IDs[0]), nil
}

func (p *PublicPrompt) SelectUnits(ctx context.Context) (bool, bool, error) {
	_, _ = io.WriteString(p.out, "Units: 1) Hooks  2) Agent-initiated notify (MCP+skill)  3) Both\n")
	result, err := p.ui.SelectOne(ctx, installerui.SelectRequest{Title: "Units", Options: unitOptions()})
	if err != nil {
		return false, false, normalizePublicError(err)
	}
	if result.Cancelled {
		return false, false, ErrPromptCanceled
	}
	return unitsFor(result.IDs[0])
}

func (p *PublicPrompt) SelectLiveUnits(ctx context.Context, live []ClientUnits) (bool, bool, bool, error) {
	var state strings.Builder
	state.WriteString("Current units differ per client; omitted flags keep each target:\n")
	for _, unit := range live {
		fmt.Fprintf(&state, "  %s: hooks=%s agent-notify=%s\n", unit.Client, boolFlag(unit.Hooks), boolFlag(unit.Notify))
	}
	_, _ = io.WriteString(p.out, state.String())
	options := []installerui.Option{
		{ID: "keep", Label: "Keep current per client"},
		{ID: "hooks", Label: "Hooks for all"},
		{ID: "notify", Label: "Agent-initiated notify for all"},
		{ID: "both", Label: "Both for all"},
	}
	for i := range options {
		if i < len(live) {
			options[i].Label += " (current state shown in host plan)"
		}
	}
	result, err := p.ui.SelectOne(ctx, installerui.SelectRequest{Title: "Current units differ per client", Options: options})
	if err != nil {
		return false, false, false, normalizePublicError(err)
	}
	if result.Cancelled {
		return false, false, false, ErrPromptCanceled
	}
	switch result.IDs[0] {
	case "keep":
		return true, false, false, nil
	case "hooks":
		return false, true, false, nil
	case "notify":
		return false, false, true, nil
	case "both":
		return false, true, true, nil
	default:
		return false, false, false, errors.New("invalid public unit selection")
	}
}

func (p *PublicPrompt) Confirm(ctx context.Context, summary string) (bool, error) {
	result, err := p.ui.Confirm(ctx, installerui.ConfirmRequest{Title: summary})
	if err != nil {
		return false, normalizePublicError(err)
	}
	if result.Cancelled {
		return false, ErrPromptCanceled
	}
	return result.Accepted, nil
}

func unitOptions() []installerui.Option {
	return []installerui.Option{
		{ID: "hooks", Label: "Hooks"},
		{ID: "notify", Label: "Agent-initiated notify (MCP+skill)"},
		{ID: "both", Label: "Both"},
	}
}

func unitsFor(id string) (bool, bool, error) {
	switch id {
	case "hooks":
		return true, false, nil
	case "notify":
		return false, true, nil
	case "both":
		return true, true, nil
	default:
		return false, false, errors.New("invalid public unit selection")
	}
}

func normalizePublicError(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return ErrPromptInputClosed
	case errors.Is(err, installerui.ErrUnavailable):
		return ErrPromptUnavailable
	default:
		return err
	}
}
