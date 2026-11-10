package notifier

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/audio"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
)

const macOSPermissionDeniedMessage = "Notification permission denied. Enable in System Settings > Notifications."

const legacyTerminalNotifierTimeout = 30 * time.Second
const legacyTerminalNotifierWaitDelay = time.Second

var execCommand = exec.Command
var runLegacyNotifierCommand = runCommandWithTimeout
var beeepNotify = beeep.Notify
var notifierGOOS = runtime.GOOS
var uniqueNotificationGroupSeq atomic.Uint64

const notificationGroupPrefix = "claude-notif-"

// NotificationPermissionDeniedError indicates macOS rejected the native
// ClaudeNotifier path because notification permission is denied for the app.
type NotificationPermissionDeniedError struct {
	Details string
}

func (e *NotificationPermissionDeniedError) Error() string {
	if strings.TrimSpace(e.Details) == "" {
		return macOSPermissionDeniedMessage
	}
	return e.Details
}

// Notifier sends desktop notifications
type Notifier struct {
	cfg         *config.Config
	audioPlayer *audio.Player
	playerInit  sync.Once
	playerErr   error
	mu          sync.Mutex
	wg          sync.WaitGroup
	closing     bool // Prevents new sounds from being enqueued after Close() is called
}

// New creates a new notifier
func New(cfg *config.Config) *Notifier {
	return &Notifier{
		cfg: cfg,
	}
}

// isTimeSensitiveStatus returns true for statuses that should break through Focus Mode
func isTimeSensitiveStatus(status analyzer.Status) bool {
	switch status {
	case analyzer.StatusAPIError, analyzer.StatusAPIErrorOverloaded, analyzer.StatusSessionLimitReached,
		analyzer.StatusPermissionRequest:
		return true
	default:
		return false
	}
}

// SendDesktop sends a desktop notification.
// On macOS, it always prefers ClaudeNotifier/terminal-notifier to avoid
// Script Editor attribution and optionally enables click-to-focus.
// On Linux with clickToFocus enabled, it uses the background daemon.
// cwd is the working directory of the project; used for window-specific focus. May be empty.
//
// Passing WithoutSound() delivers the banner without the plugin's own audio cue
// (used when the desktop is in Do Not Disturb).
func (n *Notifier) SendDesktop(status analyzer.Status, message, sessionID, cwd string, opts ...SendOption) error {
	sendOpts := resolveSendOptions(opts)
	focusEnabled := n.cfg.Notifications.Desktop.ClickToFocus && !sendOpts.noFocus

	// Send terminal bell for terminal tab indicators (e.g. Ghostty, tmux,
	// Windows Terminal). Platform-specific; see bell_other.go / bell_windows.go.
	// A terminal bell bypasses macOS Focus, even when the notification is quiet.
	if !platform.IsMacOS() && n.cfg.IsTerminalBellEnabled() {
		if sendOpts.executionContext != nil {
			sendTerminalBellContext(sendOpts.executionContext)
		} else {
			sendTerminalBell()
		}
	}

	if !n.cfg.IsDesktopEnabled() {
		logging.Debug("Desktop notifications disabled, skipping")
		return nil
	}

	statusInfo, exists := n.cfg.GetStatusInfo(string(status))
	if !exists {
		return fmt.Errorf("unknown status: %s", status)
	}

	presentation := legacyPresentation(status, message, statusInfo.Title, n.cfg.IsSessionLabelEnabled())
	if sendOpts.hookPresentation != nil {
		presentation = hookPresentation(status, *sendOpts.hookPresentation, statusInfo.Title, n.cfg.IsSessionLabelEnabled())
	}
	title, cleanMessage, subtitle := presentation.Title, presentation.Body, presentation.Subtitle

	// Get app icon path if configured
	appIcon := n.cfg.Notifications.Desktop.AppIcon
	if appIcon != "" && !platform.FileExists(appIcon) {
		logging.Warn("App icon not found: %s, using default", appIcon)
		appIcon = ""
	}
	// Strict local terminal actions are separate from the historical
	// heuristic focus routes. A target is supplied only by a verified
	// installed CLI composer, never from a notification body or session ID.
	if sendOpts.terminal != nil {
		if focusEnabled && platform.IsLinux() {
			if sendOpts.hookPresentation != nil && subtitle != "" {
				cleanMessage = subtitle + "\n" + cleanMessage
			}
			return n.sendVerifiedTerminal(title, cleanMessage, appIcon, statusInfo.Sound, sendOpts)
		}
		focusEnabled = false    // unqualified platform: informational only
		sendOpts.noFocus = true // context-backed macOS notifications stay action-free
	}
	if sendOpts.executionContext != nil {
		return n.sendNativeDesktopContext(sendOpts.executionContext, statusInfo.Sound, title, cleanMessage, subtitle, sessionID, cwd, appIcon, sendOpts)
	}

	// macOS: prefer ClaudeNotifier/terminal-notifier so the common path keeps the
	// native app attribution. If that fails, fall back to beeep as a delivery
	// safety net rather than dropping the notification entirely.
	if platform.IsMacOS() {
		if IsTerminalNotifierAvailable() {
			if err := n.sendWithTerminalNotifier(
				title,
				cleanMessage,
				subtitle,
				sessionID,
				false, // Ordinary delivery must respect Focus for every status.
				cwd,
				focusEnabled,
				n.nativeNotificationSound(statusInfo.Sound, sendOpts),
			); err != nil {
				var permissionErr *NotificationPermissionDeniedError
				if errors.As(err, &permissionErr) {
					logging.Warn("ClaudeNotifier permission denied on macOS: %v", err)
					return err
				}
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay) {
					// The helper may already have delivered before its completion
					// became uncertain. Retrying could display a duplicate banner.
					return err
				}
				logging.Warn("ClaudeNotifier failed on macOS, falling back to beeep: %v", err)
			} else {
				logging.Debug("Desktop notification sent via ClaudeNotifier/terminal-notifier: title=%s", title)
				return nil
			}
		} else {
			logging.Warn("ClaudeNotifier not available on macOS, falling back to beeep (run /claude-notifications-go:init to install it)")
		}
	}

	// The remaining backends only display title and body. Structured hooks
	// carry question identity in the subtitle, so preserve that literal context
	// here after the native macOS path has had its clean three-field layout.
	if sendOpts.hookPresentation != nil && subtitle != "" {
		cleanMessage = subtitle + "\n" + cleanMessage
	}

	// Linux: Try daemon for click-to-focus support
	if platform.IsLinux() && focusEnabled {
		if err := sendLinuxNotification(title, cleanMessage, appIcon, n.cfg, cwd); err != nil {
			logging.Warn("Linux daemon notification failed, falling back to beeep: %v", err)
			// Fall through to beeep
		} else {
			logging.Debug("Desktop notification sent via Linux daemon: title=%s", title)
			n.playSoundUnlessMuted(statusInfo.Sound, sendOpts)
			return nil
		}
	}

	// Windows: use a bounded WinRT toast with protocol activation for click-to-focus support.
	if platform.IsWindows() && focusEnabled {
		if err := sendWindowsNotification(title, cleanMessage, appIcon, n.cfg, cwd); err != nil {
			logging.Warn("Windows click-to-focus notification failed, falling back to beeep: %v", err)
			// Fall through to beeep
		} else {
			logging.Debug("Desktop notification sent via Windows click-to-focus: title=%s", title)
			n.playSoundUnlessMuted(statusInfo.Sound, sendOpts)
			return nil
		}
	}

	// Standard path: beeep (Windows, Linux fallback)
	return n.sendWithBeeep(title, cleanMessage, appIcon, statusInfo.Sound, sendOpts)
}
