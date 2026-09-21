//go:build windows

// ABOUTME: Windows notification path with protocol activation so a
// ABOUTME: notification click relaunches the binary and raises the terminal window.
package notifier

import (
	"context"
	"fmt"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/winfocus"
)

// windowsToastAppID is the AppID shown in Action Center. Kept fixed (matching
// the beeep path) to avoid registry pollution — see issue #4.
const windowsToastAppID = "Claude Code Notifications"

// --- macOS-only helpers: stubs so the notifier package compiles on Windows ---

// GetTerminalBundleID returns empty string on Windows (macOS-only concept).
func GetTerminalBundleID(configOverride string) string { return "" }

// GetTerminalNotifierPath returns an error on Windows (terminal-notifier is macOS-only).
func GetTerminalNotifierPath() (string, error) {
	return "", fmt.Errorf("terminal-notifier is only available on macOS")
}

// IsTerminalNotifierAvailable returns false on Windows.
func IsTerminalNotifierAvailable() bool { return false }

// EnsureClaudeNotificationsApp is a no-op on Windows.
func EnsureClaudeNotificationsApp() error { return nil }

// --- Linux daemon helpers: stubs so the notifier package compiles on Windows ---

// sendLinuxNotification returns an error on Windows (the Linux daemon is Linux-only).
func sendLinuxNotification(title, body, appIcon string, cfg *config.Config, cwd string) error {
	return fmt.Errorf("Linux notifications not available on Windows")
}

// IsDaemonAvailable returns false on Windows.
func IsDaemonAvailable() bool { return false }

// StartDaemon is a no-op on Windows.
func StartDaemon() bool { return false }

// StopDaemon is a no-op on Windows.
func StopDaemon() error { return nil }

// sendWindowsNotification shows a Windows toast with protocol-activation
// click-to-focus. At notify time it captures the originating terminal window and
// encodes it into the toast's activation URI; clicking the toast relaunches this
// binary's focus-windows subcommand, which raises that window.
func sendWindowsNotification(title, body, appIcon string, cfg *config.Config, cwd string) error {
	// Register the click handler under HKCU (idempotent; refreshes if the binary
	// moved). Non-fatal — the toast still shows without a working handler.
	if err := winfocus.EnsureRegistered(); err != nil {
		logging.Debug("focus protocol registration failed: %v", err)
	}

	n := windowsToastPayload{
		AppID: windowsToastAppID,
		Title: title,
		Body:  body,
		Icon:  appIcon,
	}

	if ctx, ok := winfocus.CaptureFocusContext(cwd); ok && ctx.HasTarget() {
		n.ActivationType = "protocol"
		n.ActivationArguments = ctx.EncodeURI()
		logging.Debug("Windows toast click-to-focus target: %+v", ctx)
	} else {
		logging.Debug("Windows toast: no focus target captured, sending plain toast")
	}

	// Bound the PowerShell WinRT handoff so a broken desktop session cannot
	// block the caller indefinitely.
	operation, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return submitWindowsToast(operation, n)
}
