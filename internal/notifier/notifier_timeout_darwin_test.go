//go:build darwin

package notifier

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

func TestSendDesktopLegacyCompletionFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		wantFallback bool
	}{
		{"success", nil, false},
		{"deadline exceeded", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), false},
		{"output drain expired", fmt.Errorf("wrapped: %w", exec.ErrWaitDelay), false},
		{"permission denied", &NotificationPermissionDeniedError{}, false},
		{"ordinary failure", errors.New("helper failed"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := isolateDesktopDelivery(t)
			for _, key := range []string{
				"TMUX", "STY", "ZELLIJ", "ZELLIJ_SESSION_NAME", "ZELLIJ_PANE_ID",
				"WEZTERM_PANE", "WEZTERM_UNIX_SOCKET", "KITTY_WINDOW_ID", "KITTY_LISTEN_ON",
				"WARP_FOCUS_URL", "WARP_TERMINAL_SESSION_UUID",
			} {
				t.Setenv(key, "")
			}
			called := 0
			withBeeepNotify(t, func(string, string, any) error {
				called++
				return nil
			})
			interceptLegacyNotifier(t, helper, tc.err, func(args []string) {
				if getArgValue(args, "-message") != "literal message" || getArgValue(args, "-activate") != "com.apple.Terminal" || !containsFlag(args, "-nosound") {
					t.Fatalf("legacy desktop arguments changed: %v", args)
				}
			})
			cfg := config.DefaultConfig()
			cfg.Notifications.Desktop.Enabled = true
			cfg.Notifications.Desktop.Sound = false
			cfg.Notifications.Desktop.ClickToFocus = true
			cfg.Notifications.Desktop.TerminalBundleID = "com.apple.Terminal"
			n := New(cfg)
			t.Cleanup(func() { _ = n.Close() })
			err := n.SendDesktop(analyzer.StatusTaskComplete, "literal message", "session", "")
			assertLegacyFallbackResult(t, err, tc.err, called, tc.wantFallback)
		})
	}
}

func TestSendQuickNotificationLegacyCompletionFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		wantFallback bool
	}{
		{"success", nil, false},
		{"deadline exceeded", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), false},
		{"output drain expired", fmt.Errorf("wrapped: %w", exec.ErrWaitDelay), false},
		{"ordinary failure", errors.New("helper failed"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := isolateDesktopDelivery(t)
			// Even if the fallback guard regresses, this private executable only
			// records the attempted retry and cannot send a real notification.
			dir := t.TempDir()
			marker := filepath.Join(dir, "fallback-called")
			t.Setenv("AGENT_NOTIFICATIONS_TEST_FALLBACK_MARKER", marker)
			if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte("#!/bin/sh\nprintf called > \"$AGENT_NOTIFICATIONS_TEST_FALLBACK_MARKER\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			interceptLegacyNotifier(t, helper, tc.err, func(args []string) {
				if getArgValue(args, "-title") != "literal title" || getArgValue(args, "-message") != "literal message" || getArgValue(args, "-execute") != "echo click" || !strings.HasPrefix(getArgValue(args, "-group"), "claude-quick-") || !containsFlag(args, "-nosound") {
					t.Fatalf("quick notification arguments changed: %v", args)
				}
			})
			err := SendQuickNotification("literal title", "literal message", "echo click")
			called := 0
			if _, statErr := os.Stat(marker); statErr == nil {
				called = 1
			} else if !os.IsNotExist(statErr) {
				t.Fatal(statErr)
			}
			assertLegacyFallbackResult(t, err, tc.err, called, tc.wantFallback)
		})
	}
}

func interceptLegacyNotifier(t *testing.T, helper string, result error, checkArgs func([]string)) {
	t.Helper()
	previous := runLegacyNotifierCommand
	called := 0
	runLegacyNotifierCommand = func(timeout time.Duration, notifierPath string, args ...string) ([]byte, error) {
		called++
		if timeout != 30*time.Second || notifierPath != helper {
			t.Fatalf("legacy invocation = %s %q, want 30s %q", timeout, notifierPath, helper)
		}
		checkArgs(args)
		return []byte("helper diagnostic"), result
	}
	t.Cleanup(func() {
		runLegacyNotifierCommand = previous
		if called != 1 {
			t.Errorf("legacy helper invoked %d times, want once", called)
		}
	})
}

func assertLegacyFallbackResult(t *testing.T, got, helperErr error, called int, wantFallback bool) {
	t.Helper()
	wantCalls := 0
	if wantFallback {
		wantCalls = 1
	}
	if called != wantCalls {
		t.Errorf("fallback invoked %d times, want %d", called, wantCalls)
	}
	if helperErr == nil || wantFallback {
		if got != nil {
			t.Fatalf("successful delivery returned %v", got)
		}
	} else if !errors.Is(got, helperErr) {
		t.Fatalf("helper error lost: got %v, want %v", got, helperErr)
	}
}
