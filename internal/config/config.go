package config

// Config represents the plugin configuration
type Config struct {
	Notifications NotificationsConfig   `json:"notifications"`
	Statuses      map[string]StatusInfo `json:"statuses"`
	Debug         DebugConfig           `json:"debug,omitempty"`

	// Derived solely from an explicit schema-2 agent profile, never defaults.
	terminalClickAuthorized bool
	terminalClickPath       string
	terminalClickRevision   string
}

// DebugConfig represents debug/diagnostic settings
type DebugConfig struct {
	Benchmark bool `json:"benchmark"` // Enable benchmark timing output to log file
}

// Do Not Disturb handling modes for notifications.respectDoNotDisturb.
const (
	// DNDModeOff never consults the desktop's DND state (default).
	DNDModeOff = "off"
	// DNDModeSilent delivers the banner but skips the plugin's own sound, so
	// the notification still reaches the notification centre.
	DNDModeSilent = "silent"
	// DNDModeSuppress skips the desktop notification entirely.
	DNDModeSuppress = "suppress"
)

// NotificationsConfig represents notification settings
type NotificationsConfig struct {
	Desktop                                     DesktopConfig    `json:"desktop"`
	Webhook                                     WebhookConfig    `json:"webhook"`
	SuppressQuestionAfterTaskCompleteSeconds    *int             `json:"suppressQuestionAfterTaskCompleteSeconds"`
	SuppressQuestionAfterAnyNotificationSeconds *int             `json:"suppressQuestionAfterAnyNotificationSeconds"`
	NotifyOnSubagentStop                        bool             `json:"notifyOnSubagentStop"`           // Send notifications when subagents (Task tool) complete, default: false. Requires suppressForSubagents=false to take effect.
	SuppressForSubagents                        *bool            `json:"suppressForSubagents"`           // Suppress subagent (SubagentStop) notifications, and Stop notifications whose transcript_path is a subagent/teammate transcript; default: true. Overrides notifyOnSubagentStop.
	NotifyOnTextResponse                        *bool            `json:"notifyOnTextResponse"`           // Send notifications for text-only responses (no tools), default: true
	RespectJudgeMode                            *bool            `json:"respectJudgeMode"`               // Honor CLAUDE_HOOK_JUDGE_MODE=true env var to suppress notifications, default: true
	SuppressFilters                             []SuppressFilter `json:"suppressFilters,omitempty"`      // Rules for suppressing notifications by status/branch/folder
	TeamMode                                    string           `json:"teamMode,omitempty"`             // Team mode: "always" (no suppression, default), "wait-all" (suppress lead, notify when all idle), "never" (silent in team mode)
	NotifyOnlyWhenUnfocused                     *bool            `json:"notifyOnlyWhenUnfocused"`        // Suppress desktop notifications while the terminal window running Claude Code has OS focus, default: false
	NotifyDelaySeconds                          *int             `json:"notifyDelaySeconds"`             // Wait N seconds before delivering a desktop notification (paired with notifyOnlyWhenUnfocused, it re-checks focus after the wait), default: 0
	RespectDoNotDisturb                         *string          `json:"respectDoNotDisturb,omitempty"`  // How to treat the desktop's Do Not Disturb state: "off" (default), "silent" (deliver the banner, skip the sound), "suppress" (skip the notification entirely)
	RespectDisplaySleep                         *bool            `json:"respectDisplaySleep"`            // Skip the plugin's own sound while every display is asleep (macOS only), default: false
	ReplaceNotificationsPerSession              *bool            `json:"replaceNotificationsPerSession"` // macOS: replace a session's previous banner with its newest desktop notification instead of stacking, default: false
}

// DesktopConfig represents desktop notification settings
type DesktopConfig struct {
	Enabled                     bool    `json:"enabled"`
	Sound                       bool    `json:"sound"`
	SystemSound                 *bool   `json:"systemSound,omitempty"` // OS default cue for native observers
	TerminalBell                *bool   `json:"terminalBell"`
	Volume                      float64 `json:"volume"`
	AudioDevice                 string  `json:"audioDevice"`
	AppIcon                     string  `json:"appIcon"`
	ClickToFocus                bool    `json:"clickToFocus"`
	TerminalClickConsentVersion *int    `json:"terminalClickConsentVersion,omitempty"` // Fresh opt-in for Qoder/Gemini/Cursor
	ShowSessionLabel            *bool   `json:"showSessionLabel"`
	TerminalBundleID            string  `json:"terminalBundleId"`
	ZellijFocus                 string  `json:"zellijFocus,omitempty"`
}

// WebhookConfig represents webhook settings
type WebhookConfig struct {
	Enabled        bool                   `json:"enabled"`
	Preset         string                 `json:"preset"`
	URL            string                 `json:"url"`
	ChatID         string                 `json:"chat_id"`
	Format         string                 `json:"format"`
	Headers        map[string]string      `json:"headers"`
	PayloadFields  map[string]interface{} `json:"payloadFields,omitempty"`
	Retry          RetryConfig            `json:"retry"`
	CircuitBreaker CircuitBreakerConfig   `json:"circuitBreaker"`
	RateLimit      RateLimitConfig        `json:"rateLimit"`
}

// IsSystemSoundEnabled requires affirmative native sound consent in addition
// to the existing global sound switch. Missing config never starts audio.
func (c *Config) IsSystemSoundEnabled() bool {
	return c != nil && c.Notifications.Desktop.Sound &&
		c.Notifications.Desktop.SystemSound != nil && *c.Notifications.Desktop.SystemSound
}

// RetryConfig represents retry settings
type RetryConfig struct {
	Enabled        bool   `json:"enabled"`
	MaxAttempts    int    `json:"maxAttempts"`
	InitialBackoff string `json:"initialBackoff"` // e.g. "1s"
	MaxBackoff     string `json:"maxBackoff"`     // e.g. "10s"
}

// CircuitBreakerConfig represents circuit breaker settings
type CircuitBreakerConfig struct {
	Enabled          bool   `json:"enabled"`
	FailureThreshold int    `json:"failureThreshold"` // failures before opening
	Timeout          string `json:"timeout"`          // time to wait in open state, e.g. "30s"
	SuccessThreshold int    `json:"successThreshold"` // successes needed in half-open
}

// RateLimitConfig represents rate limiting settings
type RateLimitConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requestsPerMinute"`
}

// StatusChannelConfig represents per-channel status overrides.
type StatusChannelConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // nil = inherit default enabled behavior
}

// StatusInfo represents configuration for a specific status
type StatusInfo struct {
	Enabled *bool                `json:"enabled,omitempty"` // nil = true (default for backward compatibility)
	Desktop *StatusChannelConfig `json:"desktop,omitempty"`
	Webhook *StatusChannelConfig `json:"webhook,omitempty"`
	Title   string               `json:"title"`
	Sound   string               `json:"sound"`
}

// SuppressFilter defines conditions for suppressing notifications.
// All specified (non-nil) fields must match for the filter to suppress.
// Omitted fields act as wildcards (match any value).
type SuppressFilter struct {
	Name      string  `json:"name,omitempty"`
	Status    *string `json:"status,omitempty"`
	GitBranch *string `json:"gitBranch"` // no omitempty — nil means "any", "" means "no branch"
	Folder    *string `json:"folder,omitempty"`
}

// Matches returns true if all specified fields match the given values.
func (f *SuppressFilter) Matches(status, gitBranch, folder string) bool {
	if f.Status != nil && *f.Status != status {
		return false
	}
	if f.GitBranch != nil && *f.GitBranch != gitBranch {
		return false
	}
	if f.Folder != nil && *f.Folder != folder {
		return false
	}
	return true
}

// HasConditions returns true if the filter has at least one condition field set.
func (f *SuppressFilter) HasConditions() bool {
	return f.Status != nil || f.GitBranch != nil || f.Folder != nil
}

// intPtr returns a pointer to the given int value
func intPtr(v int) *int {
	return &v
}

// stringPtr returns a pointer to the given string value
func stringPtr(v string) *string {
	return &v
}

const defaultSuppressQuestionAfterAnyNotificationSeconds = 7
