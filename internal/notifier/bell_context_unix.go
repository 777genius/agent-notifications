//go:build !windows

package notifier

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/777genius/agent-notifications/internal/commandrun"
)

func sendTerminalBellContext(ctx context.Context) {
	if writeContextBell(ctx, "/dev/tty") {
		return
	}
	pane := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || pane == "" || ctx.Err() != nil {
		return
	}
	out, err := commandrun.Output(ctx, exec.Command(getTmuxPath(), "display-message", "-p", "-t", pane, "#{pane_tty}"))
	if err == nil {
		_ = writeContextBell(ctx, strings.TrimSpace(string(out)))
	}
}
func writeContextBell(ctx context.Context, path string) bool {
	if ctx.Err() != nil || !filepath.IsAbs(path) {
		return false
	}
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer func(f *os.File) { _ = f.Close() }(f)
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 || ctx.Err() != nil {
		return false
	}
	_, err = f.Write([]byte{'\a'})
	return err == nil
}
