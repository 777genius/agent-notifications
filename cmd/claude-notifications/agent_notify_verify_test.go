package main

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
)

func TestAgentNotifyApplicationVerifierBoundary(t *testing.T) {
	app := notifysetup.Application{Path: "/test path/Codex.app", TeamID: "0123456789"}
	if runtime.GOOS == "windows" {
		app.Path = `C:\test path\Codex.app`
	}
	calls := 0
	run := func(ctx context.Context, args []string) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 90*time.Second {
			t.Fatal("missing bounded verification")
		}
		if len(args) != 6 || !strings.HasPrefix(args[4], "=anchor apple generic") || !strings.Contains(args[4], `identifier "com.openai.codex"`) || !strings.Contains(args[4], `certificate leaf[subject.OU] = "0123456789"`) || !strings.Contains(args[4], "1.2.840.113635.100.6.1.13") || !reflect.DeepEqual(args[:4], []string{"--verify", "--strict", "--all-architectures", "-R"}) || args[5] != app.Path {
			t.Fatalf("unsafe invocation: %q", args)
		}
		return nil
	}
	if err := verifyAgentNotifyApplicationWith(context.Background(), app, run); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, bad := range []notifysetup.Application{{Path: "relative.app", TeamID: app.TeamID}, {Path: app.Path, TeamID: `" OR true `}, {Path: "/x\x00.app", TeamID: app.TeamID}} {
		if err := verifyAgentNotifyApplicationWith(context.Background(), bad, run); err == nil {
			t.Fatal("accepted malformed identity")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyAgentNotifyApplicationWith(ctx, app, run); err == nil {
		t.Fatal("accepted canceled setup")
	}
	if calls != 1 {
		t.Fatal("invalid setup invoked verifier")
	}
	if err := verifyAgentNotifyApplicationWith(context.Background(), app, func(context.Context, []string) error { return errors.New("private output") }); !errors.Is(err, errAgentNotifyApplication) || err.Error() != "application_identity_invalid" {
		t.Fatal("unsanitized verification failure")
	}
}
