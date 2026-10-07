package notifier

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// IsTmux returns true if the current process is running inside a tmux session.
func IsTmux() bool {
	return os.Getenv("TMUX") != ""
}

// getTmuxSocketPath extracts the tmux socket path from the TMUX env var.
// TMUX format: "/private/tmp/tmux-501/default,12345,0"
func getTmuxSocketPath() string {
	tmuxEnv := os.Getenv("TMUX")
	if tmuxEnv == "" {
		return ""
	}
	// Socket path is everything before the first comma
	if idx := strings.IndexByte(tmuxEnv, ','); idx > 0 {
		return tmuxEnv[:idx]
	}
	return tmuxEnv
}

// getTmuxPath returns the absolute path to the tmux binary.
// ClaudeNotifier.app runs without the user's PATH, so we need the full path.
func getTmuxPath() string {
	if path, err := exec.LookPath("tmux"); err == nil {
		return path
	}
	return "tmux"
}

// GetTmuxPaneTarget returns the tmux pane ID (e.g. "%42") of the pane where
// Claude Code is running, for use with tmux select-pane / select-window commands.
//
// Prefers $TMUX_PANE (set by tmux per-pane at creation, always points to the
// process's own pane) over "tmux display-message" (which returns the currently
// active pane and may be wrong if the user switched tabs).
func GetTmuxPaneTarget() (string, error) {
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		return pane, nil
	}

	// Best-effort fallback for environments where TMUX_PANE is not available.
	// Use the resolved tmux binary and explicit socket so this still works when
	// the notifier runs outside the user's shell PATH.
	tmuxPath := getTmuxPath()
	socketPath := getTmuxSocketPath()

	var cmd *exec.Cmd
	if socketPath != "" {
		cmd = exec.Command(tmuxPath, "-S", socketPath, "display-message", "-p", "#{pane_id}")
	} else {
		cmd = exec.Command(tmuxPath, "display-message", "-p", "#{pane_id}")
	}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tmux pane target: %w", err)
	}
	target := strings.TrimSpace(string(output))
	if target == "" {
		return "", fmt.Errorf("empty tmux pane target")
	}
	if !strings.HasPrefix(target, "%") {
		return "", fmt.Errorf("invalid tmux pane target: %q", target)
	}
	return target, nil
}

// IsTmuxControlMode returns true if any tmux client attached to the current
// server is running in control mode (-CC), typically used by iTerm2.
// In control mode, standard tmux select-window doesn't cause iTerm2 to
// switch tabs; the iTerm2 Python API must be used instead.
//
// Uses list-clients (not display-message) because display-message evaluates
// #{client_control_mode} for the temporary command-line client we spawn,
// which is never in control mode. list-clients enumerates all persistent
// (attached) clients, so we can check if ANY is in control mode.
func IsTmuxControlMode() bool {
	if !IsTmux() {
		return false
	}
	tmuxPath := getTmuxPath()
	socketPath := getTmuxSocketPath()

	var cmd *exec.Cmd
	if socketPath != "" {
		cmd = exec.Command(tmuxPath, "-S", socketPath,
			"list-clients", "-F", "#{client_control_mode}")
	} else {
		cmd = exec.Command(tmuxPath,
			"list-clients", "-F", "#{client_control_mode}")
	}

	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "1" {
			return true
		}
	}
	return false
}

// buildTmuxNotifierArgs constructs command-line arguments for terminal-notifier
// when running inside tmux. Uses both -activate (to focus the terminal app)
// and -execute (to switch to the correct tmux session/window/pane) on click.
func buildTmuxNotifierArgs(title, message, paneTarget, bundleID string) []string {
	// Use absolute path to tmux and explicit socket — ClaudeNotifier.app
	// runs without the user's shell PATH, so bare "tmux" won't be found.
	tmuxPath := getTmuxPath()
	socketPath := getTmuxSocketPath()

	// switch-client is a separate command so its failure (e.g. no attached
	// clients) doesn't abort the select-window/select-pane chain.
	var tmuxCmd string
	if socketPath != "" {
		tmuxCmd = fmt.Sprintf(
			"'%s' -S '%s' switch-client -t '%s' 2>/dev/null; '%s' -S '%s' select-window -t '%s' \\; select-pane -t '%s'",
			tmuxPath, socketPath, paneTarget,
			tmuxPath, socketPath, paneTarget, paneTarget,
		)
	} else {
		tmuxCmd = fmt.Sprintf(
			"'%s' switch-client -t '%s' 2>/dev/null; '%s' select-window -t '%s' \\; select-pane -t '%s'",
			tmuxPath, paneTarget,
			tmuxPath, paneTarget, paneTarget,
		)
	}

	return []string{
		"-title", title,
		"-message", message,
		"-activate", bundleID,
		"-execute", tmuxCmd,
	}
}
