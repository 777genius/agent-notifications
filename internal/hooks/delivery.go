package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/internal/sessionname"
	"github.com/777genius/agent-notifications/internal/webhook"
)

// maxNotifyDelaySeconds bounds the configured desktop grace-period delay.
// Other hook work shares the host timeout; this is not a total execution budget.
const maxNotifyDelaySeconds = 25

// Test seams for the focus-aware / delayed desktop notification path.
var (
	isTerminalFocused = notifier.IsTerminalFocused
	isDoNotDisturb    = notifier.IsDoNotDisturb
	isDisplayAsleep   = notifier.IsDisplayAsleep
	sleepFunc         = time.Sleep
)

type notificationDelivery struct {
	webhookQueued    bool
	desktopDelivered bool
}

func (d notificationDelivery) delivered() bool {
	return d.webhookQueued || d.desktopDelivered
}

// joinMessageParts mirrors summary.appendActions: joins body and actions with a
// single space when actions is non-empty.
func joinMessageParts(body, actions string) string {
	if actions == "" {
		return body
	}
	return body + " " + actions
}

// sendNotifications sends desktop and webhook notifications and reports whether
// at least one user-visible channel was queued or delivered.
//
// body is the summary text (no metadata prefix, no action segments).
// actions is the formatted action summary (e.g. "📝 1 new  ▶ 2 cmds  ⏱ 41s") or "".
func (h *Handler) sendNotifications(status analyzer.Status, body, actions, sessionID, cwd string, onFirstDelivery func(), details ...hookNotificationDetails) notificationDelivery {
	// Add panic recovery to prevent notification failures from crashing the plugin
	defer errorhandler.HandlePanic()

	var delivery notificationDelivery

	sessionName := sessionname.GenerateSessionLabel(sessionID)
	gitBranch := platform.GetGitBranch(cwd)
	folderName := filepath.Base(cwd)

	joined := joinMessageParts(body, actions)

	// Format: "[sessionname|branch folder] message" or "[sessionname folder] message".
	// When neither the session ID nor the cwd carried any usable info (e.g. a
	// Codex event that arrived without session_id/cwd), skip the metadata block
	// entirely instead of showing a garbage "[unknown .]" prefix.
	var enhancedMessage string
	switch {
	case sessionName == "unknown" && gitBranch == "" && folderName == ".":
		enhancedMessage = joined
	case gitBranch != "":
		enhancedMessage = fmt.Sprintf("[%s|%s %s] %s", sessionName, gitBranch, folderName, joined)
	default:
		enhancedMessage = fmt.Sprintf("[%s %s] %s", sessionName, folderName, joined)
	}

	logging.Debug("Session name: %s, git branch: %s, folder: %s", sessionName, gitBranch, folderName)

	statusStr := string(status)

	// Send webhook notification first (async, check per-status enabled). Webhook
	// delivery is independent of the desktop focus/delay handling below, so the
	// notifyDelaySeconds grace period never holds it up.
	if h.cfg.IsStatusWebhookEnabled(statusStr) {
		h.webhookSvc.SendAsyncWithContext(webhook.SendContext{
			Status:        status,
			Message:       enhancedMessage,
			SessionID:     sessionID,
			CWD:           cwd,
			SessionName:   sessionName,
			GitBranch:     gitBranch,
			Folder:        folderName,
			RawBody:       body,
			ActionSummary: actions,
			AgentSource:   string(h.product),
		})
		delivery.webhookQueued = true
		if onFirstDelivery != nil {
			onFirstDelivery()
		}
	} else {
		logging.Debug("Webhook notification disabled for status: %s", statusStr)
	}

	// Send desktop notification (check per-status enabled)
	if h.cfg.IsStatusDesktopEnabled(statusStr) {
		var opts []notifier.SendOption
		if len(details) > 0 && (details[0].title != "" || details[0].question != "") {
			label := details[0].title
			if label == "" {
				label = sessionName
			}
			opts = append(opts, notifier.WithHookPresentation(notifier.HookPresentation{
				SessionName: label, Branch: gitBranch, Folder: folderName,
				Body: joined, Question: details[0].question,
			}))
		}
		// Qoder's legacy notifier may otherwise attach approximate actions.
		// A trusted local origin is bound only by the managed Qoder composer.
		if h.product == ProductQoder {
			if h.terminalFocus != nil {
				opts = append(opts, h.terminalFocus)
			} else {
				opts = append(opts, notifier.WithoutFocus())
			}
		}
		delivery.desktopDelivered = h.sendDesktopNotification(status, enhancedMessage, sessionID, cwd, opts...)
		if delivery.desktopDelivered && !delivery.webhookQueued && onFirstDelivery != nil {
			onFirstDelivery()
		}
	} else {
		logging.Debug("Desktop notification disabled for status: %s", statusStr)
	}

	return delivery
}

// sendDesktopNotification delivers the desktop notification, honoring the
// notifyDelaySeconds grace period and the notifyOnlyWhenUnfocused suppression
// from issue #93.
//
// When notifyDelaySeconds > 0 the hook waits that many seconds (bounded by
// maxNotifyDelaySeconds) before delivering, so a
// quick task you are already watching can finish before any banner appears. When
// notifyOnlyWhenUnfocused is set, the notification is dropped if the terminal
// window has OS focus at delivery time - checked after the delay, so the two
// options compose into "only notify once I have looked away". Both options are
// independent and default off; webhook delivery is unaffected.
//
// When respectDoNotDisturb is not "off", the desktop's Do Not Disturb state is
// checked at delivery time: "silent" delivers the banner without the plugin's
// sound so it still lands in the notification centre, "suppress" drops it
// entirely. Detection fails open - an unknown DND state delivers as usual.
//
// When respectDisplaySleep is set, the banner is delivered without the
// plugin's own sound while every display is asleep (macOS only; see
// docs/DO_NOT_DISTURB.md). Detection fails open the same way DND does.
//
// Webhook delivery is unaffected by any of these options.
func (h *Handler) sendDesktopNotification(status analyzer.Status, message, sessionID, cwd string, opts ...notifier.SendOption) bool {
	if delay := h.cfg.GetNotifyDelaySeconds(); delay > 0 {
		if delay > maxNotifyDelaySeconds {
			logging.Warn("notifyDelaySeconds=%d exceeds the hook timeout budget; clamping to %ds", delay, maxNotifyDelaySeconds)
			delay = maxNotifyDelaySeconds
		}
		logging.Debug("Delaying desktop notification by %ds", delay)
		sleepFunc(time.Duration(delay) * time.Second)
	}

	if h.cfg.ShouldNotifyOnlyWhenUnfocused() && isTerminalFocused(sessionID, cwd) {
		logging.Debug("Desktop notification suppressed: terminal window has focus")
		return false
	}

	// Do Not Disturb is evaluated at delivery time, after any notifyDelaySeconds
	// wait, so toggling DND during the grace period is honoured.
	sendOpts := append([]notifier.SendOption(nil), opts...)
	muted := false
	if mode := h.cfg.GetDoNotDisturbMode(); mode != config.DNDModeOff && isDoNotDisturb() {
		if mode == config.DNDModeSuppress {
			logging.Debug("Desktop notification suppressed: Do Not Disturb is active")
			return false
		}
		logging.Debug("Desktop notification muted: Do Not Disturb is active")
		sendOpts = append(sendOpts, notifier.WithoutSound())
		muted = true
	}

	// Display sleep is evaluated independently of Do Not Disturb: a machine can
	// be in DND without its display being asleep, and vice versa. Skipped once
	// DND has already muted the sound, the same cheapest-decisive-first order as
	// the focus check above, so a notification never pays for a probe whose
	// result cannot change the outcome.
	if !muted && h.cfg.ShouldRespectDisplaySleep() && isDisplayAsleep() {
		logging.Debug("Desktop notification muted: display is asleep")
		sendOpts = append(sendOpts, notifier.WithoutSound())
	}

	if err := h.notifierSvc.SendDesktop(status, message, sessionID, cwd, sendOpts...); err != nil {
		h.maybeEmitDesktopPermissionGuidance(err)
		errorhandler.HandleError(err, "Failed to send desktop notification")
		return false
	}

	return true
}

func (h *Handler) maybeEmitDesktopPermissionGuidance(err error) {
	// Observation routes (Codex) must never write to stdout; the guidance is
	// a Claude Code systemMessage feature.
	if h.product == ProductCodex || h.product == ProductQoder {
		return
	}

	if !platform.IsMacOS() {
		return
	}

	var permissionErr *notifier.NotificationPermissionDeniedError
	if !errors.As(err, &permissionErr) {
		return
	}

	stampPath, emit := h.permissionGuidanceStamp()
	if !emit {
		return
	}

	message := "[agent-notifications] macOS is blocking Agent Notifications. Open System Settings > Notifications > Agent Notifications and enable notifications. This can happen after older ad-hoc installs or stale notification permissions."
	if _, err := fmt.Printf("{\"systemMessage\":%q}\n", message); err != nil {
		logging.Warn("Failed to print notification permission guidance: %v", err)
		return
	}
	// The cooldown records successful output, not an attempt to print it.
	if stampPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(stampPath), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(stampPath, []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		logging.Warn("Failed to record notification permission guidance: %v", err)
	}
}

func (h *Handler) permissionGuidanceStamp() (string, bool) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		return "", true
	}

	stampPath := filepath.Join(cacheDir, "claude-notifications-go", "macos-notification-permission-reminder")

	if info, err := os.Stat(stampPath); err == nil {
		if time.Since(info.ModTime()) < 24*time.Hour {
			return stampPath, false
		}
	}

	return stampPath, true
}
