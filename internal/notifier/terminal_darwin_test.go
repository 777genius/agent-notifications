//go:build darwin

package notifier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetTerminalBundleID_ConfigOverride(t *testing.T) {
	// Config override should take priority
	result := GetTerminalBundleID("custom.bundle.id")
	if result != "custom.bundle.id" {
		t.Errorf("Expected config override 'custom.bundle.id', got '%s'", result)
	}
}

func TestGetTerminalBundleID_CFBundleIdentifier(t *testing.T) {
	// Save and restore original env
	original := os.Getenv("__CFBundleIdentifier")
	defer os.Setenv("__CFBundleIdentifier", original)

	// Clear TERM_PROGRAM to ensure we test __CFBundleIdentifier
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	os.Unsetenv("TERM_PROGRAM")
	defer os.Setenv("TERM_PROGRAM", originalTermProgram)

	os.Setenv("__CFBundleIdentifier", "dev.warp.Warp-Stable")

	result := GetTerminalBundleID("")
	if result != "dev.warp.Warp-Stable" {
		t.Errorf("Expected __CFBundleIdentifier 'dev.warp.Warp-Stable', got '%s'", result)
	}
}

func TestGetTerminalBundleID_Fallback(t *testing.T) {
	// Save and restore original env
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	originalTmux := os.Getenv("TMUX")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
		os.Setenv("TMUX", originalTmux)
	}()

	// Clear all env vars (including TMUX to disable tmux fallback)
	os.Unsetenv("__CFBundleIdentifier")
	os.Unsetenv("TERM_PROGRAM")
	os.Unsetenv("TMUX")

	result := GetTerminalBundleID("")
	if result != "com.apple.Terminal" {
		t.Errorf("Expected fallback 'com.apple.Terminal', got '%s'", result)
	}
}

func TestGetTerminalBundleID_UnknownTermProgram(t *testing.T) {
	// Save and restore original env
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	originalTmux := os.Getenv("TMUX")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
		os.Setenv("TMUX", originalTmux)
	}()

	// Clear __CFBundleIdentifier and TMUX, set unknown TERM_PROGRAM
	os.Unsetenv("__CFBundleIdentifier")
	os.Unsetenv("TMUX")
	os.Setenv("TERM_PROGRAM", "UnknownTerminal")

	result := GetTerminalBundleID("")
	if result != "com.apple.Terminal" {
		t.Errorf("Expected fallback for unknown terminal, got '%s'", result)
	}
}

func TestGetTerminalBundleID_Priority(t *testing.T) {
	// Save and restore original env
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
	}()

	// Set both env vars - __CFBundleIdentifier should take priority over TERM_PROGRAM
	os.Setenv("__CFBundleIdentifier", "from.cfbundle")
	os.Setenv("TERM_PROGRAM", "iTerm.app")

	result := GetTerminalBundleID("")
	if result != "from.cfbundle" {
		t.Errorf("Expected __CFBundleIdentifier to take priority, got '%s'", result)
	}

	// Config override should take priority over everything
	result = GetTerminalBundleID("config.override")
	if result != "config.override" {
		t.Errorf("Expected config override to take priority, got '%s'", result)
	}
}

func TestIsTerminalNotifierAvailable(t *testing.T) {
	// This test just checks that the function doesn't panic
	// The actual result depends on whether terminal-notifier is installed
	available := IsTerminalNotifierAvailable()
	t.Logf("terminal-notifier available: %v", available)
}

func TestGetTerminalNotifierPath(t *testing.T) {
	// This test just checks that the function returns a valid result
	// (either a path or an error)
	path, err := GetTerminalNotifierPath()
	if err != nil {
		t.Logf("terminal-notifier not found (expected if not installed): %v", err)
	} else {
		t.Logf("terminal-notifier found at: %s", path)
	}
}

func TestGetTerminalNotifierPath_ClaudeNotifier(t *testing.T) {
	cleanup, ok := setupClaudeNotifierEnv(t)
	defer cleanup()
	if !ok {
		t.Skip("ClaudeNotifier.app not built (run 'make build-notifier' first)")
	}

	path, err := GetTerminalNotifierPath()
	if err != nil {
		t.Fatalf("Expected ClaudeNotifier.app to be found, got error: %v", err)
	}

	if !filepath.IsAbs(path) {
		t.Errorf("Expected absolute path, got: %s", path)
	}

	// Should resolve to ClaudeNotifier.app binary
	if filepath.Base(path) != "terminal-notifier-modern" {
		t.Errorf("Expected binary name 'terminal-notifier-modern', got: %s", filepath.Base(path))
	}

	t.Logf("ClaudeNotifier found at: %s", path)
}

func TestGetTerminalNotifierPathFollowsStableAlias(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, "generation-test.app")
	binary := filepath.Join(generation, "Contents", "MacOS", "terminal-notifier-modern")
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(root, "plugin")
	bin := filepath.Join(plugin, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(generation, filepath.Join(bin, "ClaudeNotifier.app")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PLUGIN_ROOT", plugin)
	path, err := GetTerminalNotifierPath()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("hook path %s, want %s", resolved, want)
	}
}

func TestGetTerminalBundleID_AllMappings(t *testing.T) {
	// Test all known terminal mappings
	testCases := []struct {
		termProgram string
		expected    string
	}{
		{"Apple_Terminal", "com.apple.Terminal"},
		{"iTerm.app", "com.googlecode.iterm2"},
		{"WarpTerminal", "dev.warp.Warp-Stable"},
		{"kitty", "net.kovidgoyal.kitty"},
		{"ghostty", "com.mitchellh.ghostty"},
		{"WezTerm", "com.github.wez.wezterm"},
		{"Alacritty", "org.alacritty"},
		{"Hyper", "co.zeit.hyper"},
		{"vscode", "com.microsoft.VSCode"},
	}

	// Save and restore original env
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
	}()

	// Clear __CFBundleIdentifier to test TERM_PROGRAM mapping
	os.Unsetenv("__CFBundleIdentifier")

	for _, tc := range testCases {
		t.Run(tc.termProgram, func(t *testing.T) {
			os.Setenv("TERM_PROGRAM", tc.termProgram)
			result := GetTerminalBundleID("")
			if result != tc.expected {
				t.Errorf("For TERM_PROGRAM='%s', expected '%s', got '%s'",
					tc.termProgram, tc.expected, result)
			}
		})
	}
}

func TestGetTerminalNotifierPath_PluginRoot(t *testing.T) {
	// Save and restore original env
	originalPluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT")
	defer os.Setenv("CLAUDE_PLUGIN_ROOT", originalPluginRoot)

	// Test with non-existent plugin root
	os.Setenv("CLAUDE_PLUGIN_ROOT", "/nonexistent/path")
	_, err := GetTerminalNotifierPath()
	// Should fall back to system path or return error
	// We can't guarantee terminal-notifier is installed, so just check it doesn't panic
	_ = err

	// Test with empty plugin root
	os.Unsetenv("CLAUDE_PLUGIN_ROOT")
	_, err = GetTerminalNotifierPath()
	// Should check system path
	_ = err
}

func TestGetTerminalNotifierPath_DevelopmentFallback(t *testing.T) {
	originalPluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT")
	defer os.Setenv("CLAUDE_PLUGIN_ROOT", originalPluginRoot)

	repoRoot := t.TempDir()
	devBinary := filepath.Join(
		repoRoot,
		"swift-notifier",
		"ClaudeNotifier.app",
		"Contents",
		"MacOS",
		"terminal-notifier-modern",
	)
	if err := os.MkdirAll(filepath.Dir(devBinary), 0o755); err != nil {
		t.Fatalf("failed to create dev notifier dir: %v", err)
	}
	if err := os.WriteFile(devBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to create dev notifier binary: %v", err)
	}

	os.Setenv("CLAUDE_PLUGIN_ROOT", repoRoot)

	path, err := GetTerminalNotifierPath()
	if err != nil {
		t.Fatalf("expected dev fallback path, got error: %v", err)
	}
	if path != devBinary {
		t.Fatalf("expected dev notifier path %q, got %q", devBinary, path)
	}
}

func TestGetTerminalBundleID_EmptyConfigWithEnvVars(t *testing.T) {
	// Save and restore original env
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
	}()

	// Test that empty config override allows env var detection
	os.Setenv("__CFBundleIdentifier", "custom.from.env")
	result := GetTerminalBundleID("")
	if result != "custom.from.env" {
		t.Errorf("Expected 'custom.from.env', got '%s'", result)
	}
}

func TestGetTerminalBundleID_ConfigOverridesTrimmed(t *testing.T) {
	// Config override should be used as-is
	result := GetTerminalBundleID("  spaced.bundle.id  ")
	if result != "  spaced.bundle.id  " {
		t.Errorf("Config override should be used as-is, got '%s'", result)
	}
}

func TestGetTerminalBundleID_MultipleEnvVars(t *testing.T) {
	// Save and restore original env
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
	}()

	// When both env vars are set, __CFBundleIdentifier takes priority
	os.Setenv("__CFBundleIdentifier", "from.cfbundle.env")
	os.Setenv("TERM_PROGRAM", "iTerm.app")

	result := GetTerminalBundleID("")
	if result != "from.cfbundle.env" {
		t.Errorf("Expected __CFBundleIdentifier to take priority, got '%s'", result)
	}

	// Clear __CFBundleIdentifier, now TERM_PROGRAM should be used
	os.Unsetenv("__CFBundleIdentifier")
	result = GetTerminalBundleID("")
	if result != "com.googlecode.iterm2" {
		t.Errorf("Expected TERM_PROGRAM mapping, got '%s'", result)
	}
}

func TestGetTerminalBundleID_TmuxFallback(t *testing.T) {
	// Inside tmux, TERM_PROGRAM is "tmux" — we should fall back to
	// tmux show-environment to get the real terminal's TERM_PROGRAM
	if os.Getenv("TMUX") == "" {
		t.Skip("Skipping: not inside a tmux session")
	}

	// Clear env vars to force tmux fallback path
	originalCFBundle := os.Getenv("__CFBundleIdentifier")
	originalTermProgram := os.Getenv("TERM_PROGRAM")
	defer func() {
		os.Setenv("__CFBundleIdentifier", originalCFBundle)
		os.Setenv("TERM_PROGRAM", originalTermProgram)
	}()

	os.Unsetenv("__CFBundleIdentifier")
	os.Setenv("TERM_PROGRAM", "tmux") // simulate tmux overwrite

	result := GetTerminalBundleID("")
	// Should NOT be com.apple.Terminal (the default fallback)
	// but the actual terminal's bundle ID from tmux environment
	t.Logf("Detected bundle ID via tmux: %s", result)

	if result == "com.apple.Terminal" {
		// This may still happen if tmux session env doesn't have TERM_PROGRAM
		t.Log("Warning: tmux session env didn't have TERM_PROGRAM, got fallback")
	}
}

func TestGetBundleIDFromTmuxEnv(t *testing.T) {
	if os.Getenv("TMUX") == "" {
		t.Skip("Skipping: not inside a tmux session")
	}

	bundleID := getBundleIDFromTmuxEnv()
	t.Logf("getBundleIDFromTmuxEnv() = %q", bundleID)
	// Just verify it doesn't crash; result depends on environment
}

func TestIsTerminalNotifierAvailable_Consistency(t *testing.T) {
	// IsTerminalNotifierAvailable should be consistent with GetTerminalNotifierPath
	available := IsTerminalNotifierAvailable()
	path, err := GetTerminalNotifierPath()

	if available && err != nil {
		t.Error("IsTerminalNotifierAvailable returned true but GetTerminalNotifierPath returned error")
	}
	if !available && err == nil {
		t.Errorf("IsTerminalNotifierAvailable returned false but GetTerminalNotifierPath returned path: %s", path)
	}
}
