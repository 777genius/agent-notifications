package notifier

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTmuxE2E runs end-to-end tests for tmux integration.
// It spawns an isolated tmux server (via -S) so it never touches
// the user's running sessions.
func TestTmuxE2E(t *testing.T) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not found, skipping e2e tests")
	}

	// Skip in CI — tmux may not be available or may behave differently
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping tmux e2e tests in CI")
	}

	socketPath := filepath.Join(t.TempDir(), "tmux.sock")
	sessionName := "e2e-test"

	// Create isolated tmux session with 3 windows: default(0), editor(1), logs(2)
	mustRunTmux(t, tmuxPath, socketPath, "new-session", "-d", "-s", sessionName, "-n", "default")
	mustRunTmux(t, tmuxPath, socketPath, "new-window", "-t", sessionName, "-n", "editor")
	mustRunTmux(t, tmuxPath, socketPath, "new-window", "-t", sessionName, "-n", "logs")

	// Start on window 0 (default)
	mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":0")

	// Get pane IDs for each window
	editorPaneID := getTmuxPaneID(t, tmuxPath, socketPath, sessionName+":editor")
	logsPaneID := getTmuxPaneID(t, tmuxPath, socketPath, sessionName+":logs")

	// Get tmux server PID for TMUX env var
	serverPID := strings.TrimSpace(runTmux(t, tmuxPath, socketPath,
		"display-message", "-p", "#{pid}"))

	// Save and restore TMUX env var
	oldTmux := os.Getenv("TMUX")
	t.Cleanup(func() {
		// Kill isolated tmux server
		_ = exec.Command(tmuxPath, "-S", socketPath, "kill-server").Run()
		os.Setenv("TMUX", oldTmux)
	})

	// Set TMUX env to point at our isolated server
	os.Setenv("TMUX", fmt.Sprintf("%s,%s,0", socketPath, serverPID))

	t.Run("tmux_path_resolution", func(t *testing.T) {
		got := getTmuxPath()
		if got == "" || got == "tmux" {
			t.Fatal("getTmuxPath() should return absolute path when tmux is installed")
		}
		if got != tmuxPath {
			t.Errorf("getTmuxPath() = %q, want %q", got, tmuxPath)
		}
	})

	t.Run("socket_extraction", func(t *testing.T) {
		got := getTmuxSocketPath()
		if got != socketPath {
			t.Errorf("getTmuxSocketPath() = %q, want %q", got, socketPath)
		}
	})

	t.Run("args_construction", func(t *testing.T) {
		args := buildTmuxNotifierArgs("Title", "Message", editorPaneID, "com.test.app")

		// Must have -title, -message, -activate, -execute, -group
		if !containsArg(args, "-title", "Title") {
			t.Error("Missing -title")
		}
		if !containsArg(args, "-message", "Message") {
			t.Error("Missing -message")
		}
		if !containsArg(args, "-activate", "com.test.app") {
			t.Error("Missing -activate")
		}

		executeCmd := getArgValue(args, "-execute")
		if executeCmd == "" {
			t.Fatal("Missing -execute argument")
		}

		// -execute must contain absolute tmux path
		if !strings.Contains(executeCmd, tmuxPath) {
			t.Errorf("-execute should contain absolute tmux path %q, got: %s", tmuxPath, executeCmd)
		}
		// -execute must contain socket path (-S)
		if !strings.Contains(executeCmd, socketPath) {
			t.Errorf("-execute should contain socket path %q, got: %s", socketPath, executeCmd)
		}
		// -execute must contain pane target
		if !strings.Contains(executeCmd, editorPaneID) {
			t.Errorf("-execute should contain pane target %q, got: %s", editorPaneID, executeCmd)
		}

		// -group must be present
		group := getArgValue(args, "-group")
		if group == "" {
			t.Error("Missing -group argument")
		}
	})

	t.Run("click_switches_to_editor", func(t *testing.T) {
		// Reset to window 0
		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":0")
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "default")

		// Build args as if we're sending a notification with editor pane target
		args := buildTmuxNotifierArgs("Test", "Msg", editorPaneID, "com.test.app")
		executeCmd := getArgValue(args, "-execute")
		if executeCmd == "" {
			t.Fatal("-execute argument is empty")
		}

		// Emulate click: run the -execute command through /bin/sh -c
		out, err := exec.Command("/bin/sh", "-c", executeCmd).CombinedOutput()
		if err != nil {
			t.Fatalf("execute command failed: %v, output: %s", err, string(out))
		}

		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "editor")
	})

	t.Run("click_switches_to_logs", func(t *testing.T) {
		// Reset to window 0
		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":0")
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "default")

		// Build args for logs pane target
		args := buildTmuxNotifierArgs("Test", "Msg", logsPaneID, "com.test.app")
		executeCmd := getArgValue(args, "-execute")
		if executeCmd == "" {
			t.Fatal("-execute argument is empty")
		}

		// Emulate click
		out, err := exec.Command("/bin/sh", "-c", executeCmd).CombinedOutput()
		if err != nil {
			t.Fatalf("execute command failed: %v, output: %s", err, string(out))
		}

		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "logs")
	})

	t.Run("is_tmux_detection", func(t *testing.T) {
		if !IsTmux() {
			t.Error("IsTmux() should return true when TMUX env is set")
		}
	})

	t.Run("click_roundtrip", func(t *testing.T) {
		// default → editor → logs → editor (verify round-trip works)
		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":0")
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "default")

		// → editor
		execClick(t, buildTmuxNotifierArgs("Test", "Msg", editorPaneID, "com.test.app"))
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "editor")

		// → logs
		execClick(t, buildTmuxNotifierArgs("Test", "Msg", logsPaneID, "com.test.app"))
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "logs")

		// → editor again
		execClick(t, buildTmuxNotifierArgs("Test", "Msg", editorPaneID, "com.test.app"))
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "editor")
	})

	t.Run("split_pane_focus", func(t *testing.T) {
		// Split "editor" window into two panes horizontally
		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":editor")
		mustRunTmux(t, tmuxPath, socketPath, "split-window", "-h", "-t", sessionName+":editor")

		// Get pane IDs for both panes in the editor window
		out := runTmux(t, tmuxPath, socketPath,
			"list-panes", "-t", sessionName+":editor", "-F", "#{pane_id}")
		panes := strings.Split(strings.TrimSpace(out), "\n")
		if len(panes) < 2 {
			t.Fatalf("expected at least 2 panes after split, got %d", len(panes))
		}
		pane0 := strings.TrimSpace(panes[0])
		pane1 := strings.TrimSpace(panes[1])

		// Switch to window 0, then click to pane1 in editor
		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":0")
		execClick(t, buildTmuxNotifierArgs("Test", "Msg", pane1, "com.test.app"))

		// Verify we're on the editor window
		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "editor")

		// Verify the active pane is pane1
		activePane := strings.TrimSpace(runTmux(t, tmuxPath, socketPath,
			"display-message", "-t", sessionName, "-p", "#{pane_id}"))
		if activePane != pane1 {
			t.Errorf("active pane = %q, want %q", activePane, pane1)
		}

		// Now click to pane0
		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":0")
		execClick(t, buildTmuxNotifierArgs("Test", "Msg", pane0, "com.test.app"))

		verifyActiveWindow(t, tmuxPath, socketPath, sessionName, "editor")
		activePane = strings.TrimSpace(runTmux(t, tmuxPath, socketPath,
			"display-message", "-t", sessionName, "-p", "#{pane_id}"))
		if activePane != pane0 {
			t.Errorf("active pane = %q, want %q", activePane, pane0)
		}
	})

	t.Run("fallback_without_tmux_pane_uses_current_socket", func(t *testing.T) {
		oldPane := os.Getenv("TMUX_PANE")
		os.Unsetenv("TMUX_PANE")
		t.Cleanup(func() {
			if oldPane != "" {
				os.Setenv("TMUX_PANE", oldPane)
			} else {
				os.Unsetenv("TMUX_PANE")
			}
		})

		mustRunTmux(t, tmuxPath, socketPath, "select-window", "-t", sessionName+":logs")

		target, err := GetTmuxPaneTarget()
		if err != nil {
			t.Fatalf("GetTmuxPaneTarget fallback failed: %v", err)
		}
		if target != logsPaneID {
			t.Errorf("fallback target = %q, want %q", target, logsPaneID)
		}
	})

	t.Run("control_mode_false_for_normal_client", func(t *testing.T) {
		// A normal (non -CC) tmux client should not report control mode
		if IsTmuxControlMode() {
			t.Error("should be false for normal tmux client")
		}
	})

	t.Run("non_cc_mode_uses_select_window", func(t *testing.T) {
		// In a normal tmux session (not -CC), buildTmuxClickArgs should
		// use select-window, not the iTerm2 Python API
		oldPane := os.Getenv("TMUX_PANE")
		os.Setenv("TMUX_PANE", editorPaneID)
		t.Cleanup(func() {
			if oldPane != "" {
				os.Setenv("TMUX_PANE", oldPane)
			} else {
				os.Unsetenv("TMUX_PANE")
			}
		})

		args, err := buildTmuxClickArgs("Test", "Msg", "com.test", nil)
		if err != nil {
			t.Fatalf("buildTmuxClickArgs failed: %v", err)
		}
		executeCmd := getArgValue(args, "-execute")
		if !strings.Contains(executeCmd, "select-window") {
			t.Errorf("expected select-window in normal mode, got: %s", executeCmd)
		}
	})

	t.Run("socket_extraction_edge_cases", func(t *testing.T) {
		// Save current TMUX value
		savedTmux := os.Getenv("TMUX")
		defer os.Setenv("TMUX", savedTmux)

		tests := []struct {
			name     string
			tmuxEnv  string
			expected string
		}{
			{"standard_format", "/tmp/tmux-501/default,12345,0", "/tmp/tmux-501/default"},
			{"no_commas", "/tmp/tmux-501/default", "/tmp/tmux-501/default"},
			{"empty", "", ""},
			{"single_comma", "/sock,123", "/sock"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				os.Setenv("TMUX", tt.tmuxEnv)
				got := getTmuxSocketPath()
				if got != tt.expected {
					t.Errorf("getTmuxSocketPath() with TMUX=%q = %q, want %q", tt.tmuxEnv, got, tt.expected)
				}
			})
		}
	})
}

// --- helpers ---

// mustRunTmux runs a tmux command on the isolated server and fails the test on error.
func mustRunTmux(t *testing.T, tmuxPath, socketPath string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-S", socketPath}, args...)
	out, err := exec.Command(tmuxPath, fullArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v failed: %v, output: %s", args, err, string(out))
	}
}

// runTmux runs a tmux command and returns stdout.
func runTmux(t *testing.T, tmuxPath, socketPath string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"-S", socketPath}, args...)
	out, err := exec.Command(tmuxPath, fullArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v failed: %v, output: %s", args, err, string(out))
	}
	return string(out)
}

// getTmuxPaneID returns the pane ID (e.g. "%3") for a given window target.
func getTmuxPaneID(t *testing.T, tmuxPath, socketPath, windowTarget string) string {
	t.Helper()
	out := runTmux(t, tmuxPath, socketPath,
		"list-panes", "-t", windowTarget, "-F", "#{pane_id}")
	paneID := strings.TrimSpace(out)
	if paneID == "" {
		t.Fatalf("empty pane_id for window %s", windowTarget)
	}
	// If multiple panes, take the first one
	if lines := strings.Split(paneID, "\n"); len(lines) > 0 {
		paneID = strings.TrimSpace(lines[0])
	}
	return paneID
}

// execClick emulates a notification click by running the -execute arg through /bin/sh -c.
func execClick(t *testing.T, args []string) {
	t.Helper()
	executeCmd := getArgValue(args, "-execute")
	if executeCmd == "" {
		t.Fatal("-execute argument is empty")
	}
	out, err := exec.Command("/bin/sh", "-c", executeCmd).CombinedOutput()
	if err != nil {
		t.Fatalf("execute command failed: %v, output: %s", err, string(out))
	}
}

// verifyActiveWindow checks that the active window in the session matches the expected name.
func verifyActiveWindow(t *testing.T, tmuxPath, socketPath, sessionName, expectedWindow string) {
	t.Helper()
	out := runTmux(t, tmuxPath, socketPath,
		"display-message", "-t", sessionName, "-p", "#{window_name}")
	activeWindow := strings.TrimSpace(out)
	if activeWindow != expectedWindow {
		t.Errorf("active window = %q, want %q", activeWindow, expectedWindow)
	}
}
