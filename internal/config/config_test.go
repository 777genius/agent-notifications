package config

import (
	"github.com/777genius/agent-notifications/internal/testenv"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setTestHome isolates all configuration, metadata and temporary paths.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	testenv.Set(t, dir)
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.True(t, cfg.Notifications.Desktop.Enabled)
	assert.True(t, cfg.Notifications.Desktop.Sound)
	assert.False(t, cfg.Notifications.Webhook.Enabled)
	assert.Equal(t, intPtr(12), cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds)

	// Check statuses
	assert.Contains(t, cfg.Statuses, "task_complete")
	assert.Contains(t, cfg.Statuses, "question")
	assert.Contains(t, cfg.Statuses, "plan_ready")
}

func TestLoadConfig(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configJSON := `{
		"notifications": {
			"desktop": {
				"enabled": false,
				"sound": false,
				"appIcon": ""
			},
			"webhook": {
				"enabled": true,
				"preset": "slack",
				"url": "https://hooks.slack.com/test",
				"format": "json",
				"payloadFields": {
					"context": {
						"gitEmail": "${{git.user.email}}"
					}
				}
			},
			"suppressQuestionAfterTaskCompleteSeconds": 10
		},
		"statuses": {
			"task_complete": {
				"title": "Done",
				"sound": ""
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	// Load config
	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.False(t, cfg.Notifications.Desktop.Enabled)
	assert.True(t, cfg.Notifications.Webhook.Enabled)
	assert.Equal(t, "slack", cfg.Notifications.Webhook.Preset)
	assert.Equal(t, "${{git.user.email}}", cfg.Notifications.Webhook.PayloadFields["context"].(map[string]interface{})["gitEmail"])
	assert.Equal(t, intPtr(10), cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds)
}

func TestLoadConfig_PartialStatusesPreserveDefaults(t *testing.T) {
	pluginRoot := t.TempDir()
	t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)
	configPath := filepath.Join(t.TempDir(), "config.json")
	configJSON := `{
		"statuses": {
			"task_complete": {"sound": "custom.mp3"},
			"question": {"title": "Ask"},
			"review_complete": null,
			"api_error": {"sound": ""}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(configJSON), 0o600))

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, "✅ Completed", cfg.Statuses["task_complete"].Title)
	assert.Equal(t, "custom.mp3", cfg.Statuses["task_complete"].Sound)
	assert.Equal(t, "Ask", cfg.Statuses["question"].Title)
	assert.Equal(t, filepath.Join(pluginRoot, "sounds", "question.mp3"), cfg.Statuses["question"].Sound)
	assert.Equal(t, "🔍 Review", cfg.Statuses["review_complete"].Title)
	assert.Equal(t, filepath.Join(pluginRoot, "sounds", "review-complete.mp3"), cfg.Statuses["review_complete"].Sound)
	assert.Equal(t, "🔴 API Error: 401", cfg.Statuses["api_error"].Title)
	assert.Empty(t, cfg.Statuses["api_error"].Sound)
}

func TestDefaultConfig_UnresolvedPluginRootUsesCurrentDirectory(t *testing.T) {
	for _, pluginRoot := range []string{"$CLAUDE_PLUGIN_ROOT", "${CLAUDE_PLUGIN_ROOT}"} {
		t.Run(pluginRoot, func(t *testing.T) {
			t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)
			cfg := DefaultConfig()
			assert.Equal(t, "claude_icon.png", cfg.Notifications.Desktop.AppIcon)
			assert.Equal(t, filepath.Join("sounds", "task-complete.mp3"), cfg.Statuses["task_complete"].Sound)
		})
	}
}

func TestLoadConfigNotExists(t *testing.T) {
	// Load non-existent config should return defaults
	cfg, err := Load("/nonexistent/config.json")
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.Notifications.Desktop.Enabled)
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "valid config",
			cfg:     DefaultConfig(),
			wantErr: false,
		},
		{
			name: "invalid webhook preset",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "invalid",
						URL:     "https://example.com",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "webhook enabled but no URL",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "slack",
						URL:     "",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "telegram without chat_id",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "telegram",
						URL:     "https://api.telegram.org",
						ChatID:  "",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "webhook disabled with invalid preset (should pass)",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Desktop: DesktopConfig{
						Enabled: true,
						Sound:   true,
						Volume:  1.0,
					},
					Webhook: WebhookConfig{
						Enabled: false,
						Preset:  "none", // Invalid preset, but webhooks are disabled
						URL:     "",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetStatusInfo(t *testing.T) {
	cfg := DefaultConfig()

	info, exists := cfg.GetStatusInfo("task_complete")
	assert.True(t, exists)
	assert.Contains(t, info.Title, "Completed")

	_, exists = cfg.GetStatusInfo("nonexistent")
	assert.False(t, exists)
}

func TestIsNotificationEnabled(t *testing.T) {
	cfg := DefaultConfig()

	assert.True(t, cfg.IsDesktopEnabled())
	assert.False(t, cfg.IsWebhookEnabled())
	assert.True(t, cfg.IsAnyNotificationEnabled())

	// Disable all
	cfg.Notifications.Desktop.Enabled = false
	assert.False(t, cfg.IsAnyNotificationEnabled())
}

func TestDefaultConfigPathsNoMixedSeparators(t *testing.T) {
	cfg := DefaultConfig()

	// Check AppIcon path doesn't contain forward slashes on any platform
	// (should use OS-specific separators via filepath.Join)
	appIcon := cfg.Notifications.Desktop.AppIcon
	assert.NotContains(t, appIcon, "/claude_icon.png", "AppIcon should use filepath.Join, not string concatenation")

	// Check all sound paths don't contain forward slashes
	for status, info := range cfg.Statuses {
		assert.NotContains(t, info.Sound, "/sounds/", "Sound path for %s should use filepath.Join, not string concatenation", status)
	}

	// Verify paths are valid (contain expected filename)
	assert.Contains(t, appIcon, "claude_icon.png")
	assert.Contains(t, cfg.Statuses["task_complete"].Sound, "task-complete.mp3")
	assert.Contains(t, cfg.Statuses["question"].Sound, "question.mp3")
}

func TestLoadFromPluginRoot_Success(t *testing.T) {
	// Isolate stable path so it doesn't interfere
	setTestHome(t, t.TempDir())

	// Create temp plugin root with config
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	require.NoError(t, err)

	configPath := filepath.Join(configDir, "config.json")
	configJSON := `{
		"notifications": {
			"desktop": {"enabled": false, "sound": false},
			"webhook": {"enabled": true, "url": "https://test.com/webhook"}
		}
	}`
	err = os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	// Load config from plugin root
	t.Setenv(OverrideEnv, filepath.Join(tmpDir, "config", "config.json"))
	cfg, err := LoadFromPluginRoot(tmpDir)

	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.False(t, cfg.Notifications.Desktop.Enabled)
	assert.True(t, cfg.Notifications.Webhook.Enabled)
	assert.Equal(t, "https://test.com/webhook", cfg.Notifications.Webhook.URL)
}

func TestLoadFromPluginRoot_NoConfigFile(t *testing.T) {
	setTestHome(t, t.TempDir())

	// Create empty plugin root (no config file)
	tmpDir := t.TempDir()

	// Should return default config without error
	cfg, err := LoadFromPluginRoot(tmpDir)

	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.Notifications.Desktop.Enabled, "should use default config")
}

func TestLoadFromPluginRoot_MalformedJSON(t *testing.T) {
	// Isolate stable path so it doesn't interfere
	setTestHome(t, t.TempDir())

	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	require.NoError(t, err)

	configPath := filepath.Join(configDir, "config.json")
	err = os.WriteFile(configPath, []byte("{ invalid json }"), 0644)
	require.NoError(t, err)

	// Unknown historical bytes require explicit import; no fallback or migration.
	cfg, err := LoadFromPluginRoot(tmpDir)

	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), string(ConfigLegacyImportRequired))
}

func TestLoadFromPluginRoot_NonexistentRoot(t *testing.T) {
	setTestHome(t, t.TempDir())

	// Use nonexistent plugin root
	nonexistentDir := "/nonexistent/plugin/root"

	// Should return default config (file doesn't exist)
	cfg, err := LoadFromPluginRoot(nonexistentDir)

	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.Notifications.Desktop.Enabled)
}

func TestLoadFromPluginRoot_EmptyRoot(t *testing.T) {
	setTestHome(t, t.TempDir())

	// Empty string as plugin root
	cfg, err := LoadFromPluginRoot("")

	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.Notifications.Desktop.Enabled)
}

func TestLoadFromPluginRoot_WithEnvironmentVariables(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("TEST_WEBHOOK_URL", "https://example.com/hook")

	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	require.NoError(t, err)

	configPath := filepath.Join(configDir, "config.json")
	configJSON := `{
		"notifications": {
			"webhook": {
				"enabled": true,
				"url": "$TEST_WEBHOOK_URL"
			}
		}
	}`
	err = os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	// Load config - should expand environment variables
	t.Setenv(OverrideEnv, filepath.Join(tmpDir, "config", "config.json"))
	cfg, err := LoadFromPluginRoot(tmpDir)

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/hook", cfg.Notifications.Webhook.URL)
}

func TestLoadFromPluginRoot_ExpandsUnsetPluginRootFromResolvedPath(t *testing.T) {
	pluginRoot := filepath.Join(t.TempDir(), "$literal", "plugin")
	setTestHome(t, t.TempDir())
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	t.Setenv("TEST_SOUND_PATH", filepath.Join(pluginRoot, "custom.mp3"))

	configDir := filepath.Join(pluginRoot, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	data := []byte(`{
		"notifications": {"desktop": {"appIcon": "${CLAUDE_PLUGIN_ROOT}/claude_icon.png"}},
		"statuses": {
			"task_complete": {"sound": "${CLAUDE_PLUGIN_ROOT}/sounds/task-complete.mp3"},
			"question": {"sound": "$TEST_SOUND_PATH"},
			"api_error": {"sound": ""}
		}
	}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(OverrideEnv, filepath.Join(pluginRoot, "config", "config.json"))
	cfg, err := LoadFromPluginRoot(pluginRoot)
	if err != nil {
		t.Fatalf("LoadFromPluginRoot() error = %v", err)
	}

	wantIcon := filepath.Join(pluginRoot, "claude_icon.png")
	if cfg.Notifications.Desktop.AppIcon != wantIcon {
		t.Errorf("app icon = %q, want %q", cfg.Notifications.Desktop.AppIcon, wantIcon)
	}
	wantSound := filepath.Join(pluginRoot, "sounds", "task-complete.mp3")
	if got := cfg.Statuses["task_complete"].Sound; got != wantSound {
		t.Errorf("sound = %q, want %q", got, wantSound)
	}
	if got := cfg.Statuses["question"].Sound; got != filepath.Join(pluginRoot, "custom.mp3") {
		t.Errorf("custom sound = %q", got)
	}
	if got := cfg.Statuses["api_error"].Sound; got != "" {
		t.Errorf("empty sound = %q, want empty", got)
	}
	wantDefaultSound := filepath.Join(pluginRoot, "sounds", "review-complete.mp3")
	if got := cfg.Statuses["review_complete"].Sound; got != wantDefaultSound {
		t.Errorf("default sound = %q, want %q", got, wantDefaultSound)
	}
}

func TestLoadFromPluginRoot_PreservesDollarInDefaultPaths(t *testing.T) {
	pluginRoot := filepath.Join(t.TempDir(), "$literal", "plugin")
	setTestHome(t, t.TempDir())
	configDir := filepath.Join(pluginRoot, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(OverrideEnv, filepath.Join(pluginRoot, "config", "config.json"))
	cfg, err := LoadFromPluginRoot(pluginRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Notifications.Desktop.AppIcon, filepath.Join(pluginRoot, "claude_icon.png"); got != want {
		t.Errorf("app icon = %q, want %q", got, want)
	}
	if got, want := cfg.Statuses["task_complete"].Sound, filepath.Join(pluginRoot, "sounds", "task-complete.mp3"); got != want {
		t.Errorf("default sound = %q, want %q", got, want)
	}
}

func TestLoadFromPluginRoot_NullStatusesUseResolvedRoot(t *testing.T) {
	pluginRoot := t.TempDir()
	setTestHome(t, t.TempDir())
	configDir := filepath.Join(pluginRoot, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"statuses": null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(OverrideEnv, filepath.Join(pluginRoot, "config", "config.json"))
	cfg, err := LoadFromPluginRoot(pluginRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(pluginRoot, "sounds", "task-complete.mp3")
	if got := cfg.Statuses["task_complete"].Sound; got != want {
		t.Errorf("default sound = %q, want %q", got, want)
	}
}

func TestExpandPath_PreservesEmptyValue(t *testing.T) {
	if got := expandPath("", t.TempDir()); got != "" {
		t.Fatalf("expandPath(empty) = %q, want empty", got)
	}
}

// === Tests for ApplyDefaults ===

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *Config
		expected *Config
	}{
		{
			name: "Apply defaults to empty config",
			cfg:  &Config{},
			expected: func() *Config {
				def := DefaultConfig()
				return def
			}(),
		},
		{
			name: "Preserve existing desktop settings",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Desktop: DesktopConfig{
						Enabled: false,
						Sound:   false,
						Volume:  0.5,
					},
				},
			},
			expected: &Config{
				Notifications: NotificationsConfig{
					Desktop: DesktopConfig{
						Enabled: false, // Preserved
						Sound:   false, // Preserved
						Volume:  0.5,   // Preserved
					},
					SuppressQuestionAfterTaskCompleteSeconds: intPtr(12), // Default
				},
			},
		},
		{
			name: "Apply missing statuses from defaults",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Desktop: DesktopConfig{
						Enabled: true,
					},
				},
				Statuses: map[string]StatusInfo{
					"task_complete": {
						Title: "Custom Title",
					},
				},
			},
			expected: func() *Config {
				def := DefaultConfig()
				def.Statuses["task_complete"] = StatusInfo{
					Title: "Custom Title", // Preserved custom
				}
				return def
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.ApplyDefaults()

			// Check key fields are set
			if tt.cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds == nil {
				t.Errorf("SuppressQuestionAfterTaskCompleteSeconds should be set to default")
			}
			if tt.cfg.Notifications.Webhook.PayloadFields == nil {
				t.Errorf("Webhook.PayloadFields should be initialized")
			}
			if len(tt.cfg.Statuses) == 0 {
				t.Errorf("Statuses should be populated from defaults")
			}
			// Verify statuses contain required entries
			if _, ok := tt.cfg.Statuses["task_complete"]; !ok {
				t.Errorf("Statuses should contain task_complete")
			}
			if _, ok := tt.cfg.Statuses["question"]; !ok {
				t.Errorf("Statuses should contain question")
			}
		})
	}
}

// === Additional Validate tests for better coverage ===

func TestValidateConfig_MoreCases(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "invalid webhook format",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "slack",
						URL:     "https://example.com",
						Format:  "invalid_format",
					},
				},
			},
			wantErr: true,
			errMsg:  "format",
		},
		{
			name: "custom preset with valid URL",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "custom",
						URL:     "https://my-webhook.com/endpoint",
						Format:  "json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "discord preset with valid URL",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "discord",
						URL:     "https://discord.com/api/webhooks/123/abc",
						Format:  "json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "telegram with chat_id",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "telegram",
						URL:     "https://api.telegram.org/bot123:ABC/sendMessage",
						ChatID:  "123456789",
						Format:  "json",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "custom text webhook cannot use payload fields",
			cfg: &Config{
				Notifications: NotificationsConfig{
					Webhook: WebhookConfig{
						Enabled: true,
						Preset:  "custom",
						URL:     "https://example.com",
						Format:  "text",
						PayloadFields: map[string]interface{}{
							"git_email": "${{git.user.email}}",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "payloadFields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply defaults first (Validate expects defaults to be applied)
			tt.cfg.ApplyDefaults()

			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidate_InvalidVolume(t *testing.T) {
	tests := []struct {
		name   string
		volume float64
	}{
		{"volume too low", -0.1},
		{"volume too high", 1.1},
		{"volume way too high", 2.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Notifications.Desktop.Volume = tt.volume

			err := cfg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "volume must be between 0.0 and 1.0")
		})
	}
}

func TestValidate_NegativeCooldown(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds = intPtr(-1)

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "suppressQuestionAfterTaskCompleteSeconds must be >= 0")
}

// === Tests for cooldown zero-value fix (issue #37) ===

func TestApplyDefaults_PreservesZeroCooldown(t *testing.T) {
	// Setting cooldown to 0 should mean "disabled", not "use default 12"
	cfg := &Config{
		Notifications: NotificationsConfig{
			SuppressQuestionAfterTaskCompleteSeconds:    intPtr(0),
			SuppressQuestionAfterAnyNotificationSeconds: intPtr(0),
		},
	}

	cfg.ApplyDefaults()

	// 0 should be preserved, NOT overwritten with 12
	assert.Equal(t, intPtr(0), cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds)
	assert.Equal(t, intPtr(0), cfg.Notifications.SuppressQuestionAfterAnyNotificationSeconds)
}

func TestApplyDefaults_SetsDefaultForNilCooldown(t *testing.T) {
	// nil (not set in config) should get defaults
	cfg := &Config{
		Notifications: NotificationsConfig{
			// Both fields are nil (not set)
		},
	}

	cfg.ApplyDefaults()

	assert.Equal(t, intPtr(12), cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds)
	assert.Equal(t, intPtr(defaultSuppressQuestionAfterAnyNotificationSeconds), cfg.Notifications.SuppressQuestionAfterAnyNotificationSeconds)
}

func TestGetCooldownSeconds_Defaults(t *testing.T) {
	cfg := &Config{}

	// nil should return defaults
	assert.Equal(t, 12, cfg.GetSuppressQuestionAfterTaskCompleteSeconds())
	assert.Equal(t, defaultSuppressQuestionAfterAnyNotificationSeconds, cfg.GetSuppressQuestionAfterAnyNotificationSeconds())
}

func TestGetCooldownSeconds_Zero(t *testing.T) {
	cfg := &Config{
		Notifications: NotificationsConfig{
			SuppressQuestionAfterTaskCompleteSeconds:    intPtr(0),
			SuppressQuestionAfterAnyNotificationSeconds: intPtr(0),
		},
	}

	// 0 means disabled
	assert.Equal(t, 0, cfg.GetSuppressQuestionAfterTaskCompleteSeconds())
	assert.Equal(t, 0, cfg.GetSuppressQuestionAfterAnyNotificationSeconds())
}

func TestGetCooldownSeconds_CustomValues(t *testing.T) {
	cfg := &Config{
		Notifications: NotificationsConfig{
			SuppressQuestionAfterTaskCompleteSeconds:    intPtr(5),
			SuppressQuestionAfterAnyNotificationSeconds: intPtr(30),
		},
	}

	assert.Equal(t, 5, cfg.GetSuppressQuestionAfterTaskCompleteSeconds())
	assert.Equal(t, 30, cfg.GetSuppressQuestionAfterAnyNotificationSeconds())
}

func TestValidate_NegativeCooldownForAnyNotification(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications.SuppressQuestionAfterAnyNotificationSeconds = intPtr(-1)

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "suppressQuestionAfterAnyNotificationSeconds must be >= 0")
}

func TestLoadConfig_ZeroCooldownPreserved(t *testing.T) {
	// Simulate loading config with explicit 0 values
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configJSON := `{
		"notifications": {
			"suppressQuestionAfterTaskCompleteSeconds": 0,
			"suppressQuestionAfterAnyNotificationSeconds": 0
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// 0 must be preserved after load + ApplyDefaults
	assert.Equal(t, 0, cfg.GetSuppressQuestionAfterTaskCompleteSeconds())
	assert.Equal(t, 0, cfg.GetSuppressQuestionAfterAnyNotificationSeconds())
}

// === Tests for Click-to-Focus settings ===

func TestDefaultConfig_ClickToFocus(t *testing.T) {
	cfg := DefaultConfig()

	// ClickToFocus should be enabled by default
	assert.True(t, cfg.Notifications.Desktop.ClickToFocus, "ClickToFocus should be true by default")

	// TerminalBundleID should be empty (auto-detect)
	assert.Empty(t, cfg.Notifications.Desktop.TerminalBundleID, "TerminalBundleID should be empty for auto-detect")
}

func TestIsTerminalBellEnabled(t *testing.T) {
	// Default (nil) should be true
	cfg := DefaultConfig()
	assert.True(t, cfg.IsTerminalBellEnabled(), "TerminalBell should be true by default (nil)")

	// Explicitly true
	bellOn := true
	cfg.Notifications.Desktop.TerminalBell = &bellOn
	assert.True(t, cfg.IsTerminalBellEnabled(), "TerminalBell should be true when set to true")

	// Explicitly false
	bellOff := false
	cfg.Notifications.Desktop.TerminalBell = &bellOff
	assert.False(t, cfg.IsTerminalBellEnabled(), "TerminalBell should be false when set to false")
}

func TestIsSessionLabelEnabled(t *testing.T) {
	// Default (nil) should be true
	cfg := DefaultConfig()
	assert.True(t, cfg.IsSessionLabelEnabled(), "ShowSessionLabel should be true by default (nil)")

	// Explicitly true
	labelOn := true
	cfg.Notifications.Desktop.ShowSessionLabel = &labelOn
	assert.True(t, cfg.IsSessionLabelEnabled(), "ShowSessionLabel should be true when set to true")

	// Explicitly false
	labelOff := false
	cfg.Notifications.Desktop.ShowSessionLabel = &labelOff
	assert.False(t, cfg.IsSessionLabelEnabled(), "ShowSessionLabel should be false when set to false")
}

func TestLoadConfig_ShowSessionLabel(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Test explicit showSessionLabel: false
	configJSON := `{
		"notifications": {
			"desktop": {
				"enabled": true,
				"sound": true,
				"showSessionLabel": false
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	require.NotNil(t, cfg.Notifications.Desktop.ShowSessionLabel)
	assert.False(t, cfg.IsSessionLabelEnabled(), "ShowSessionLabel should be false when explicitly set")
}

func TestLoadConfig_ShowSessionLabel_DefaultWhenNotSpecified(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Config without showSessionLabel - existing installs keep the label
	configJSON := `{
		"notifications": {
			"desktop": {
				"enabled": true,
				"sound": true
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Nil(t, cfg.Notifications.Desktop.ShowSessionLabel)
	assert.True(t, cfg.IsSessionLabelEnabled(), "ShowSessionLabel should default to true")
}

func TestLoadConfig_ClickToFocus(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Test explicit clickToFocus: false
	configJSON := `{
		"notifications": {
			"desktop": {
				"enabled": true,
				"sound": true,
				"clickToFocus": false,
				"terminalBundleId": "com.custom.terminal"
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.False(t, cfg.Notifications.Desktop.ClickToFocus, "ClickToFocus should be false when explicitly set")
	assert.Equal(t, "com.custom.terminal", cfg.Notifications.Desktop.TerminalBundleID)
}

func TestLoadConfig_ClickToFocus_DefaultWhenNotSpecified(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Config without clickToFocus field - should inherit from DefaultConfig
	configJSON := `{
		"notifications": {
			"desktop": {
				"enabled": true,
				"sound": true
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Should inherit default value (true) since we unmarshal into DefaultConfig()
	assert.True(t, cfg.Notifications.Desktop.ClickToFocus, "ClickToFocus should default to true")
}

func TestLoadConfig_TerminalBundleID_Variations(t *testing.T) {
	tests := []struct {
		name             string
		bundleID         string
		expectedBundleID string
	}{
		{"iTerm2", "com.googlecode.iterm2", "com.googlecode.iterm2"},
		{"Warp", "dev.warp.Warp-Stable", "dev.warp.Warp-Stable"},
		{"Terminal.app", "com.apple.Terminal", "com.apple.Terminal"},
		{"Kitty", "net.kovidgoyal.kitty", "net.kovidgoyal.kitty"},
		{"Empty (auto-detect)", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")

			configJSON := `{
				"notifications": {
					"desktop": {
						"terminalBundleId": "` + tt.bundleID + `"
					}
				}
			}`

			err := os.WriteFile(configPath, []byte(configJSON), 0644)
			require.NoError(t, err)

			cfg, err := Load(configPath)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedBundleID, cfg.Notifications.Desktop.TerminalBundleID)
		})
	}
}

func TestApplyDefaults_ClickToFocus(t *testing.T) {
	// When loading a config without clickToFocus, ApplyDefaults shouldn't change it
	// because bool defaults to false and we can't distinguish "not set" from "set to false"
	// The solution is to use DefaultConfig() as base for Unmarshal

	cfg := &Config{
		Notifications: NotificationsConfig{
			Desktop: DesktopConfig{
				Enabled:      true,
				Sound:        true,
				Volume:       0.5,
				ClickToFocus: false, // Explicitly set to false
			},
		},
	}

	cfg.ApplyDefaults()

	// ClickToFocus should remain false (user explicitly set it)
	assert.False(t, cfg.Notifications.Desktop.ClickToFocus)

	// Volume should be preserved
	assert.Equal(t, 0.5, cfg.Notifications.Desktop.Volume)
}

// === Tests for Per-Status Enabled ===

func boolPtr(b bool) *bool {
	return &b
}

func TestIsStatusEnabled(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		enabled  *bool
		expected bool
	}{
		{
			name:     "nil means enabled (backward compatibility)",
			status:   "task_complete",
			enabled:  nil,
			expected: true,
		},
		{
			name:     "explicit true",
			status:   "task_complete",
			enabled:  boolPtr(true),
			expected: true,
		},
		{
			name:     "explicit false",
			status:   "task_complete",
			enabled:  boolPtr(false),
			expected: false,
		},
		{
			name:     "unknown status returns true",
			status:   "unknown_status",
			enabled:  nil,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()

			// Set enabled for the status
			if tt.status != "unknown_status" {
				info := cfg.Statuses[tt.status]
				info.Enabled = tt.enabled
				cfg.Statuses[tt.status] = info
			}

			result := cfg.IsStatusEnabled(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsStatusDesktopEnabled(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		globalEnabled  bool
		statusEnabled  *bool
		channelEnabled *bool
		expected       bool
	}{
		{
			name:          "global enabled + status enabled by default",
			status:        "task_complete",
			globalEnabled: true,
			expected:      true,
		},
		{
			name:           "desktop override disables desktop only",
			status:         "task_complete",
			globalEnabled:  true,
			statusEnabled:  boolPtr(true),
			channelEnabled: boolPtr(false),
			expected:       false,
		},
		{
			name:           "desktop override enables desktop when status is enabled",
			status:         "task_complete",
			globalEnabled:  true,
			statusEnabled:  boolPtr(true),
			channelEnabled: boolPtr(true),
			expected:       true,
		},
		{
			name:           "status disabled overrides desktop channel allow",
			status:         "task_complete",
			globalEnabled:  true,
			statusEnabled:  boolPtr(false),
			channelEnabled: boolPtr(true),
			expected:       false,
		},
		{
			name:           "global desktop disabled",
			status:         "task_complete",
			globalEnabled:  false,
			statusEnabled:  boolPtr(true),
			channelEnabled: boolPtr(true),
			expected:       false,
		},
		{
			name:          "unknown status defaults to enabled",
			status:        "unknown_status",
			globalEnabled: true,
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Notifications.Desktop.Enabled = tt.globalEnabled

			if tt.status != "unknown_status" {
				info := cfg.Statuses[tt.status]
				info.Enabled = tt.statusEnabled
				if tt.channelEnabled != nil {
					info.Desktop = &StatusChannelConfig{Enabled: tt.channelEnabled}
				}
				cfg.Statuses[tt.status] = info
			}

			result := cfg.IsStatusDesktopEnabled(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsStatusWebhookEnabled(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		globalEnabled  bool
		statusEnabled  *bool
		channelEnabled *bool
		expected       bool
	}{
		{
			name:          "global enabled + status enabled by default",
			status:        "task_complete",
			globalEnabled: true,
			expected:      true,
		},
		{
			name:           "webhook override disables webhook only",
			status:         "task_complete",
			globalEnabled:  true,
			statusEnabled:  boolPtr(true),
			channelEnabled: boolPtr(false),
			expected:       false,
		},
		{
			name:           "webhook override enables webhook when status is enabled",
			status:         "task_complete",
			globalEnabled:  true,
			statusEnabled:  boolPtr(true),
			channelEnabled: boolPtr(true),
			expected:       true,
		},
		{
			name:           "status disabled overrides webhook channel allow",
			status:         "task_complete",
			globalEnabled:  true,
			statusEnabled:  boolPtr(false),
			channelEnabled: boolPtr(true),
			expected:       false,
		},
		{
			name:           "global webhook disabled",
			status:         "task_complete",
			globalEnabled:  false,
			statusEnabled:  boolPtr(true),
			channelEnabled: boolPtr(true),
			expected:       false,
		},
		{
			name:          "unknown status defaults to enabled",
			status:        "unknown_status",
			globalEnabled: true,
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Notifications.Webhook.Enabled = tt.globalEnabled

			if tt.status != "unknown_status" {
				info := cfg.Statuses[tt.status]
				info.Enabled = tt.statusEnabled
				if tt.channelEnabled != nil {
					info.Webhook = &StatusChannelConfig{Enabled: tt.channelEnabled}
				}
				cfg.Statuses[tt.status] = info
			}

			result := cfg.IsStatusWebhookEnabled(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBackwardCompatibility_NoEnabledField(t *testing.T) {
	// Test loading config without "enabled" field in statuses
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Old-style config without enabled field
	configJSON := `{
		"notifications": {
			"desktop": {"enabled": true}
		},
		"statuses": {
			"task_complete": {
				"title": "Task Done",
				"sound": "/path/to/sound.mp3"
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// All statuses should be enabled by default (backward compatibility)
	assert.True(t, cfg.IsStatusEnabled("task_complete"))
	assert.True(t, cfg.IsStatusEnabled("question"))
	assert.True(t, cfg.IsStatusEnabled("plan_ready"))
	assert.True(t, cfg.IsStatusEnabled("review_complete"))
	assert.True(t, cfg.IsStatusDesktopEnabled("task_complete"))
	cfg.Notifications.Webhook.Enabled = true
	assert.True(t, cfg.IsStatusWebhookEnabled("task_complete"))
}

func TestLoadConfig_WithStatusEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Config with enabled: false for task_complete
	configJSON := `{
		"notifications": {
			"desktop": {"enabled": true}
		},
		"statuses": {
			"task_complete": {
				"enabled": false,
				"title": "Task Done",
				"sound": "/path/to/sound.mp3"
			},
			"question": {
				"enabled": true,
				"title": "Question",
				"sound": "/path/to/question.mp3"
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// task_complete should be disabled
	assert.False(t, cfg.IsStatusEnabled("task_complete"))
	assert.False(t, cfg.IsStatusDesktopEnabled("task_complete"))

	// question should be enabled
	assert.True(t, cfg.IsStatusEnabled("question"))
	assert.True(t, cfg.IsStatusDesktopEnabled("question"))
	assert.False(t, cfg.IsStatusWebhookEnabled("question"))
}

func TestLoadConfig_WithStatusChannelOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configJSON := `{
		"notifications": {
			"desktop": {"enabled": true},
			"webhook": {"enabled": true, "url": "https://example.com/webhook"}
		},
		"statuses": {
			"task_complete": {
				"title": "Task Done",
				"sound": "/path/to/sound.mp3",
				"desktop": {"enabled": false},
				"webhook": {"enabled": true}
			},
			"question": {
				"title": "Question",
				"sound": "/path/to/question.mp3",
				"webhook": {"enabled": false}
			}
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	require.NotNil(t, cfg.Statuses["task_complete"].Desktop)
	require.NotNil(t, cfg.Statuses["task_complete"].Webhook)
	assert.False(t, cfg.IsStatusDesktopEnabled("task_complete"))
	assert.True(t, cfg.IsStatusWebhookEnabled("task_complete"))
	assert.True(t, cfg.IsStatusDesktopEnabled("question"))
	assert.False(t, cfg.IsStatusWebhookEnabled("question"))
}

// === Tests for stable config path ===

func TestGetStableConfigPath(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	path, err := GetStableConfigPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".claude", "claude-notifications-go", "config.json"), path)
}

func TestGetStableConfigPath_NoHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME isolation not applicable on Windows (uses USERPROFILE)")
	}

	// Unset HOME to simulate missing home directory
	setTestHome(t, "")

	_, err := GetStableConfigPath()
	assert.Error(t, err)
}

// === Tests for LoadFromPluginRoot fallback chain ===

func TestLoadFromPluginRoot_StablePathFirst(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	// Create config at stable path
	stableDir := filepath.Join(home, ".claude", "claude-notifications-go")
	require.NoError(t, os.MkdirAll(stableDir, 0700))
	stableConfig := `{"notifications":{"desktop":{"enabled":false,"sound":false},"webhook":{"enabled":true,"url":"https://stable.example.com"}}}`
	require.NoError(t, os.WriteFile(filepath.Join(stableDir, "config.json"), []byte(stableConfig), 0600))

	// No config at old path
	pluginRoot := t.TempDir()

	cfg, err := LoadFromPluginRoot(pluginRoot)
	require.NoError(t, err)
	assert.False(t, cfg.Notifications.Desktop.Enabled)
	assert.True(t, cfg.Notifications.Webhook.Enabled)
	assert.Equal(t, "https://stable.example.com", cfg.Notifications.Webhook.URL)
}

func TestLoadFromPluginRoot_StableTakesPriority(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	// Create config at BOTH paths with different values
	stableDir := filepath.Join(home, ".claude", "claude-notifications-go")
	require.NoError(t, os.MkdirAll(stableDir, 0700))
	stableConfig := `{"notifications":{"webhook":{"enabled":true,"url":"https://stable.example.com"}}}`
	require.NoError(t, os.WriteFile(filepath.Join(stableDir, "config.json"), []byte(stableConfig), 0600))

	pluginRoot := t.TempDir()
	configDir := filepath.Join(pluginRoot, "config")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	oldConfig := `{"notifications":{"webhook":{"enabled":true,"url":"https://old.example.com"}}}`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.json"), []byte(oldConfig), 0644))

	cfg, err := LoadFromPluginRoot(pluginRoot)
	require.NoError(t, err)
	assert.Equal(t, "https://stable.example.com", cfg.Notifications.Webhook.URL, "stable path should take priority")
}

func TestLoadFromPluginRoot_NeitherPath_ReturnsDefaults(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	pluginRoot := t.TempDir()

	cfg, err := LoadFromPluginRoot(pluginRoot)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, cfg.Notifications.Desktop.Enabled, "should return defaults")
}

func TestSuppressFilter_Matches(t *testing.T) {
	tests := []struct {
		name      string
		filter    SuppressFilter
		status    string
		gitBranch string
		folder    string
		want      bool
	}{
		{
			name:      "all fields match",
			filter:    SuppressFilter{Status: stringPtr("task_complete"), GitBranch: stringPtr(""), Folder: stringPtr("ClaudeProbe")},
			status:    "task_complete",
			gitBranch: "",
			folder:    "ClaudeProbe",
			want:      true,
		},
		{
			name:      "status mismatch",
			filter:    SuppressFilter{Status: stringPtr("task_complete"), GitBranch: stringPtr(""), Folder: stringPtr("ClaudeProbe")},
			status:    "question",
			gitBranch: "",
			folder:    "ClaudeProbe",
			want:      false,
		},
		{
			name:      "branch mismatch",
			filter:    SuppressFilter{Status: stringPtr("task_complete"), GitBranch: stringPtr(""), Folder: stringPtr("ClaudeProbe")},
			status:    "task_complete",
			gitBranch: "main",
			folder:    "ClaudeProbe",
			want:      false,
		},
		{
			name:      "folder mismatch",
			filter:    SuppressFilter{Status: stringPtr("task_complete"), GitBranch: stringPtr(""), Folder: stringPtr("ClaudeProbe")},
			status:    "task_complete",
			gitBranch: "",
			folder:    "my-project",
			want:      false,
		},
		{
			name:      "nil status matches any status",
			filter:    SuppressFilter{GitBranch: stringPtr(""), Folder: stringPtr("ClaudeProbe")},
			status:    "question",
			gitBranch: "",
			folder:    "ClaudeProbe",
			want:      true,
		},
		{
			name:      "nil branch matches any branch",
			filter:    SuppressFilter{Status: stringPtr("task_complete"), Folder: stringPtr("ClaudeProbe")},
			status:    "task_complete",
			gitBranch: "dev",
			folder:    "ClaudeProbe",
			want:      true,
		},
		{
			name:      "nil folder matches any folder",
			filter:    SuppressFilter{Status: stringPtr("task_complete"), GitBranch: stringPtr("main")},
			status:    "task_complete",
			gitBranch: "main",
			folder:    "anything",
			want:      true,
		},
		{
			name:      "empty branch matches no-git-repo",
			filter:    SuppressFilter{GitBranch: stringPtr("")},
			status:    "task_complete",
			gitBranch: "",
			folder:    "test",
			want:      true,
		},
		{
			name:      "empty branch does not match actual branch",
			filter:    SuppressFilter{GitBranch: stringPtr("")},
			status:    "task_complete",
			gitBranch: "main",
			folder:    "test",
			want:      false,
		},
		{
			name:      "status-only filter",
			filter:    SuppressFilter{Status: stringPtr("api_error")},
			status:    "api_error",
			gitBranch: "main",
			folder:    "my-project",
			want:      true,
		},
		{
			name:      "folder-only filter",
			filter:    SuppressFilter{Folder: stringPtr("scratch")},
			status:    "question",
			gitBranch: "dev",
			folder:    "scratch",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.filter.Matches(tt.status, tt.gitBranch, tt.folder)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSuppressFilter_HasConditions(t *testing.T) {
	assert.False(t, (&SuppressFilter{}).HasConditions(), "empty filter has no conditions")
	assert.False(t, (&SuppressFilter{Name: "just a name"}).HasConditions(), "name-only has no conditions")
	assert.True(t, (&SuppressFilter{Status: stringPtr("task_complete")}).HasConditions())
	assert.True(t, (&SuppressFilter{GitBranch: stringPtr("")}).HasConditions())
	assert.True(t, (&SuppressFilter{Folder: stringPtr("test")}).HasConditions())
}

func TestConfig_ShouldFilter(t *testing.T) {
	cfg := &Config{
		Notifications: NotificationsConfig{
			SuppressFilters: []SuppressFilter{
				{Name: "rule1", Status: stringPtr("task_complete"), GitBranch: stringPtr(""), Folder: stringPtr("ClaudeProbe")},
				{Name: "rule2", Folder: stringPtr("scratch")},
			},
		},
	}

	// Rule 1 matches
	assert.True(t, cfg.ShouldFilter("task_complete", "", "ClaudeProbe"))
	// Rule 2 matches
	assert.True(t, cfg.ShouldFilter("question", "dev", "scratch"))
	// Neither matches
	assert.False(t, cfg.ShouldFilter("task_complete", "main", "my-project"))
	// Partial match on rule 1 (wrong folder)
	assert.False(t, cfg.ShouldFilter("task_complete", "", "other-project"))
}

func TestConfig_ShouldFilter_EmptyFilters(t *testing.T) {
	cfg := DefaultConfig()
	// No filters configured — should never filter
	assert.False(t, cfg.ShouldFilter("task_complete", "", "ClaudeProbe"))
	assert.False(t, cfg.ShouldFilter("question", "main", "my-project"))
}

func TestConfig_Validate_SuppressFilters(t *testing.T) {
	tests := []struct {
		name    string
		filters []SuppressFilter
		wantErr string
	}{
		{
			name:    "valid filter with all fields",
			filters: []SuppressFilter{{Status: stringPtr("task_complete"), GitBranch: stringPtr(""), Folder: stringPtr("test")}},
		},
		{
			name:    "valid filter with status only",
			filters: []SuppressFilter{{Status: stringPtr("question")}},
		},
		{
			name:    "valid filter with folder only",
			filters: []SuppressFilter{{Folder: stringPtr("scratch")}},
		},
		{
			name:    "empty filter rejected",
			filters: []SuppressFilter{{}},
			wantErr: "suppressFilters[0]: must have at least one condition",
		},
		{
			name:    "name-only filter rejected",
			filters: []SuppressFilter{{Name: "just a name"}},
			wantErr: "suppressFilters[0]: must have at least one condition",
		},
		{
			name:    "invalid status rejected",
			filters: []SuppressFilter{{Status: stringPtr("bogus")}},
			wantErr: `suppressFilters[0]: invalid status "bogus"`,
		},
		{
			name: "second filter invalid",
			filters: []SuppressFilter{
				{Status: stringPtr("task_complete")},
				{Status: stringPtr("not_a_status")},
			},
			wantErr: `suppressFilters[1]: invalid status "not_a_status"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Notifications.SuppressFilters = tt.filters
			err := cfg.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadConfig_WithSuppressFilters(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configJSON := `{
		"notifications": {
			"desktop": {"enabled": true},
			"suppressFilters": [
				{
					"name": "Suppress ClaudeProbe completions",
					"status": "task_complete",
					"gitBranch": "",
					"folder": "ClaudeProbe"
				},
				{
					"folder": "scratch"
				}
			]
		}
	}`

	err := os.WriteFile(configPath, []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	require.Len(t, cfg.Notifications.SuppressFilters, 2)

	// First filter
	f0 := cfg.Notifications.SuppressFilters[0]
	assert.Equal(t, "Suppress ClaudeProbe completions", f0.Name)
	require.NotNil(t, f0.Status)
	assert.Equal(t, "task_complete", *f0.Status)
	require.NotNil(t, f0.GitBranch)
	assert.Equal(t, "", *f0.GitBranch)
	require.NotNil(t, f0.Folder)
	assert.Equal(t, "ClaudeProbe", *f0.Folder)

	// Second filter
	f1 := cfg.Notifications.SuppressFilters[1]
	assert.Nil(t, f1.Status)
	assert.Nil(t, f1.GitBranch)
	require.NotNil(t, f1.Folder)
	assert.Equal(t, "scratch", *f1.Folder)

	// Verify filtering works end-to-end
	assert.True(t, cfg.ShouldFilter("task_complete", "", "ClaudeProbe"))
	assert.True(t, cfg.ShouldFilter("question", "main", "scratch"))
	assert.False(t, cfg.ShouldFilter("task_complete", "main", "my-project"))
}

// === Tests for focus-aware / delayed notifications (issue #93) ===

func TestShouldNotifyOnlyWhenUnfocused_DefaultsFalse(t *testing.T) {
	// nil (not set) means notify regardless of focus
	cfg := &Config{}
	assert.False(t, cfg.ShouldNotifyOnlyWhenUnfocused())
}

func TestShouldNotifyOnlyWhenUnfocused_Explicit(t *testing.T) {
	enabled := true
	disabled := false

	cfgOn := &Config{Notifications: NotificationsConfig{NotifyOnlyWhenUnfocused: &enabled}}
	assert.True(t, cfgOn.ShouldNotifyOnlyWhenUnfocused())

	cfgOff := &Config{Notifications: NotificationsConfig{NotifyOnlyWhenUnfocused: &disabled}}
	assert.False(t, cfgOff.ShouldNotifyOnlyWhenUnfocused())
}

func TestGetNotifyDelaySeconds_DefaultsZero(t *testing.T) {
	cfg := &Config{}
	assert.Equal(t, 0, cfg.GetNotifyDelaySeconds())
}

func TestGetNotifyDelaySeconds_CustomAndNegative(t *testing.T) {
	cfg := &Config{Notifications: NotificationsConfig{NotifyDelaySeconds: intPtr(10)}}
	assert.Equal(t, 10, cfg.GetNotifyDelaySeconds())

	// Negative is clamped to 0 (Validate rejects it, but the getter is defensive)
	cfgNeg := &Config{Notifications: NotificationsConfig{NotifyDelaySeconds: intPtr(-5)}}
	assert.Equal(t, 0, cfgNeg.GetNotifyDelaySeconds())
}

func TestValidate_NegativeNotifyDelay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications.NotifyDelaySeconds = intPtr(-1)

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notifyDelaySeconds must be >= 0")
}
