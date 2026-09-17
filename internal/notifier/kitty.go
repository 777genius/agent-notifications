package notifier

import (
	"fmt"
	"os"
	"os/exec"
)

// IsKitty returns true if the current process is running inside Kitty
// with remote control enabled. Checks both $KITTY_WINDOW_ID (always set
// in Kitty) and $KITTY_LISTEN_ON (only set when remote control is configured).
func IsKitty() bool {
	return os.Getenv("KITTY_WINDOW_ID") != "" && os.Getenv("KITTY_LISTEN_ON") != ""
}

// getKittyPath returns the absolute path to the kitten binary.
// ClaudeNotifier.app runs without the user's PATH, so we need the full path.
func getKittyPath() string {
	if path, err := exec.LookPath("kitten"); err == nil {
		return path
	}
	return "kitten"
}

// GetKittyWindowTarget returns the window ID and listen socket path from environment variables.
func GetKittyWindowTarget() (windowID, listenOn string, err error) {
	windowID = os.Getenv("KITTY_WINDOW_ID")
	if windowID == "" {
		return "", "", fmt.Errorf("KITTY_WINDOW_ID not set")
	}
	listenOn = os.Getenv("KITTY_LISTEN_ON")
	if listenOn == "" {
		return "", "", fmt.Errorf("KITTY_LISTEN_ON not set")
	}
	return windowID, listenOn, nil
}

// buildKittyNotifierArgs constructs command-line arguments for terminal-notifier
// when running inside Kitty. Uses -activate (to focus the terminal app)
// and -execute (to switch to the correct Kitty window) on click.
func buildKittyNotifierArgs(title, message, windowID, listenOn, bundleID string) []string {
	kittyPath := getKittyPath()

	executeCmd := fmt.Sprintf(
		"'%s' @ --to '%s' focus-window --match id:%s",
		kittyPath, listenOn, windowID,
	)

	return []string{
		"-title", title,
		"-message", message,
		"-activate", bundleID,
		"-execute", executeCmd,
	}
}
