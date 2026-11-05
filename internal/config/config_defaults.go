package config

import (
	"os"
	"path/filepath"
)

// DefaultConfig returns a config with sensible defaults
func DefaultConfig() *Config {
	return defaultConfig("")
}

func defaultConfig(resolvedPluginRoot string) *Config {
	// Get plugin root from environment, fallback to current directory
	pluginRoot := resolvedPluginRoot
	if pluginRoot == "" {
		pluginRoot = assetRoot(AssetContext{LookupEnv: os.LookupEnv})
	}
	if isUnresolvedPluginRoot(pluginRoot) {
		pluginRoot = "."
	}
	return buildDefaultConfig(pluginRoot)
}

func isUnresolvedPluginRoot(pluginRoot string) bool {
	return pluginRoot == "" ||
		pluginRoot == "$AGENT_NOTIFICATIONS_ROOT" ||
		pluginRoot == "${AGENT_NOTIFICATIONS_ROOT}" ||
		pluginRoot == "$CLAUDE_PLUGIN_ROOT" ||
		pluginRoot == "${CLAUDE_PLUGIN_ROOT}"
}

func buildDefaultConfig(pluginRoot string) *Config {
	return &Config{
		Notifications: NotificationsConfig{
			Desktop: DesktopConfig{
				Enabled:      true,
				Sound:        true,
				Volume:       1.0, // Full volume by default
				AppIcon:      filepath.Join(pluginRoot, "claude_icon.png"),
				ClickToFocus: true, // macOS/Linux/Windows: activate terminal on click (default: enabled)
				// TerminalBundleID: "" - empty means auto-detect
			},
			Webhook: WebhookConfig{
				Enabled:       false,
				Preset:        "custom",
				URL:           "",
				ChatID:        "",
				Format:        "json",
				Headers:       make(map[string]string),
				PayloadFields: make(map[string]interface{}),
				Retry: RetryConfig{
					Enabled:        true,
					MaxAttempts:    3,
					InitialBackoff: "1s",
					MaxBackoff:     "10s",
				},
				CircuitBreaker: CircuitBreakerConfig{
					Enabled:          true,
					FailureThreshold: 5,
					Timeout:          "30s",
					SuccessThreshold: 2,
				},
				RateLimit: RateLimitConfig{
					Enabled:           true,
					RequestsPerMinute: 10,
				},
			},
			SuppressQuestionAfterTaskCompleteSeconds:    intPtr(12),
			SuppressQuestionAfterAnyNotificationSeconds: intPtr(defaultSuppressQuestionAfterAnyNotificationSeconds),
		},
		Statuses: defaultStatuses(pluginRoot),
	}
}

func defaultStatuses(pluginRoot string) map[string]StatusInfo {
	return map[string]StatusInfo{
		"agent_stopping": {Title: "Copilot in VS Code"},
		"agent_error":    {Title: "🔴 Agent error", Sound: filepath.Join(pluginRoot, "sounds", "error.mp3")},
		"task_complete": {
			Title: "✅ Completed",
			Sound: filepath.Join(pluginRoot, "sounds", "task-complete.mp3"),
		},
		"review_complete": {
			Title: "🔍 Review",
			Sound: filepath.Join(pluginRoot, "sounds", "review-complete.mp3"),
		},
		"question": {
			Title: "❓ Question",
			Sound: filepath.Join(pluginRoot, "sounds", "question.mp3"),
		},
		"plan_ready": {
			Title: "📋 Plan",
			Sound: filepath.Join(pluginRoot, "sounds", "plan-ready.mp3"),
		},
		"session_limit_reached": {
			Title: "⏱️ Session Limit Reached",
			Sound: filepath.Join(pluginRoot, "sounds", "error.mp3"),
		},
		"api_error": {
			Title: "🔴 API Error: 401",
			Sound: filepath.Join(pluginRoot, "sounds", "error.mp3"),
		},
		"api_error_overloaded": {
			Title: "🔴 API Error",
			Sound: filepath.Join(pluginRoot, "sounds", "error.mp3"),
		},
		"permission_request": {
			Title: "🔐 Permission Request",
			Sound: filepath.Join(pluginRoot, "sounds", "question.mp3"),
		},
	}
}

// ApplyDefaults fills in missing fields with default values
func (c *Config) ApplyDefaults() {
	c.applyDefaults("")
}

func (c *Config) applyDefaults(pluginRoot string) {
	// Desktop defaults
	if c.Notifications.Desktop.Volume == 0 {
		c.Notifications.Desktop.Volume = 1.0 // Default to full volume
	}
	// AppIcon: Keep empty if not set (no default)

	// Webhook defaults
	if c.Notifications.Webhook.Preset == "" {
		c.Notifications.Webhook.Preset = "custom"
	}
	if c.Notifications.Webhook.Format == "" {
		c.Notifications.Webhook.Format = "json"
	}
	if c.Notifications.Webhook.Headers == nil {
		c.Notifications.Webhook.Headers = make(map[string]string)
	}
	if c.Notifications.Webhook.PayloadFields == nil {
		c.Notifications.Webhook.PayloadFields = make(map[string]interface{})
	}

	// Cooldown defaults (nil = not set in config, apply defaults)
	if c.Notifications.SuppressQuestionAfterTaskCompleteSeconds == nil {
		c.Notifications.SuppressQuestionAfterTaskCompleteSeconds = intPtr(12)
	}
	if c.Notifications.SuppressQuestionAfterAnyNotificationSeconds == nil {
		c.Notifications.SuppressQuestionAfterAnyNotificationSeconds = intPtr(defaultSuppressQuestionAfterAnyNotificationSeconds)
	}

	// Status defaults
	defaults := defaultConfig(pluginRoot)
	if c.Statuses == nil {
		c.Statuses = defaults.Statuses
	} else {
		// Fill in missing statuses
		for key, val := range defaults.Statuses {
			if _, exists := c.Statuses[key]; !exists {
				c.Statuses[key] = val
			}
		}
	}
}
