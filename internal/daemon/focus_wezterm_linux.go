//go:build linux

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// wezTermWindowTitle queries the WezTerm mux for the window title of the window
// containing paneID. Used to raise the exact WezTerm window when multiple instances
// are open (they share the same WM class and can't be distinguished via activateByWmClass).
// Returns empty string on any failure.
func wezTermWindowTitle(paneID, socketPath string) string {
	paneIDInt, err := strconv.Atoi(paneID)
	if err != nil {
		return ""
	}
	cmd := exec.Command("wezterm", "cli", "--no-auto-start", "list", "--format", "json")
	if strings.TrimSpace(socketPath) != "" {
		cmd.Env = append(os.Environ(), "WEZTERM_UNIX_SOCKET="+socketPath)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	var panes []struct {
		PaneID      int    `json:"pane_id"`
		WindowTitle string `json:"window_title"`
	}
	if err := json.Unmarshal(output, &panes); err != nil {
		return ""
	}
	for _, p := range panes {
		if p.PaneID == paneIDInt {
			return p.WindowTitle
		}
	}
	return ""
}

// TryWezTermPane activates a specific WezTerm pane by ID using the WezTerm CLI.
// This switches to the exact tab/pane where Claude is running.
// socketPath is passed via WEZTERM_UNIX_SOCKET env var (the CLI has no --unix-socket flag).
func TryWezTermPane(paneID, socketPath string) error {
	if _, err := exec.LookPath("wezterm"); err != nil {
		return fmt.Errorf("wezterm not installed")
	}

	// --no-auto-start: fail instead of spawning a new mux server when socket is wrong.
	cmd := exec.Command("wezterm", "cli", "--no-auto-start", "activate-pane", "--pane-id", paneID)
	if strings.TrimSpace(socketPath) != "" {
		cmd.Env = append(os.Environ(), "WEZTERM_UNIX_SOCKET="+socketPath)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wezterm cli activate-pane failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
