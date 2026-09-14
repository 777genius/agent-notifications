package setupwizard

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func promptCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestFillInteractiveSelectsBothAndConfirms(t *testing.T) {
	in := strings.NewReader("3\ny\n")
	var out strings.Builder
	got, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out})
	if err != nil || !got.Yes || strings.Join(got.Agents, ",") != "claude,codex" {
		t.Fatalf("fill: %+v %v", got, err)
	}
	if !strings.Contains(out.String(), "Claude Code") || !strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("prompt text: %s", out.String())
	}
}

func TestFillInteractiveCancelIsEmptySelection(t *testing.T) {
	in := strings.NewReader("\n")
	var out strings.Builder
	_, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out})
	if err != ErrPromptCanceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestFillInteractiveEOF(t *testing.T) {
	in := strings.NewReader("")
	var out strings.Builder
	_, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out})
	if err != ErrPromptInputClosed {
		t.Fatalf("eof: %v", err)
	}
}

func TestFillInteractiveLeavesInspectAlone(t *testing.T) {
	got, err := FillInteractive(promptCtx(t), Request{Action: ActionInspect, Agents: []string{"codex"}}, &LinePrompt{In: strings.NewReader("1\n"), Out: io.Discard})
	if err != nil || got.Yes || strings.Join(got.Agents, ",") != "codex" {
		t.Fatalf("inspect: %+v %v", got, err)
	}
}

func TestRetryCommandIncludesYesAndPaths(t *testing.T) {
	off := false
	claudeOff := false
	cmd := RetryCommand(Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true, Hooks: &off,
		ClaudeAgentNotify: &claudeOff,
		ControlRoot:       "/tmp/control", CodexHome: "/tmp/codex",
		ClientExecutables: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex"},
	})
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--agents claude,codex") || !strings.Contains(joined, "--claude-executable /bin/claude") || !strings.Contains(joined, "--codex-executable /bin/codex") || !strings.Contains(joined, "--claude-agent-notify false") {
		t.Fatalf("command: %v", cmd)
	}
}

func TestRunAttachesRetryCommandOnIncomplete(t *testing.T) {
	got, err := Run(promptCtx(t), Request{Action: ActionUpdate, Agents: []string{"codex"}, Yes: true, ControlRoot: "/tmp/control"})
	if err == nil || got.Outcome != "incomplete" || len(got.Command) == 0 {
		t.Fatalf("command: %+v %v", got, err)
	}
	if got.Command[0] != "setup-notifications" || got.Command[1] != "wizard" {
		t.Fatalf("argv: %v", got.Command)
	}
}

func TestFillInteractiveNilPrompter(t *testing.T) {
	_, err := FillInteractive(context.Background(), Request{Action: ActionInstall}, nil)
	if err != ErrPromptUnavailable {
		t.Fatalf("nil: %v", err)
	}
}
