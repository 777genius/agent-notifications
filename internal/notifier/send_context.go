package notifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/commandrun"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
)

// ErrDesktopSuppressed identifies a deliberate no-effect policy decision.
var ErrDesktopSuppressed = notification.ErrSuppressed

var runContextSound = commandrun.Run

// SendDesktopContext uses the existing presentation, focus scripts, daemon,
// native helper and configured sound. It adds one caller-owned budget and does
// not fall back after a possible native handoff. Nil means hook handoff only,
// never visible/audible notification or successful future click.
func (n *Notifier) SendDesktopContext(ctx context.Context, status analyzer.Status, message, sessionID, cwd string, opts ...SendOption) error {
	if ctx == nil {
		return errors.New("invalid_context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n == nil || n.cfg == nil {
		return errors.New("invalid_config")
	}
	if !n.cfg.IsStatusDesktopEnabled(string(status)) {
		return ErrDesktopSuppressed
	}
	if delay := n.cfg.GetNotifyDelaySeconds(); delay > 0 {
		timer := time.NewTimer(time.Duration(delay) * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if n.cfg.ShouldNotifyOnlyWhenUnfocused() && terminalHasFocusContext(ctx, sessionID, cwd) {
		return ErrDesktopSuppressed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	muted := false
	if mode := n.cfg.GetDoNotDisturbMode(); mode != config.DNDModeOff && desktopDNDContext(ctx) {
		if mode == config.DNDModeSuppress {
			return ErrDesktopSuppressed
		}
		muted = true
	}
	if !muted && n.cfg.ShouldRespectDisplaySleep() && IsDisplayAsleep() {
		muted = true
	}
	if muted {
		opts = append(opts, WithoutSound())
	}
	opts = append(opts, func(o *sendOptions) { o.executionContext = ctx })
	if err := ctx.Err(); err != nil {
		return err
	}
	err := n.SendDesktop(status, message, sessionID, cwd, opts...)
	if err == nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// HasLocalTerminal reads only native process context. A Pi mode value or
// caller-supplied cwd is never sufficient to authorize local desktop routing.
func HasLocalTerminal() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return HasLocalTerminalContext(ctx)
}
func HasLocalTerminalContext(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(key) != "" {
			return false
		}
	}
	return hasNativeTerminal(ctx)
}

func (n *Notifier) playSoundContext(ctx context.Context, sound string, opts sendOptions) error {
	if opts.muteSound || !n.cfg.Notifications.Desktop.Sound || n.cfg.Notifications.Desktop.Volume <= 0 || sound == "" {
		return nil
	}
	info, err := os.Stat(sound)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"play-sound", sound, "--volume", strconv.FormatFloat(n.cfg.Notifications.Desktop.Volume, 'f', -1, 64)}
	if device := n.cfg.Notifications.Desktop.AudioDevice; device != "" {
		args = append(args, "--device", device)
	}
	return runContextSound(ctx, exec.Command(exe, args...))
}

func validInstalledRuntime(opts sendOptions) bool {
	return filepath.IsAbs(opts.runtimeRoot) && filepath.Clean(opts.runtimeRoot) == opts.runtimeRoot
}
