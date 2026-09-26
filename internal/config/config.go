package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
)

// Config represents the plugin configuration
type Config struct {
	Notifications NotificationsConfig   `json:"notifications"`
	Statuses      map[string]StatusInfo `json:"statuses"`
	Debug         DebugConfig           `json:"debug,omitempty"`
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
	NotifyOnSubagentStop                        bool             `json:"notifyOnSubagentStop"`          // Send notifications when subagents (Task tool) complete, default: false. Requires suppressForSubagents=false to take effect.
	SuppressForSubagents                        *bool            `json:"suppressForSubagents"`          // Suppress subagent (SubagentStop) notifications, and Stop notifications whose transcript_path is a subagent/teammate transcript; default: true. Overrides notifyOnSubagentStop.
	NotifyOnTextResponse                        *bool            `json:"notifyOnTextResponse"`          // Send notifications for text-only responses (no tools), default: true
	RespectJudgeMode                            *bool            `json:"respectJudgeMode"`              // Honor CLAUDE_HOOK_JUDGE_MODE=true env var to suppress notifications, default: true
	SuppressFilters                             []SuppressFilter `json:"suppressFilters,omitempty"`     // Rules for suppressing notifications by status/branch/folder
	TeamMode                                    string           `json:"teamMode,omitempty"`            // Team mode: "always" (no suppression, default), "wait-all" (suppress lead, notify when all idle), "never" (silent in team mode)
	NotifyOnlyWhenUnfocused                     *bool            `json:"notifyOnlyWhenUnfocused"`       // Suppress desktop notifications while the terminal window running Claude Code has OS focus, default: false
	NotifyDelaySeconds                          *int             `json:"notifyDelaySeconds"`            // Wait N seconds before delivering a desktop notification (paired with notifyOnlyWhenUnfocused, it re-checks focus after the wait), default: 0
	RespectDoNotDisturb                         *string          `json:"respectDoNotDisturb,omitempty"` // How to treat the desktop's Do Not Disturb state: "off" (default), "silent" (deliver the banner, skip the sound), "suppress" (skip the notification entirely)
	RespectDisplaySleep                         *bool            `json:"respectDisplaySleep"`           // Skip the plugin's own sound while every display is asleep (macOS only), default: false
}

// DesktopConfig represents desktop notification settings
type DesktopConfig struct {
	Enabled          bool    `json:"enabled"`
	Sound            bool    `json:"sound"`
	TerminalBell     *bool   `json:"terminalBell"`          // Send BEL to /dev/tty for terminal tab indicators (default: true)
	Volume           float64 `json:"volume"`                // Volume level 0.0-1.0, default 1.0 (full volume)
	AudioDevice      string  `json:"audioDevice"`           // Audio output device name (empty = system default)
	AppIcon          string  `json:"appIcon"`               // Path to app icon
	ClickToFocus     bool    `json:"clickToFocus"`          // macOS/Linux/Windows: activate the originating terminal window on notification click (default: true)
	ShowSessionLabel *bool   `json:"showSessionLabel"`      // Include the "[name id]" session label in the notification title (default: true)
	TerminalBundleID string  `json:"terminalBundleId"`      // macOS: override auto-detected terminal bundle ID (empty = auto)
	ZellijFocus      string  `json:"zellijFocus,omitempty"` // Linux: "auto" (default), "pane" (zellij 0.44.1+), "tab" (legacy), "off"
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
		Statuses: map[string]StatusInfo{
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
		},
	}
}

// Load loads configuration from a file
// If the file doesn't exist, returns default config
func Load(path string) (*Config, error) {
	return load(path, "")
}

func load(path, pluginRoot string) (*Config, error) {
	// If path doesn't exist, use default config
	if !platform.FileExists(path) {
		return defaultConfig(pluginRoot), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	config := defaultConfigForLoad(pluginRoot)
	statusDefaults := make(map[string]StatusInfo, len(config.Statuses))
	for status, info := range config.Statuses {
		statusDefaults[status] = info
	}
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	if err := mergeStatusOverrides(data, config, statusDefaults); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Expand environment variables in paths
	config.Notifications.Desktop.AppIcon = expandPath(config.Notifications.Desktop.AppIcon, pluginRoot)
	config.Notifications.Webhook.URL = platform.ExpandEnv(config.Notifications.Webhook.URL)

	// Expand environment variables in sound paths
	for status, info := range config.Statuses {
		info.Sound = expandPath(info.Sound, pluginRoot)
		config.Statuses[status] = info
	}

	// Apply defaults for missing fields
	config.applyDefaults(pluginRoot)

	return config, nil
}

func defaultConfigForLoad(pluginRoot string) *Config {
	resolvedRoot := pluginRoot
	if resolvedRoot == "" {
		resolvedRoot = assetRoot(AssetContext{LookupEnv: os.LookupEnv})
	}
	if isUnresolvedPluginRoot(resolvedRoot) {
		return buildDefaultConfig(".")
	}

	// Keep defaults symbolic until expandPath processes config values. Injecting
	// a resolved root here would make literal '$' segments expand a second time.
	return buildDefaultConfig("${CLAUDE_PLUGIN_ROOT}")
}

// mergeStatusOverrides preserves defaults for fields omitted from an individual
// status object. encoding/json otherwise replaces map values with zero structs.
func mergeStatusOverrides(data []byte, config *Config, defaults map[string]StatusInfo) error {
	var rawConfig struct {
		Statuses map[string]json.RawMessage `json:"statuses"`
	}
	if err := json.Unmarshal(data, &rawConfig); err != nil {
		return err
	}
	if rawConfig.Statuses == nil {
		return nil
	}
	if config.Statuses == nil {
		config.Statuses = make(map[string]StatusInfo)
	}
	for status, rawStatus := range rawConfig.Statuses {
		info := defaults[status]
		if err := json.Unmarshal(rawStatus, &info); err != nil {
			return fmt.Errorf("invalid status %q: %w", status, err)
		}
		config.Statuses[status] = info
	}
	return nil
}

func expandEnv(value, pluginRoot string) string {
	return os.Expand(value, func(key string) string {
		if key == AssetRootPlaceholder || key == LegacyAssetRootPlaceholder {
			return assetRoot(AssetContext{PluginRoot: pluginRoot, LookupEnv: os.LookupEnv})
		}
		return os.Getenv(key)
	})
}

func expandPath(value, pluginRoot string) string {
	expanded := expandEnv(value, pluginRoot)
	if expanded == "" {
		return ""
	}
	return filepath.Clean(expanded)
}

// GetStableConfigDir returns the stable config directory outside the plugin cache.
// This directory survives plugin updates (bootstrap.sh rm -rf of cache).
func GetStableConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "claude-notifications-go"), nil
}

// GetStableConfigPath returns the stable config file path outside the plugin cache.
func GetStableConfigPath() (string, error) {
	dir, err := GetStableConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadFromPluginRoot reads the shared canonical Store. Bundle paths are
// resources and historical evidence only; selected errors never fall back.
func LoadFromPluginRoot(pluginRoot string) (*Config, error) {
	return LoadForAgent(pluginRoot, AgentClaude)
}

func LoadForAgent(pluginRoot string, agent AgentID) (*Config, error) {
	return loadFromPluginRoot(pluginRoot, agent, func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
		logging.Warn("%s", msg)
	})
}

// LoadFromPluginRootQuiet behaves like LoadFromPluginRoot but keeps warnings
// in the file log only. Observation hook routes (Codex) must never write to
// the process stderr.
func LoadFromPluginRootQuiet(pluginRoot string) (*Config, error) {
	return LoadForAgentQuiet(pluginRoot, AgentClaude)
}

func LoadForAgentQuiet(pluginRoot string, agent AgentID) (*Config, error) {
	return loadFromPluginRoot(pluginRoot, agent, func(msg string) {
		logging.Warn("%s", msg)
	})
}

func loadFromPluginRoot(pluginRoot string, agent AgentID, warn func(string)) (*Config, error) {
	env := SnapshotEnv()
	assets, legacy := ConsumerContext(pluginRoot)
	assets.Agent = agent
	_, cfg, selection, err := ReadDocumentSelection(ReadRequest{Env: env, Assets: assets, Legacy: legacy, ReadSnapshot: ReadFileSnapshot})
	for _, diagnostic := range append(selection.Diagnostics, ConsumerDiagnostics()...) {
		warn(string(diagnostic.Code))
	}
	return cfg, err
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

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate volume
	if c.Notifications.Desktop.Volume < 0.0 || c.Notifications.Desktop.Volume > 1.0 {
		return fmt.Errorf("desktop volume must be between 0.0 and 1.0 (got %.2f)", c.Notifications.Desktop.Volume)
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
