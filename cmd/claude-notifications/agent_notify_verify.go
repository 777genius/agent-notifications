package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
)

var errAgentNotifyApplication = errors.New("application_identity_invalid")

// Only explicit installer composition calls this verifier. It never launches the
// selected application; the team is operator policy, not a model argument.
func verifyAgentNotifyApplication(ctx context.Context, app notifysetup.Application) error {
	if runtime.GOOS != "darwin" {
		return errAgentNotifyApplication
	}
	return verifyAgentNotifyApplicationWith(ctx, app, func(ctx context.Context, args []string) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/codesign", args...)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		cmd.WaitDelay = time.Second
		return cmd.Run()
	})
}

func verifyAgentNotifyApplicationWith(ctx context.Context, app notifysetup.Application, run func(context.Context, []string) error) error {
	if ctx == nil || run == nil || !filepath.IsAbs(app.Path) || filepath.Clean(app.Path) != app.Path || filepath.Ext(app.Path) != ".app" || len(app.Path) > 1024 || strings.ContainsAny(app.Path, "\x00\r\n") || len(app.TeamID) != 10 {
		return errAgentNotifyApplication
	}
	for _, c := range app.TeamID {
		if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return errAgentNotifyApplication
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return errAgentNotifyApplication
	}
	requirement := fmt.Sprintf(`=anchor apple generic and identifier "com.openai.codex" and certificate leaf[subject.OU] = "%s" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists`, app.TeamID)
	if err := run(ctx, []string{"--verify", "--strict", "--all-architectures", "-R", requirement, app.Path}); err != nil {
		return errAgentNotifyApplication
	}
	if ctx.Err() != nil {
		return errAgentNotifyApplication
	}
	return nil
}
