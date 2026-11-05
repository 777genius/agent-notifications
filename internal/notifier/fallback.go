package notifier

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"github.com/gen2brain/beeep"

	"github.com/777genius/agent-notifications/internal/logging"
)

// SendQuickNotification sends a one-off notification without requiring a
// Notifier instance.
// executeCmd is the shell command run when the user clicks the notification (may be empty).
func SendQuickNotification(title, message, executeCmd string) error {
	if notifierPath, err := GetTerminalNotifierPath(); err == nil {
		args := []string{
			"-title", title,
			"-message", message,
		}
		if executeCmd != "" {
			args = append(args, "-execute", executeCmd)
		}
		args = append(args,
			"-group", fmt.Sprintf("claude-quick-%d", time.Now().UnixNano()),
			"-nosound",
		)
		if output, err := runLegacyNotifierCommand(legacyTerminalNotifierTimeout, notifierPath, args...); err == nil {
			return nil
		} else {
			logging.Debug("terminal-notifier failed: %v, output: %s", err, string(output))
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay) {
				// Delivery may have completed before the helper stopped answering.
				return fmt.Errorf("terminal-notifier error: %w, output: %s", err, string(output))
			}
		}
	}

	// Fallback: osascript (no click action, just informational)
	script := fmt.Sprintf(`display notification %q with title %q`, message, title)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("all notification methods failed: %w", err)
	}
	return nil
}

// sendWithBeeep sends notification via beeep (cross-platform)
func (n *Notifier) sendWithBeeep(title, message, appIcon, sound string, opts sendOptions) error {
	// Platform-specific AppName handling:
	// - Windows: Use fixed AppName to prevent registry pollution. Each unique AppName
	//   creates a persistent entry in HKEY_CURRENT_USER\SOFTWARE\Microsoft\Windows\
	//   CurrentVersion\Notifications\Settings\ that is never cleaned up.
	//   See: https://github.com/777genius/agent-notifications/issues/4
	// - macOS/Linux: Use unique AppName to prevent notification grouping/replacement,
	//   allowing multiple notifications to be displayed simultaneously.
	originalAppName := beeep.AppName
	if isNotifierWindows() {
		beeep.AppName = "Claude Code Notifications"
	} else {
		beeep.AppName = fmt.Sprintf("claude-notif-%d", time.Now().UnixNano())
	}
	defer func() {
		beeep.AppName = originalAppName
	}()

	// Send notification using beeep with proper title and clean message.
	err := notifyViaBeeep(title, message, appIcon)
	if err != nil && isWindowsToastFallbackSuccess(err) {
		logging.Warn("beeep.Notify returned a Windows toast false positive after PowerShell fallback: %v (AppName=%q, title=%q)", err, beeep.AppName, title)
		err = nil
	}
	if err != nil {
		logging.Error("beeep.Notify failed on %s: %v (AppName=%q, title=%q)", notifierGOOS, err, beeep.AppName, title)
	} else {
		logging.Debug("Desktop notification sent via beeep: title=%s", title)
	}

	// Play the sound regardless of the toast outcome. The desktop toast and the
	// audio cue are independent signals, and on Windows beeep.Notify routinely
	// returns an error even though the toast was delivered: go-toast falls back
	// to its PowerShell path when the WinRT COM call fails, but Push() still
	// returns the (joined) COM error. Gating the sound on that error therefore
	// drops the audio cue spuriously. See docs/troubleshooting.md, which already
	// documents the "doc.LoadXml(tmpl)" error as a harmless false positive.
	n.playSoundUnlessMuted(sound, opts)

	return err
}

func notifyViaBeeep(title, message, appIcon string) error {
	if isNotifierWindows() {
		// go-toast initializes WinRT COM for the current OS thread. Pin the call
		// so RoInitialize, XmlDocument creation, LoadXml, and Show execute on the
		// same initialized apartment.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	return beeepNotify(title, message, appIcon)
}

func isNotifierWindows() bool {
	return notifierGOOS == "windows"
}

func isWindowsToastFallbackSuccess(err error) bool {
	if err == nil || !isNotifierWindows() || !strings.Contains(err.Error(), "doc.LoadXml(tmpl)") {
		return false
	}

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		return false
	}

	parts := joined.Unwrap()
	return len(parts) == 1 && strings.Contains(parts[0].Error(), "doc.LoadXml(tmpl)")
}
