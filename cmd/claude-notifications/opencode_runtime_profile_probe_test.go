package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The native child records this before exiting; inspecting only the SDK's
// returned evidence would not prove which environment actually reached startup.
func assertRuntimeProbePrivateEnvironment(t *testing.T, cwd string, environment []string) {
	t.Helper()
	if !filepath.IsAbs(cwd) {
		t.Fatal("probe did not start in private cwd")
	}
	values := make(map[string]string)
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		key = strings.ToUpper(key)
		if !ok {
			t.Fatal("invalid probe environment")
		}
		if _, exists := values[key]; exists {
			t.Fatal("duplicate probe environment key", key)
		}
		values[key] = value
	}
	static := map[string]string{"PATH": "", "OPENCODE_CONFIG_CONTENT": "{}", "OPENCODE_CLI_CONFIG_CONTENT": "{}",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_DISABLE_AUTOUPDATE": "1"}
	for key, expected := range static {
		if value, ok := values[key]; !ok || value != expected {
			t.Fatal("probe startup flag changed", key)
		}
		delete(values, key)
	}
	privateKeys := []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP", "OPENCODE_CONFIG"}
	if runtime.GOOS == "windows" {
		privateKeys = append(privateKeys, "USERPROFILE", "APPDATA", "LOCALAPPDATA")
	}
	for _, key := range privateKeys {
		value, ok := values[key]
		rel, err := filepath.Rel(cwd, value)
		if !ok || !filepath.IsAbs(value) || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatal("probe root escaped private cwd", key)
		}
		delete(values, key)
	}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SYSTEMROOT", "WINDIR"} {
			delete(values, key)
		}
	}
	if len(values) != 0 {
		t.Fatal("ambient probe environment", values)
	}
}
