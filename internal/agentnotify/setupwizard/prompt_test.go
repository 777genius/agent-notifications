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
	in := strings.NewReader("3\n3\n")
	var out strings.Builder
	got, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out}, nil)
	if err != nil || got.Yes || strings.Join(got.Agents, ",") != "claude,codex" {
		t.Fatalf("fill: %+v %v", got, err)
	}
	if got.Hooks == nil || !*got.Hooks || got.AgentNotify == nil || !*got.AgentNotify {
		t.Fatalf("units: hooks=%v notify=%v", got.Hooks, got.AgentNotify)
	}
	if !strings.Contains(out.String(), "Claude Code") || strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("prompt text: %s", out.String())
	}
	if !strings.Contains(out.String(), "Units:") || !strings.Contains(out.String(), "Agent-initiated notify") {
		t.Fatalf("units prompt: %s", out.String())
	}
}

func TestFillInteractiveSelectsNotifyOnly(t *testing.T) {
	in := strings.NewReader("1\n2\n")
	var out strings.Builder
	got, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out}, nil)
	if err != nil || strings.Join(got.Agents, ",") != "claude" || got.Hooks == nil || *got.Hooks || got.AgentNotify == nil || !*got.AgentNotify {
		t.Fatalf("notify-only: %+v %v", got, err)
	}
}

func TestFillInteractiveEmptyUnitsCancels(t *testing.T) {
	in := strings.NewReader("1\n\n")
	var out strings.Builder
	_, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out}, nil)
	if err != ErrPromptCanceled {
		t.Fatalf("cancel units: %v", err)
	}
}

func TestFillInteractiveSkipsUnitsWhenFlagsSet(t *testing.T) {
	off, on := false, true
	got, err := FillInteractive(promptCtx(t), Request{
		Action: ActionInstall, Agents: []string{"codex"}, Hooks: &on, AgentNotify: &off,
	}, &LinePrompt{In: strings.NewReader(""), Out: io.Discard}, nil)
	if err != nil || got.Yes || got.Hooks == nil || !*got.Hooks || got.AgentNotify == nil || *got.AgentNotify {
		t.Fatalf("preset units: %+v %v", got, err)
	}
}

func TestFillInteractiveCancelIsEmptySelection(t *testing.T) {
	in := strings.NewReader("\n")
	var out strings.Builder
	_, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out}, nil)
	if err != ErrPromptCanceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestFillInteractiveEOF(t *testing.T) {
	in := strings.NewReader("")
	var out strings.Builder
	_, err := FillInteractive(promptCtx(t), Request{Action: ActionInstall}, &LinePrompt{In: in, Out: &out}, nil)
	if err != ErrPromptInputClosed {
		t.Fatalf("eof: %v", err)
	}
}

func TestFillInteractiveLeavesInspectAlone(t *testing.T) {
	got, err := FillInteractive(promptCtx(t), Request{Action: ActionInspect, Agents: []string{"codex"}}, &LinePrompt{In: strings.NewReader("1\n"), Out: io.Discard}, nil)
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
	_, err := FillInteractive(context.Background(), Request{Action: ActionInstall}, nil, nil)
	if err != ErrPromptUnavailable {
		t.Fatalf("nil: %v", err)
	}
}

func TestFillInteractiveOmitsActionInstallsNew(t *testing.T) {
	in := strings.NewReader("2\n3\n")
	var out strings.Builder
	got, err := FillInteractive(promptCtx(t), Request{}, &LinePrompt{In: in, Out: &out}, nil)
	if err != nil || got.Action != ActionInstall || strings.Join(got.Agents, ",") != "codex" || got.Yes {
		t.Fatalf("new: %+v %v", got, err)
	}
	if strings.Contains(out.String(), "Existing agent-notify") {
		t.Fatalf("new install prompted existing action: %s", out.String())
	}
}

func TestFillInteractiveExistingSelectsInspect(t *testing.T) {
	in := strings.NewReader("1\n")
	var out strings.Builder
	got, err := FillInteractive(promptCtx(t), Request{Agents: []string{"codex"}}, &LinePrompt{In: in, Out: &out}, func([]string) []string {
		return []string{"codex"}
	})
	if err != nil || got.Action != ActionInspect || got.Yes {
		t.Fatalf("inspect: %+v %v", got, err)
	}
	if !strings.Contains(out.String(), "Existing agent-notify") || strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("inspect prompt: %s", out.String())
	}
}

func TestFillInteractiveExistingSelectsUninstall(t *testing.T) {
	in := strings.NewReader("3\n3\n")
	got, err := FillInteractive(promptCtx(t), Request{Agents: []string{"claude"}}, &LinePrompt{In: in, Out: io.Discard}, func([]string) []string {
		return []string{"claude"}
	})
	if err != nil || got.Action != ActionUninstall || got.Yes {
		t.Fatalf("uninstall: %+v %v", got, err)
	}
}

func TestConfirmPlanShowsMixedPerClientFlags(t *testing.T) {
	off, on := false, true
	got := confirmPlan(Request{
		Action: ActionInstall, Agents: []string{"claude", "codex"},
		Hooks: &off, ClaudeAgentNotify: &off, CodexAgentNotify: &on,
	})
	if !strings.Contains(got, "agents=claude,codex") || !strings.Contains(got, "hooks=false") || !strings.Contains(got, "claude-agent-notify=false") || !strings.Contains(got, "codex-agent-notify=true") {
		t.Fatalf("mixed plan: %s", got)
	}
}

func TestConfirmPlanShowsProfilesRevisionAndRequiredActions(t *testing.T) {
	got := confirmPlan(Request{
		Action: ActionInstall, Agents: []string{"codex"},
		CodexHome: "/tmp/codex", ReleaseVersion: "1.44.0", PackageSHA256: "abc",
	})
	if !strings.Contains(got, "codex-profile=/tmp/codex") || !strings.Contains(got, "revision=1.44.0") || !strings.Contains(got, "digest=abc") {
		t.Fatalf("identity: %s", got)
	}
	if !strings.Contains(got, "required=restart,request-permission,test-notification") || !strings.Contains(got, "permission-dialog=explicit") {
		t.Fatalf("required actions: %s", got)
	}
	uninstall := confirmPlan(Request{Action: ActionUninstall, Agents: []string{"codex"}})
	if !strings.Contains(uninstall, "permission-dialog=skipped") {
		t.Fatalf("uninstall plan: %s", uninstall)
	}
}

func TestFillInteractiveShowsDiscoverCapabilities(t *testing.T) {
	in := strings.NewReader("1\n3\n")
	var out strings.Builder
	got, err := FillInteractive(promptCtx(t), Request{
		Action: ActionInstall,
		DiscoverAgents: func() []AgentCapability {
			return []AgentCapability{
				{ID: "claude", Present: true, Path: "/bin/claude", Bound: true},
				{ID: "codex", Present: false},
			}
		},
	}, &LinePrompt{In: in, Out: &out}, nil)
	if err != nil || strings.Join(got.Agents, ",") != "claude" {
		t.Fatalf("discover picker: %+v %v", got, err)
	}
	if !strings.Contains(out.String(), "Claude Code (executable present, installed)") || !strings.Contains(out.String(), "Codex (executable not found)") {
		t.Fatalf("capability labels: %s", out.String())
	}
}
