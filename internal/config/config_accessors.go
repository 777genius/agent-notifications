package config

import (
	"strings"

	"github.com/777genius/agent-notifications/internal/logging"
)

// GetStatusInfo returns status information for a given status
func (c *Config) GetStatusInfo(status string) (StatusInfo, bool) {
	info, exists := c.Statuses[status]
	return info, exists
}

// IsDesktopEnabled returns true if desktop notifications are enabled
func (c *Config) IsDesktopEnabled() bool {
	return c.Notifications.Desktop.Enabled
}

// IsWebhookEnabled returns true if webhook notifications are enabled
func (c *Config) IsWebhookEnabled() bool {
	return c.Notifications.Webhook.Enabled
}

// IsAnyNotificationEnabled returns true if at least one notification method is enabled
func (c *Config) IsAnyNotificationEnabled() bool {
	return c.IsDesktopEnabled() || c.IsWebhookEnabled()
}

// GetSuppressQuestionAfterTaskCompleteSeconds returns the cooldown in seconds
// after task completion before question notifications are allowed (default: 12)
func (c *Config) GetSuppressQuestionAfterTaskCompleteSeconds() int {
	if c.Notifications.SuppressQuestionAfterTaskCompleteSeconds == nil {
		return 12
	}
	return *c.Notifications.SuppressQuestionAfterTaskCompleteSeconds
}

// GetSuppressQuestionAfterAnyNotificationSeconds returns the cooldown in seconds
// after any notification before question notifications are allowed (default: 7).
func (c *Config) GetSuppressQuestionAfterAnyNotificationSeconds() int {
	if c.Notifications.SuppressQuestionAfterAnyNotificationSeconds == nil {
		return defaultSuppressQuestionAfterAnyNotificationSeconds
	}
	return *c.Notifications.SuppressQuestionAfterAnyNotificationSeconds
}

// ShouldNotifyOnTextResponse returns true if notifications should be sent for text-only responses (default: true)
func (c *Config) ShouldNotifyOnTextResponse() bool {
	if c.Notifications.NotifyOnTextResponse == nil {
		return true // Default: notify on text responses
	}
	return *c.Notifications.NotifyOnTextResponse
}

// ShouldSuppressForSubagents returns true if notifications should be suppressed
// when transcript_path contains /subagents/ (default: true)
func (c *Config) ShouldSuppressForSubagents() bool {
	if c.Notifications.SuppressForSubagents == nil {
		return true // Default: suppress subagent notifications
	}
	return *c.Notifications.SuppressForSubagents
}

// IsBenchmarkEnabled returns true if benchmark timing is enabled via config
func (c *Config) IsBenchmarkEnabled() bool {
	return c.Debug.Benchmark
}

// ShouldRespectJudgeMode returns true if CLAUDE_HOOK_JUDGE_MODE=true env var should suppress notifications (default: true)
func (c *Config) ShouldRespectJudgeMode() bool {
	if c.Notifications.RespectJudgeMode == nil {
		return true // Default: respect judge mode
	}
	return *c.Notifications.RespectJudgeMode
}

// ShouldNotifyOnlyWhenUnfocused returns true if desktop notifications should be
// suppressed while the terminal window running Claude Code currently has OS focus
// (default: false).
func (c *Config) ShouldNotifyOnlyWhenUnfocused() bool {
	if c.Notifications.NotifyOnlyWhenUnfocused == nil {
		return false // Default: notify regardless of focus
	}
	return *c.Notifications.NotifyOnlyWhenUnfocused
}

// GetNotifyDelaySeconds returns how many seconds to wait before delivering a
// desktop notification (default: 0). Negative values are treated as 0.
func (c *Config) GetNotifyDelaySeconds() int {
	if c.Notifications.NotifyDelaySeconds == nil {
		return 0 // Default: no delay
	}
	if *c.Notifications.NotifyDelaySeconds < 0 {
		return 0
	}
	return *c.Notifications.NotifyDelaySeconds
}

// GetDoNotDisturbMode returns how the desktop's Do Not Disturb state should be
// treated: DNDModeOff (default), DNDModeSilent, or DNDModeSuppress.
//
// Unknown values fall back to DNDModeOff with a warning. Rejecting them in
// Validate would abort config loading and therefore silence every notification
// over a typo in one optional field, which is the exact failure this feature is
// meant to avoid.
func (c *Config) GetDoNotDisturbMode() string {
	if c.Notifications.RespectDoNotDisturb == nil {
		return DNDModeOff // Default: never consult the desktop's DND state
	}
	switch strings.ToLower(strings.TrimSpace(*c.Notifications.RespectDoNotDisturb)) {
	case DNDModeSilent:
		return DNDModeSilent
	case DNDModeSuppress:
		return DNDModeSuppress
	case DNDModeOff:
		return DNDModeOff
	default:
		logging.Warn("Unknown respectDoNotDisturb value %q, falling back to %q", *c.Notifications.RespectDoNotDisturb, DNDModeOff)
		return DNDModeOff
	}
}

// ShouldRespectDisplaySleep returns true if the plugin's own sound should be
// skipped while every display is asleep (default: false). It has no effect on
// platforms with no display-sleep detector; see docs/DO_NOT_DISTURB.md.
func (c *Config) ShouldRespectDisplaySleep() bool {
	if c.Notifications.RespectDisplaySleep == nil {
		return false // Default: play sound regardless of display sleep state
	}
	return *c.Notifications.RespectDisplaySleep
}

// ShouldReplaceNotificationsPerSession returns true if a newer desktop
// notification from the same session should replace that session's previous
// banner instead of stacking a new one (default: false). macOS only; other
// delivery paths always stack.
func (c *Config) ShouldReplaceNotificationsPerSession() bool {
	if c.Notifications.ReplaceNotificationsPerSession == nil {
		return false // Default: stack, never replace
	}
	return *c.Notifications.ReplaceNotificationsPerSession
}

// GetTeamMode returns the team notification mode: "always" (default), "wait-all", or "never"
func (c *Config) GetTeamMode() string {
	switch c.Notifications.TeamMode {
	case "wait-all", "never":
		return c.Notifications.TeamMode
	default:
		return "always"
	}
}

// IsStatusEnabled returns true if notifications for this status are enabled
// Returns true by default (if Enabled is nil or not specified) for backward compatibility
func (c *Config) IsStatusEnabled(status string) bool {
	info, exists := c.Statuses[status]
	if !exists {
		return true // unknown statuses are enabled by default
	}
	if info.Enabled == nil {
		return true // nil means enabled (backward compatibility)
	}
	return *info.Enabled
}

func isStatusChannelEnabled(channel *StatusChannelConfig) bool {
	if channel == nil || channel.Enabled == nil {
		return true
	}
	return *channel.Enabled
}

// IsTerminalBellEnabled returns true if terminal bell (BEL) should be sent (default: true)
func (c *Config) IsTerminalBellEnabled() bool {
	if c.Notifications.Desktop.TerminalBell == nil {
		return true // Default: enabled
	}
	return *c.Notifications.Desktop.TerminalBell
}

// IsSessionLabelEnabled returns true if the session label should be appended to the
// notification title (default: true)
func (c *Config) IsSessionLabelEnabled() bool {
	if c.Notifications.Desktop.ShowSessionLabel == nil {
		return true // Default: enabled
	}
	return *c.Notifications.Desktop.ShowSessionLabel
}

// IsStatusDesktopEnabled returns true if desktop notifications for this status are enabled
// Considers global desktop.enabled, per-status enabled, and per-channel desktop override.
func (c *Config) IsStatusDesktopEnabled(status string) bool {
	if !c.IsDesktopEnabled() || !c.IsStatusEnabled(status) {
		return false
	}

	info, exists := c.Statuses[status]
	if !exists {
		return true
	}

	return isStatusChannelEnabled(info.Desktop)
}

// IsStatusWebhookEnabled returns true if webhook notifications for this status are enabled
// Considers global webhook.enabled, per-status enabled, and per-channel webhook override.
func (c *Config) IsStatusWebhookEnabled(status string) bool {
	if !c.IsWebhookEnabled() || !c.IsStatusEnabled(status) {
		return false
	}

	info, exists := c.Statuses[status]
	if !exists {
		return true
	}

	return isStatusChannelEnabled(info.Webhook)
}

// ShouldFilter returns true if any suppress-filter rule matches the given context.
// When true, the notification should be suppressed entirely (both desktop and webhook).
func (c *Config) ShouldFilter(status, gitBranch, folder string) bool {
	for i := range c.Notifications.SuppressFilters {
		if !c.Notifications.SuppressFilters[i].HasConditions() {
			continue
		}
		if c.Notifications.SuppressFilters[i].Matches(status, gitBranch, folder) {
			return true
		}
	}
	return false
}
