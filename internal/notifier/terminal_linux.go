//go:build linux

// ABOUTME: Linux-specific notification handling with click-to-focus support.
// ABOUTME: Uses background daemon for persistent D-Bus connection when click-to-focus is enabled.
package notifier

import (
	"fmt"
	"os"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/warpfocus"
	"github.com/gen2brain/beeep"
)

// macOS stub functions - these are not used on Linux but required for compilation

// GetTerminalBundleID returns empty string on Linux
// as terminal bundle IDs are a macOS-specific concept.
func GetTerminalBundleID(configOverride string) string {
	return ""
}

// GetTerminalNotifierPath returns an error on Linux
// as terminal-notifier is macOS-only.
func GetTerminalNotifierPath() (string, error) {
	return "", fmt.Errorf("terminal-notifier is only available on macOS")
}

// IsTerminalNotifierAvailable returns false on Linux.
func IsTerminalNotifierAvailable() bool {
	return false
}

// EnsureClaudeNotificationsApp is a no-op on Linux.
func EnsureClaudeNotificationsApp() error {
	return nil
}

// sendWindowsNotification is a stub on Linux (the real implementation lives in
// terminal_windows.go).
func sendWindowsNotification(title, body, appIcon string, cfg *config.Config, cwd string) error {
	return fmt.Errorf("windows notifications are only available on windows")
}

// sendLinuxNotification sends a notification on Linux.
// When clickToFocus is enabled, uses the daemon for click-to-focus support.
// Falls back to beeep when daemon is unavailable.
// cwd is the working directory of the project; used for window-specific focus. May be empty.
func sendLinuxNotification(title, body, appIcon string, cfg *config.Config, cwd string) error {
	// If click-to-focus is disabled, use beeep directly
	if !cfg.Notifications.Desktop.ClickToFocus {
		logging.Debug("Click-to-focus disabled, using beeep directly")
		return beeep.Notify(title, body, appIcon)
	}

	// Try to use daemon for click-to-focus
	if err := sendViaDaemon(title, body, appIcon, cwd, cfg); err == nil {
		logging.Debug("Notification sent via daemon with click-to-focus support")
		return nil
	} else {
		logging.Debug("Daemon not available (%v), falling back to beeep", err)
	}

	// Fallback to beeep (no click-to-focus)
	return beeep.Notify(title, body, appIcon)
}

// sendViaDaemon sends a notification via the background daemon.
// Returns an error if daemon is not available or fails.
// cwd is used to extract the project folder name for window-specific focus.
func sendViaDaemon(title, body, appIcon, cwd string, cfg *config.Config) error {
	// Start daemon on-demand (no-op if already running)
	if !daemon.StartDaemonOnDemand() {
		return daemon.ErrDaemonNotAvailable
	}

	// Create client and send notification
	client, err := daemon.NewClient()
	if err != nil {
		return err
	}

	// Send notification with 30 second timeout.
	// Detect focus target in the hook process (not the daemon), since the daemon may
	// have been started from a different environment.
	focusTarget := daemon.GetTerminalName()
	// Folder name for title-based window focus
	folderName := daemon.GetFocusFolderName(focusTarget, cwd)
	focusWindowID := daemon.GetX11WindowID()
	focusWindowTitle := daemon.GetExactWindowTitle(focusTarget)
	if sessionType := os.Getenv("XDG_SESSION_TYPE"); sessionType != "" && sessionType != "x11" {
		focusWindowID = ""
	}

	// Capture WezTerm pane info only when the focus target is actually WezTerm.
	wezTermPaneID, wezTermSocket := daemon.GetWezTermFocusHints(focusTarget)
	warpFocusURL := warpfocus.FromEnv()

	hints := daemon.FocusHints{
		TerminalName:  focusTarget,
		FolderName:    folderName,
		WindowID:      focusWindowID,
		WindowTitle:   focusWindowTitle,
		WezTermPaneID: wezTermPaneID,
		WezTermSocket: wezTermSocket,
		WarpFocusURL:  warpFocusURL,
	}

	// Zellij is not tied to a focus target the way WezTerm is: it runs inside
	// whichever terminal the user launched it from, so it is consulted for
	// every terminal.
	applyZellijFocusHints(cfg, &hints)

	_, err = client.SendNotification(title, body, appIcon, hints, 30)
	return err
}

// IsDaemonAvailable checks if the notification daemon is available and running.
// Exported for testing and status checks.
func IsDaemonAvailable() bool {
	return daemon.IsDaemonRunning()
}

// StartDaemon starts the notification daemon on-demand.
// Returns true if daemon started successfully or was already running.
func StartDaemon() bool {
	return daemon.StartDaemonOnDemand()
}

// StopDaemon stops the running notification daemon.
func StopDaemon() error {
	return daemon.StopDaemon()
}
