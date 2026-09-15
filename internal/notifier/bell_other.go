//go:build !windows

// ABOUTME: terminal-bell delivery on Unix-likes — write BEL to /dev/tty, with a
// ABOUTME: tmux pane-tty fallback for hook subprocesses that lack a controlling tty.
package notifier

import (
	"os"
	"strings"

	"github.com/777genius/agent-notifications/internal/logging"
)

// sendTerminalBell writes a BEL character to /dev/tty to trigger terminal
// tab indicators (e.g. Ghostty tab highlight, tmux window bell flag).
//
// When the hook subprocess has no controlling tty (notably Claude Code hooks,
// which detach from the parent terminal), /dev/tty open fails with ENXIO.
// In that case we fall back to writing BEL into the tmux pane's tty directly,
// using $TMUX_PANE to locate the pane and `tmux display-message` to resolve
// its tty path. Tmux reads the BEL from the pty and sets the window's bell
// flag, so tab indicators (window-status-bell-style) still light up.
func sendTerminalBell() {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		logging.Debug("Could not open /dev/tty for bell: %v", err)
		sendTmuxPaneBell()
		return
	}
	defer func() { _ = f.Close() }()

	// Fall back to the tmux pane tty if the write fails too, not just on open.
	if _, err := f.Write([]byte("\a")); err != nil {
		logging.Debug("Could not write BEL to /dev/tty: %v", err)
		sendTmuxPaneBell()
	}
}

// sendTmuxPaneBell writes a BEL byte to the current tmux pane's tty as a
// fallback path for environments without a controlling tty (e.g. Claude Code
// hook subprocesses). No-op when not running under tmux.
func sendTmuxPaneBell() {
	paneID := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || paneID == "" {
		return
	}

	out, err := execCommand("tmux", "display-message", "-p", "-t", paneID, "#{pane_tty}").Output()
	if err != nil {
		logging.Debug("tmux display-message failed for bell fallback: %v", err)
		return
	}

	paneTTY := strings.TrimSpace(string(out))
	if paneTTY == "" {
		return
	}

	f, err := os.OpenFile(paneTTY, os.O_WRONLY, 0)
	if err != nil {
		logging.Debug("Could not open tmux pane tty %s for bell: %v", paneTTY, err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write([]byte("\a"))
}
