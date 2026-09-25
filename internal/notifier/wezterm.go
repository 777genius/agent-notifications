package notifier

import (
	"fmt"
	"os"
	"os/exec"
)

// IsWezTerm returns true if the current process is running inside WezTerm.
func IsWezTerm() bool {
	return os.Getenv("WEZTERM_PANE") != ""
}

// getWezTermPath returns the absolute path to the wezterm binary.
// ClaudeNotifier.app runs without the user's PATH, so we need the full path.
func getWezTermPath() string {
	if path, err := exec.LookPath("wezterm"); err == nil {
		return path
	}
	return "wezterm"
}

// GetWezTermPaneTarget returns the pane ID and unix socket path from environment variables.
// No external commands needed — the pane ID is already available in $WEZTERM_PANE.
func GetWezTermPaneTarget() (paneID, socketPath string, err error) {
	paneID = os.Getenv("WEZTERM_PANE")
	if paneID == "" {
		return "", "", fmt.Errorf("WEZTERM_PANE not set")
	}
	socketPath = os.Getenv("WEZTERM_UNIX_SOCKET")
	return paneID, socketPath, nil
}

// buildWezTermNotifierArgs constructs command-line arguments for terminal-notifier
// when running inside WezTerm. Uses -activate (to focus the terminal app)
// and -execute (to switch to the correct WezTerm pane) on click.
func buildWezTermNotifierArgs(title, message, paneID, socketPath, bundleID string) []string {
	weztermPath := getWezTermPath()

	var executeCmd string
	if socketPath != "" {
		executeCmd = fmt.Sprintf(
			"'%s' cli activate-pane --pane-id %s --unix-socket '%s'",
			weztermPath, paneID, socketPath,
		)
	} else {
		executeCmd = fmt.Sprintf(
			"'%s' cli activate-pane --pane-id %s",
			weztermPath, paneID,
		)
	}

	return []string{
		"-title", title,
		"-message", message,
		"-activate", bundleID,
		"-execute", executeCmd,
	}
}
