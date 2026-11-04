package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
)

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
