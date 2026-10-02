//go:build windows

package main

import (
	"path/filepath"
	"testing"
)

// Git Bash passes slash-form absolute paths to a native Windows executable.
// Rejecting them prevents the public bootstrap and /init wizard from running.
func TestSetupWizardAcceptsMSYSAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile ü with spaces")
	slash := filepath.ToSlash(profile)
	t.Setenv("CODEX_HOME", slash+"/")
	t.Setenv("CLAUDE_CONFIG_DIR", slash+"/")
	req, _, err := parseSetupWizard([]string{
		"--action", "inspect", "--package", slash,
		"--plugin-root", slash, "--claude-mcp-config", filepath.ToSlash(filepath.Join(profile, ".claude.json")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.PackageRoot != profile || req.PluginRoot != profile || req.EnvCodexHome != profile || req.EnvClaudeConfig != profile || req.MCPConfig["claude"] != filepath.Join(profile, ".claude.json") {
		t.Fatalf("slash path not normalized: %+v", req)
	}
	configured, _, err := parseNotificationConfigure([]string{"--provider", "codex", "--codex-home", slash})
	if err != nil || configured.CodexHome != profile {
		t.Fatalf("configure slash path: %+v %v", configured, err)
	}
	for _, bad := range []string{slash + "/../other", "relative/path", slash + "//nested"} {
		if _, _, err := parseSetupWizard([]string{"--action", "inspect", "--package", bad}); err == nil {
			t.Fatalf("accepted nonphysical path %q", bad)
		}
	}
}
