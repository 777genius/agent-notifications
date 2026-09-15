//go:build windows

// ABOUTME: Windows notification path — go-toast with protocol activation so a
// ABOUTME: notification click relaunches the binary and raises the terminal window.
package notifier

import (
	"fmt"
	"runtime"
	"strings"

	toast "git.sr.ht/~jackmordaunt/go-toast"

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

	n := toast.Notification{
		AppID: windowsToastAppID,
		Title: title,
		Body:  body,
	}
	if appIcon != "" {
		n.Icon = appIcon
	}

	if ctx, ok := winfocus.CaptureFocusContext(cwd); ok && ctx.HasTarget() {
		n.ActivationType = toast.Protocol
		n.ActivationArguments = ctx.EncodeURI()
		logging.Debug("Windows toast click-to-focus target: %+v", ctx)
	} else {
		logging.Debug("Windows toast: no focus target captured, sending plain toast")
	}

	// go-toast initializes WinRT COM on the current OS thread; pin the call so
	// the COM init and Show run on the same apartment (mirrors the beeep path).
	runtime.LockOSThread()
	err := n.Push()
	runtime.UnlockOSThread()

	if toastDeliveredDespiteError(err) {
		if err != nil {
			logging.Debug("Windows toast delivered via go-toast PowerShell fallback (benign error: %v)", err)
		}
		return nil
	}
	return err
}

// toastDeliveredDespiteError reports whether a non-nil go-toast error still
// corresponds to a delivered notification. go-toast attempts the WinRT COM path
// first and silently falls back to a PowerShell script; when COM fails it still
// delivers via PowerShell but returns the COM error (commonly "doc.LoadXml(tmpl)").
// Treating that as delivered avoids a duplicate beeep toast. See the same
// documented false-positive handling in notifier.go / docs/troubleshooting.md.
func toastDeliveredDespiteError(err error) bool {
	if err == nil {
		return true
	}
	return strings.Contains(err.Error(), "doc.LoadXml(tmpl)")
}
