package config

import (
	"fmt"
	"math"
)

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate volume
	if math.IsNaN(c.Notifications.Desktop.Volume) || c.Notifications.Desktop.Volume < 0.0 || c.Notifications.Desktop.Volume > 1.0 {
		return fmt.Errorf("desktop volume must be between 0.0 and 1.0 (got %.2f)", c.Notifications.Desktop.Volume)
	}

	if v := c.Notifications.Desktop.TerminalClickConsentVersion; v != nil && *v != 1 {
		return fmt.Errorf("terminalClickConsentVersion must be 1 when set")
	}

	// Validate webhook preset (only if webhooks are enabled)
	validPresets := map[string]bool{
		"slack":    true,
		"discord":  true,
		"telegram": true,
		"lark":     true,
		"custom":   true,
	}
	if c.Notifications.Webhook.Enabled && !validPresets[c.Notifications.Webhook.Preset] {
		return fmt.Errorf("invalid webhook preset: %s (must be one of: slack, discord, telegram, lark, custom)", c.Notifications.Webhook.Preset)
	}

	// Validate webhook format (only if webhooks are enabled)
	validFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if c.Notifications.Webhook.Enabled && !validFormats[c.Notifications.Webhook.Format] {
		return fmt.Errorf("invalid webhook format: %s (must be one of: json, text)", c.Notifications.Webhook.Format)
	}

	if c.Notifications.Webhook.Enabled &&
		c.Notifications.Webhook.Preset == "custom" &&
		c.Notifications.Webhook.Format == "text" &&
		len(c.Notifications.Webhook.PayloadFields) > 0 {
		return fmt.Errorf("webhook payloadFields require json format when using custom preset")
	}

	// Validate webhook URL if enabled
	if c.Notifications.Webhook.Enabled && c.Notifications.Webhook.URL == "" {
		return fmt.Errorf("webhook URL is required when webhooks are enabled")
	}

	// Validate Telegram chat_id if Telegram preset is used
	if c.Notifications.Webhook.Enabled && c.Notifications.Webhook.Preset == "telegram" && c.Notifications.Webhook.ChatID == "" {
		return fmt.Errorf("chat_id is required for Telegram webhook")
	}

	// Validate cooldowns (both fields, if explicitly set)
	if c.Notifications.SuppressQuestionAfterTaskCompleteSeconds != nil && *c.Notifications.SuppressQuestionAfterTaskCompleteSeconds < 0 {
		return fmt.Errorf("suppressQuestionAfterTaskCompleteSeconds must be >= 0")
	}
	if c.Notifications.SuppressQuestionAfterAnyNotificationSeconds != nil && *c.Notifications.SuppressQuestionAfterAnyNotificationSeconds < 0 {
		return fmt.Errorf("suppressQuestionAfterAnyNotificationSeconds must be >= 0")
	}
	if c.Notifications.NotifyDelaySeconds != nil && *c.Notifications.NotifyDelaySeconds < 0 {
		return fmt.Errorf("notifyDelaySeconds must be >= 0")
	}

	// Validate teamMode
	validTeamModes := map[string]bool{"": true, "wait-all": true, "always": true, "never": true}
	if !validTeamModes[c.Notifications.TeamMode] {
		return fmt.Errorf("invalid teamMode %q (must be one of: always, wait-all, never)", c.Notifications.TeamMode)
	}

	// Validate suppress-filters
	validStatuses := map[string]bool{
		"task_complete":         true,
		"review_complete":       true,
		"question":              true,
		"plan_ready":            true,
		"session_limit_reached": true,
		"api_error":             true,
		"api_error_overloaded":  true,
		"agent_error":           true,
		"permission_request":    true,
	}
	for i, f := range c.Notifications.SuppressFilters {
		if !f.HasConditions() {
			return fmt.Errorf("suppressFilters[%d]: must have at least one condition (status, gitBranch, or folder)", i)
		}
		if f.Status != nil && !validStatuses[*f.Status] {
			return fmt.Errorf("suppressFilters[%d]: invalid status %q", i, *f.Status)
		}
	}

	return nil
}
