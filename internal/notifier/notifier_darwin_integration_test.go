//go:build darwin

package notifier

import (
	"os"
	"os/exec"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

func TestSendWithTerminalNotifier_Integration(t *testing.T) {
	// Skip in CI - no NotificationCenter available
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping in CI - no NotificationCenter available")
	}

	// Prefer ClaudeNotifier.app if built
	cleanup, ok := setupClaudeNotifierEnv(t)
	defer cleanup()
	if !ok {
		if !IsTerminalNotifierAvailable() {
			t.Skip("Neither ClaudeNotifier.app nor terminal-notifier available")
		}
	}

	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// This will send a real notification - we just verify it doesn't error
	err := n.sendWithTerminalNotifier("Integration Test", "This is a test notification", "", "", false, "", true)
	if err != nil {
		t.Errorf("sendWithTerminalNotifier failed: %v", err)
	}
}

func TestTerminalNotifier_CommandExecution(t *testing.T) {
	// Prefer ClaudeNotifier.app if built
	cleanup, ok := setupClaudeNotifierEnv(t)
	defer cleanup()
	if !ok {
		if _, err := GetTerminalNotifierPath(); err != nil {
			t.Skip("Neither ClaudeNotifier.app nor terminal-notifier available")
		}
	}

	path, err := GetTerminalNotifierPath()
	if err != nil {
		t.Fatalf("GetTerminalNotifierPath failed: %v", err)
	}

	t.Logf("Using notifier: %s", path)

	// Test that the binary accepts -help and produces output
	cmd := exec.Command(path, "-help")
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Some versions may return non-zero for -help, that's ok
		t.Logf("-help returned: %v (output: %s)", err, string(output))
	}

	// Verify binary is executable and produces output
	if len(output) == 0 {
		t.Error("notifier produced no output for -help")
	}
}
