//go:build linux

// ABOUTME: Linux-specific notification handling with click-to-focus support.
// ABOUTME: Uses background daemon for persistent D-Bus connection when click-to-focus is enabled.
package notifier

import (
	"context"
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
// The daemon is used when focus is set (click-to-focus) or replaceKey is set
// (the session's previous notification is replaced, which needs the daemon's
// per-session notification ids). Falls back to beeep when the daemon is
// unavailable. cwd is the working directory of the project; used for
// window-specific focus. May be empty.
func sendLinuxNotification(title, body, appIcon string, cfg *config.Config, cwd string, focus bool, replaceKey string) error {
	if !focus && replaceKey == "" {
		logging.Debug("Click-to-focus disabled, using beeep directly")
		return beeep.Notify(title, body, appIcon)
	}

	if err := sendViaDaemon(title, body, appIcon, cwd, cfg, focus, replaceKey); err == nil {
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
// Without focus the notification has no click action; a non-empty replaceKey
// replaces that session's previous notification.
func sendViaDaemon(title, body, appIcon, cwd string, cfg *config.Config, focus bool, replaceKey string) error {
	// Start daemon on-demand (no-op if already running)
	if !daemon.StartDaemonOnDemand() {
		return daemon.ErrDaemonNotAvailable
	}

	// Create client and send notification
	client, err := daemon.NewClient()
	if err != nil {
		return err
	}

	opts := daemon.NotifyOptions{ReplaceKey: replaceKey, NoFocus: !focus}
	if !focus {
		_, err = client.SendNotificationWithOptionsContext(context.Background(), title, body, appIcon, daemon.FocusHints{}, 30, opts)
		return err
	}

	// Send notification with 30 second timeout.
	// Detect focus target in the hook process (not the daemon), since the daemon may
	// have been started from a different environment.
	focusTarget := daemon.GetTerminalName()
	// Folder name for title-based window focus
	folderName := daemon.GetFocusFolderName(focusTarget, cwd)
	focusWindowID := daemon.GetX11WindowID(focusTarget)
	focusWindowTitle := daemon.GetExactWindowTitle(focusTarget)
	if sessionType := os.Getenv("XDG_SESSION_TYPE"); sessionType != "" && sessionType != "x11" {
		focusWindowID = ""
	}

	// Capture WezTerm pane info only when the focus target is actually WezTerm.
	wezTermPaneID, wezTermSocket := daemon.GetWezTermFocusHints(focusTarget)
	warpFocusURL := warpfocus.FromEnv()

	// Sway names a window by the process that owns it, so the hook records its own
	// ancestry for the daemon to match against Sway's window tree.
	swaySocket, ancestorPIDs := daemon.GetSwayFocusHints()

	hints := daemon.FocusHints{
		TerminalName:  focusTarget,
		FolderName:    folderName,
		ProjectPath:   daemon.GetFocusProjectPath(focusTarget, cwd),
		IDEPID:        daemon.GetFocusIDEPID(focusTarget),
		WindowID:      focusWindowID,
		WindowTitle:   focusWindowTitle,
		WezTermPaneID: wezTermPaneID,
		WezTermSocket: wezTermSocket,
		WarpFocusURL:  warpFocusURL,
		SwaySocket:    swaySocket,
		AncestorPIDs:  ancestorPIDs,
	}

	// Zellij is not tied to a focus target the way WezTerm is: it runs inside
	// whichever terminal the user launched it from, so it is consulted for
	// every terminal.
	applyZellijFocusHints(cfg, &hints)

	_, err = client.SendNotificationWithOptionsContext(context.Background(), title, body, appIcon, hints, 30, opts)
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
