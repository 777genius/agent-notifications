package notifier

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/beeep"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

func TestExtractSessionInfo(t *testing.T) {
	tests := []struct {
		name             string
		message          string
		expectedSession  string
		expectedBranch   string
		expectedCleanMsg string
	}{
		{
			name:             "Valid session name with message",
			message:          "[boldcat] 📝 3 new  ✏️ 2 edited  ⏱ 2m 15s",
			expectedSession:  "boldcat",
			expectedBranch:   "",
			expectedCleanMsg: "📝 3 new  ✏️ 2 edited  ⏱ 2m 15s",
		},
		{
			name:             "Session name with git branch",
			message:          "[boldcat|main] 📝 3 new",
			expectedSession:  "boldcat",
			expectedBranch:   "main",
			expectedCleanMsg: "📝 3 new",
		},
		{
			name:             "Session with feature branch",
			message:          "[swifteagle|feature/auth] Task complete",
			expectedSession:  "swifteagle",
			expectedBranch:   "feature/auth",
			expectedCleanMsg: "Task complete",
		},
		{
			name:             "Valid session name with short message",
			message:          "[swifteagle] Task complete",
			expectedSession:  "swifteagle",
			expectedBranch:   "",
			expectedCleanMsg: "Task complete",
		},
		{
			name:             "Message without session name",
			message:          "Task completed successfully",
			expectedSession:  "",
			expectedBranch:   "",
			expectedCleanMsg: "Task completed successfully",
		},
		{
			name:             "Message with only opening bracket",
			message:          "[no-closing-bracket Task complete",
			expectedSession:  "",
			expectedBranch:   "",
			expectedCleanMsg: "[no-closing-bracket Task complete",
		},
		{
			name:             "Empty message",
			message:          "",
			expectedSession:  "",
			expectedBranch:   "",
			expectedCleanMsg: "",
		},
		{
			name:             "Session name with extra spaces",
			message:          "[cool-fox]   Multiple   spaces   message",
			expectedSession:  "cool-fox",
			expectedBranch:   "",
			expectedCleanMsg: "Multiple   spaces   message",
		},
		{
			name:             "Session name only (no message)",
			message:          "[lonely-wolf]",
			expectedSession:  "lonely-wolf",
			expectedBranch:   "",
			expectedCleanMsg: "",
		},
		{
			name:             "Leading/trailing spaces",
			message:          "  [trim-test] Message with spaces  ",
			expectedSession:  "trim-test",
			expectedBranch:   "",
			expectedCleanMsg: "Message with spaces",
		},
		{
			name:             "Session with branch only (no message)",
			message:          "[test-session|develop]",
			expectedSession:  "test-session",
			expectedBranch:   "develop",
			expectedCleanMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, branch, cleanMsg := extractSessionInfo(tt.message)
			if session != tt.expectedSession {
				t.Errorf("extractSessionInfo(%q) session = %q, want %q", tt.message, session, tt.expectedSession)
			}
			if branch != tt.expectedBranch {
				t.Errorf("extractSessionInfo(%q) branch = %q, want %q", tt.message, branch, tt.expectedBranch)
			}
			if cleanMsg != tt.expectedCleanMsg {
				t.Errorf("extractSessionInfo(%q) cleanMsg = %q, want %q", tt.message, cleanMsg, tt.expectedCleanMsg)
			}
		})
	}
}

func TestSendDesktopRestoresAppName(t *testing.T) {
	// This test verifies that SendDesktop properly restores beeep.AppName
	// after sending a notification, even if the notification fails.

	// Save original AppName
	originalAppName := beeep.AppName
	defer func() {
		beeep.AppName = originalAppName
	}()

	// Set a test value
	testAppName := "test-app-name"
	beeep.AppName = testAppName

	// Create notifier with desktop notifications disabled to skip actual notification
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = false
	n := New(cfg)

	// Call SendDesktop - should not change AppName since notifications are disabled
	_ = n.SendDesktop(analyzer.StatusTaskComplete, "test message", "", "")

	// Verify AppName is unchanged (because we skipped notification)
	if beeep.AppName != testAppName {
		t.Errorf("AppName changed unexpectedly: got %q, want %q", beeep.AppName, testAppName)
	}

	// Now test with enabled notifications (will attempt real notification)
	cfg.Notifications.Desktop.Enabled = true
	beeep.AppName = testAppName

	// This will attempt to send a real notification and may fail in CI,
	// but the important thing is that AppName is restored afterward
	_ = n.SendDesktop(analyzer.StatusTaskComplete, "test message", "", "")

	// Verify AppName is restored to testAppName after the defer runs
	if beeep.AppName != testAppName {
		t.Errorf("AppName not restored after SendDesktop: got %q, want %q", beeep.AppName, testAppName)
	}
}

// === Tests for Click-to-Focus functionality ===

func TestSendDesktop_ClickToFocusDisabled(t *testing.T) {
	// When ClickToFocus is disabled, should use beeep even on macOS
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.Sound = false // Disable sound for faster test

	n := New(cfg)

	// Should not panic and should use beeep path
	// We can't easily verify which path was taken without mocking,
	// but we can verify it doesn't crash
	err := n.SendDesktop(analyzer.StatusTaskComplete, "[test-session] Task done", "", "")
	// Error is acceptable in CI environment where notifications may not work
	_ = err
}

func TestSendDesktop_WithTerminalBundleIDOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.TerminalBundleID = "com.custom.terminal"
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// Verify the config is properly set
	if n.cfg.Notifications.Desktop.TerminalBundleID != "com.custom.terminal" {
		t.Errorf("TerminalBundleID not set correctly: got %s", n.cfg.Notifications.Desktop.TerminalBundleID)
	}

	// SendDesktop should work without panic
	err := n.SendDesktop(analyzer.StatusTaskComplete, "Test message", "", "")
	_ = err // Error acceptable in CI
}

func TestPlaySoundAsync_Disabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// Should not start any goroutine when sound is disabled
	n.playSoundAsync("")
	n.playSoundAsync("nonexistent.mp3")

	// Close should complete quickly since no sound was playing
	err := n.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestPlaySoundAsync_EmptyPath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = true

	n := New(cfg)

	// Empty sound path should not start playback
	n.playSoundAsync("")

	// Close should complete quickly
	err := n.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestSendWithBeeep_RestoresAppName(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// Save original AppName
	originalAppName := beeep.AppName
	testAppName := "test-restore-check"
	beeep.AppName = testAppName

	// Call sendWithBeeep
	_ = n.sendWithBeeep("Test Title", "Test Message", "", "")

	// AppName should be restored
	if beeep.AppName != testAppName {
		t.Errorf("AppName not restored: got %q, want %q", beeep.AppName, testAppName)
	}

	// Restore original
	beeep.AppName = originalAppName
}

func TestSendWithBeeep_WindowsToastFallbackSuccess(t *testing.T) {
	withNotifierGOOS(t, "windows")
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false
	n := New(cfg)

	originalAppName := beeep.AppName
	testAppName := "test-windows-fallback"
	beeep.AppName = testAppName
	t.Cleanup(func() {
		beeep.AppName = originalAppName
	})

	var called bool
	withBeeepNotify(t, func(title, message string, icon any) error {
		called = true
		if beeep.AppName != "Claude Code Notifications" {
			t.Fatalf("beeep.AppName during Notify = %q, want fixed Windows app name", beeep.AppName)
		}
		return errors.Join(fmt.Errorf("doc.LoadXml(tmpl): error 3222070623"))
	})

	err := n.sendWithBeeep("🔍 Review", "Test Message", "", "")
	if err != nil {
		t.Fatalf("sendWithBeeep returned %v, want nil for successful PowerShell fallback", err)
	}
	if !called {
		t.Fatal("beeepNotify was not called")
	}
	if beeep.AppName != testAppName {
		t.Errorf("beeep.AppName not restored: got %q, want %q", beeep.AppName, testAppName)
	}
}

func TestSendWithBeeep_WindowsToastFallbackFailure(t *testing.T) {
	withNotifierGOOS(t, "windows")
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false
	n := New(cfg)

	withBeeepNotify(t, func(title, message string, icon any) error {
		return errors.Join(
			fmt.Errorf("doc.LoadXml(tmpl): error 3222070623"),
			fmt.Errorf("executing powershell: exit status 1"),
		)
	})

	err := n.sendWithBeeep("🔍 Review", "Test Message", "", "")
	if err == nil {
		t.Fatal("sendWithBeeep returned nil, want error when COM and PowerShell fallback both fail")
	}
}

func TestIsWindowsToastFallbackSuccess(t *testing.T) {
	tests := []struct {
		name string
		goos string
		err  error
		want bool
	}{
		{
			name: "windows joined loadxml only",
			goos: "windows",
			err:  errors.Join(fmt.Errorf("doc.LoadXml(tmpl): error 3222070623")),
			want: true,
		},
		{
			name: "windows joined loadxml plus powershell failure",
			goos: "windows",
			err: errors.Join(
				fmt.Errorf("doc.LoadXml(tmpl): error 3222070623"),
				fmt.Errorf("executing powershell: exit status 1"),
			),
			want: false,
		},
		{
			name: "windows non joined loadxml",
			goos: "windows",
			err:  fmt.Errorf("doc.LoadXml(tmpl): error 3222070623"),
			want: false,
		},
		{
			name: "darwin joined loadxml only",
			goos: "darwin",
			err:  errors.Join(fmt.Errorf("doc.LoadXml(tmpl): error 3222070623")),
			want: false,
		},
		{
			name: "windows unrelated error",
			goos: "windows",
			err:  errors.Join(fmt.Errorf("notifier.Show(): access denied")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withNotifierGOOS(t, tt.goos)
			if got := isWindowsToastFallbackSuccess(tt.err); got != tt.want {
				t.Errorf("isWindowsToastFallbackSuccess() = %v, want %v", got, tt.want)
			}
		})
	}
}

func withNotifierGOOS(t *testing.T, goos string) {
	t.Helper()
	old := notifierGOOS
	notifierGOOS = goos
	t.Cleanup(func() {
		notifierGOOS = old
	})
}

func withBeeepNotify(t *testing.T, fn func(title, message string, icon any) error) {
	t.Helper()
	old := beeepNotify
	beeepNotify = fn
	t.Cleanup(func() {
		beeepNotify = old
	})
}

func TestNotifier_NewWithClickToFocusConfig(t *testing.T) {
	tests := []struct {
		name         string
		clickToFocus bool
		bundleID     string
	}{
		{"ClickToFocus enabled, auto-detect", true, ""},
		{"ClickToFocus enabled, custom bundle", true, "com.custom.app"},
		{"ClickToFocus disabled", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Notifications.Desktop.ClickToFocus = tt.clickToFocus
			cfg.Notifications.Desktop.TerminalBundleID = tt.bundleID

			n := New(cfg)

			if n.cfg.Notifications.Desktop.ClickToFocus != tt.clickToFocus {
				t.Errorf("ClickToFocus = %v, want %v", n.cfg.Notifications.Desktop.ClickToFocus, tt.clickToFocus)
			}
			if n.cfg.Notifications.Desktop.TerminalBundleID != tt.bundleID {
				t.Errorf("TerminalBundleID = %q, want %q", n.cfg.Notifications.Desktop.TerminalBundleID, tt.bundleID)
			}
		})
	}
}

func TestSendDesktop_AllStatuses(t *testing.T) {
	// Test that all status types work with click-to-focus config
	statuses := []analyzer.Status{
		analyzer.StatusTaskComplete,
		analyzer.StatusReviewComplete,
		analyzer.StatusQuestion,
		analyzer.StatusPlanReady,
		analyzer.StatusSessionLimitReached,
		analyzer.StatusAPIError,
	}

	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false // Disable sound for faster tests

	n := New(cfg)

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			// Should not panic for any status
			err := n.SendDesktop(status, "[test] Message for "+string(status), "test-session", "")
			// Error is acceptable (notifications may not work in CI)
			_ = err
		})
	}
}

func TestSendDesktop_Disabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = false

	n := New(cfg)

	// Should return nil without doing anything
	err := n.SendDesktop(analyzer.StatusTaskComplete, "test message", "", "")
	if err != nil {
		t.Errorf("Expected nil error when disabled, got: %v", err)
	}
}

func TestSendDesktop_UnknownStatus(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true

	n := New(cfg)

	// Should return error for unknown status
	err := n.SendDesktop(analyzer.Status("unknown_status"), "test message", "", "")
	if err == nil {
		t.Error("Expected error for unknown status, got nil")
	}
}

func TestSendDesktop_WithSessionName(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false

	n := New(cfg)

	// Test with session name
	err := n.SendDesktop(analyzer.StatusTaskComplete, "[my-session] Task completed", "", "")
	// Error acceptable in CI
	_ = err
}

func TestSendDesktop_WithoutSessionName(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false

	n := New(cfg)

	// Test without session name
	err := n.SendDesktop(analyzer.StatusTaskComplete, "Task completed without session", "", "")
	// Error acceptable in CI
	_ = err
}

func TestNotifier_Close_MultipleCallsSafe(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// Close should be safe to call multiple times
	err1 := n.Close()
	err2 := n.Close()

	if err1 != nil {
		t.Errorf("First Close() returned error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Second Close() returned error: %v", err2)
	}
}

func TestNotifier_CloseWithoutPlayback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// Close without any sound playback should complete immediately
	done := make(chan struct{})
	go func() {
		n.Close()
		close(done)
	}()

	select {
	case <-done:
		// Expected
	case <-time.After(time.Second):
		t.Error("Close() took too long")
	}
}

func TestExtractSessionInfo_MoreCases(t *testing.T) {
	tests := []struct {
		name             string
		message          string
		expectedSession  string
		expectedBranch   string
		expectedCleanMsg string
	}{
		{
			name:             "Nested brackets",
			message:          "[outer] message with [inner] brackets",
			expectedSession:  "outer",
			expectedBranch:   "",
			expectedCleanMsg: "message with [inner] brackets",
		},
		{
			name:             "Multiple brackets at start",
			message:          "[first][second] message",
			expectedSession:  "first",
			expectedBranch:   "",
			expectedCleanMsg: "[second] message",
		},
		{
			name:             "Bracket in middle",
			message:          "message [not-session] here",
			expectedSession:  "",
			expectedBranch:   "",
			expectedCleanMsg: "message [not-session] here",
		},
		{
			name:             "Only brackets with text",
			message:          "[]",
			expectedSession:  "",
			expectedBranch:   "",
			expectedCleanMsg: "",
		},
		{
			name:             "Hyphenated session name",
			message:          "[bold-red-fox] Long message here",
			expectedSession:  "bold-red-fox",
			expectedBranch:   "",
			expectedCleanMsg: "Long message here",
		},
		{
			name:             "Underscored session name",
			message:          "[session_with_underscores] Message",
			expectedSession:  "session_with_underscores",
			expectedBranch:   "",
			expectedCleanMsg: "Message",
		},
		{
			name:             "Numeric session name",
			message:          "[session123] Message",
			expectedSession:  "session123",
			expectedBranch:   "",
			expectedCleanMsg: "Message",
		},
		{
			name:             "Session with branch containing slash",
			message:          "[test-session|feature/new-feature] Work in progress",
			expectedSession:  "test-session",
			expectedBranch:   "feature/new-feature",
			expectedCleanMsg: "Work in progress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, branch, cleanMsg := extractSessionInfo(tt.message)
			if session != tt.expectedSession {
				t.Errorf("extractSessionInfo(%q) session = %q, want %q", tt.message, session, tt.expectedSession)
			}
			if branch != tt.expectedBranch {
				t.Errorf("extractSessionInfo(%q) branch = %q, want %q", tt.message, branch, tt.expectedBranch)
			}
			if cleanMsg != tt.expectedCleanMsg {
				t.Errorf("extractSessionInfo(%q) cleanMsg = %q, want %q", tt.message, cleanMsg, tt.expectedCleanMsg)
			}
		})
	}
}

func TestPlaySoundAsync_WithSoundFile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = true

	n := New(cfg)

	// Playing nonexistent sound should not panic
	n.playSoundAsync("/nonexistent/path/to/sound.mp3")

	// Wait for goroutine to complete
	n.Close()
}

func TestSendDesktop_ClickToFocusWithBeeepFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.TerminalBundleID = "" // auto-detect

	n := New(cfg)

	// Should work regardless of terminal-notifier availability
	// Will use terminal-notifier if available, otherwise beeep
	err := n.SendDesktop(analyzer.StatusTaskComplete, "[fallback-test] Testing fallback", "", "")
	// Error acceptable in CI where neither may work
	_ = err
}

func TestNotifier_ConfigAccess(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.TerminalBundleID = "custom.bundle"
	cfg.Notifications.Desktop.Volume = 0.5

	n := New(cfg)

	// Verify config is accessible
	if !n.cfg.Notifications.Desktop.Enabled {
		t.Error("Expected Desktop.Enabled to be true")
	}
	if !n.cfg.Notifications.Desktop.ClickToFocus {
		t.Error("Expected Desktop.ClickToFocus to be true")
	}
	if n.cfg.Notifications.Desktop.TerminalBundleID != "custom.bundle" {
		t.Errorf("Expected TerminalBundleID 'custom.bundle', got '%s'", n.cfg.Notifications.Desktop.TerminalBundleID)
	}
	if n.cfg.Notifications.Desktop.Volume != 0.5 {
		t.Errorf("Expected Volume 0.5, got %f", n.cfg.Notifications.Desktop.Volume)
	}
}

// === Tests for buildTerminalNotifierArgs ===

func TestBuildTerminalNotifierArgs_Basic(t *testing.T) {
	args := buildTerminalNotifierArgs("Test Title", "Test Message", "com.test.app", "", true)

	// Check required arguments
	if !containsArg(args, "-title", "Test Title") {
		t.Error("Missing or incorrect -title argument")
	}
	if !containsArg(args, "-message", "Test Message") {
		t.Error("Missing or incorrect -message argument")
	}
	if !containsArg(args, "-activate", "com.test.app") {
		t.Error("Missing or incorrect -activate argument")
	}

	// Note: -sender was removed because it conflicts with -activate on macOS Sequoia

	// Check that -group is present (for deduplication)
	hasGroup := false
	for _, arg := range args {
		if arg == "-group" {
			hasGroup = true
			break
		}
	}
	if !hasGroup {
		t.Error("Missing -group argument")
	}
}

func TestBuildTerminalNotifierArgs_NoSender(t *testing.T) {
	// -sender was removed because it conflicts with -activate on macOS Sequoia (15.x)
	// This test verifies that -sender is NOT present
	args := buildTerminalNotifierArgs("Title", "Message", "com.test.app", "", true)

	for _, arg := range args {
		if arg == "-sender" {
			t.Error("-sender should not be present (conflicts with -activate on macOS Sequoia)")
		}
	}
}

func TestBuildTerminalNotifierArgs_SpecialCharacters(t *testing.T) {
	// Test with special characters in title/message
	args := buildTerminalNotifierArgs(
		"Task Complete [session-1]",
		"📝 3 new  ✏️ 2 edited  ⏱ 2m 15s",
		"com.googlecode.iterm2",
		"",
		true,
	)

	if !containsArg(args, "-title", "Task Complete [session-1]") {
		t.Error("Title with special characters not preserved")
	}
	if !containsArg(args, "-message", "📝 3 new  ✏️ 2 edited  ⏱ 2m 15s") {
		t.Error("Message with special characters not preserved")
	}
}

func TestBuildTerminalNotifierArgs_EmptyValues(t *testing.T) {
	// Test with empty title/message (edge case)
	args := buildTerminalNotifierArgs("", "", "com.test.app", "", true)

	if !containsArg(args, "-title", "") {
		t.Error("Empty title should still be present")
	}
	if !containsArg(args, "-message", "") {
		t.Error("Empty message should still be present")
	}
}

func TestBuildTerminalNotifierArgs_UniqueGroupID(t *testing.T) {
	// Two calls should produce different group IDs
	args1 := buildTerminalNotifierArgs("Title", "Msg", "com.test", "", true)
	time.Sleep(time.Nanosecond) // Ensure different timestamp
	args2 := buildTerminalNotifierArgs("Title", "Msg", "com.test", "", true)

	group1 := getArgValue(args1, "-group")
	group2 := getArgValue(args2, "-group")

	if group1 == "" || group2 == "" {
		t.Error("Group ID should not be empty")
	}
	if group1 == group2 {
		t.Error("Group IDs should be unique between calls")
	}
}

// NOTE: TestSendWithTerminalNotifier_Integration and TestTerminalNotifier_CommandExecution
// are in notifier_darwin_integration_test.go (require setupClaudeNotifierEnv which is darwin-only)

// === Fallback logic tests ===

func TestSendDesktop_FallbackWhenTerminalNotifierFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-only test")
	}

	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false
	// Use invalid bundle ID to test error handling
	cfg.Notifications.Desktop.TerminalBundleID = "com.nonexistent.app.12345"

	n := New(cfg)

	// Should not panic. On macOS we no longer fall back to beeep/osascript.
	err := n.SendDesktop(analyzer.StatusTaskComplete, "[test] Fallback test", "", "")
	// Error is acceptable in CI, but should not panic
	_ = err
}

func TestSendDesktop_ClickToFocusDisabledStillDoesNotPanic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = false // Disabled
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// Click-to-focus disabled should still avoid panics.
	err := n.SendDesktop(analyzer.StatusTaskComplete, "[test] Beeep path test", "", "")
	// Error acceptable in CI
	_ = err
}

func TestClaudeNotifierAppPath_RecognizesBundleExecutable(t *testing.T) {
	notifierPath := filepath.Join(
		string(filepath.Separator),
		"tmp",
		"plugin",
		"bin",
		"ClaudeNotifier.app",
		"Contents",
		"MacOS",
		"terminal-notifier-modern",
	)

	appPath, ok := claudeNotifierAppPath(notifierPath)
	if !ok {
		t.Fatal("Expected ClaudeNotifier path to be recognized")
	}
	if filepath.Base(appPath) != "ClaudeNotifier.app" {
		t.Fatalf("Expected app path to end with ClaudeNotifier.app, got %s", appPath)
	}
}

func TestClaudeNotifierAppPath_IgnoresLegacyBinary(t *testing.T) {
	notifierPath := filepath.Join(string(filepath.Separator), "usr", "local", "bin", "terminal-notifier")
	if _, ok := claudeNotifierAppPath(notifierPath); ok {
		t.Fatalf("Legacy terminal-notifier path should not be treated as ClaudeNotifier.app: %s", notifierPath)
	}
}

func TestBuildNotifierCommand_UsesOpenForClaudeNotifier(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-specific LaunchServices command test")
	}

	notifierPath := filepath.Join(
		string(filepath.Separator),
		"tmp",
		"plugin",
		"bin",
		"ClaudeNotifier.app",
		"Contents",
		"MacOS",
		"terminal-notifier-modern",
	)

	cmd := buildNotifierCommand(notifierPath, []string{"-title", "Test", "-message", "Hello"})
	commandBase := strings.ToLower(filepath.Base(cmd.Path))
	if commandBase != "open" && commandBase != "open.exe" {
		t.Fatalf("Expected open command for ClaudeNotifier.app, got %s", cmd.Path)
	}

	if len(cmd.Args) < 9 {
		t.Fatalf("Expected open command args with --args payload, got: %v", cmd.Args)
	}
	if cmd.Args[1] != "-W" || cmd.Args[2] != "-n" || cmd.Args[3] != "-g" {
		t.Fatalf("Expected open -W -n -g flags, got: %v", cmd.Args)
	}
	if cmd.Args[5] != "--args" {
		t.Fatalf("Expected --args marker, got: %v", cmd.Args)
	}
	if cmd.Args[6] != "-launchedViaLaunchServices" {
		t.Fatalf("Expected LaunchServices marker arg, got: %v", cmd.Args)
	}
}

func TestBuildNotifierCommand_UsesDirectBinaryForLegacy(t *testing.T) {
	notifierPath := filepath.Join(string(filepath.Separator), "usr", "local", "bin", "terminal-notifier")
	cmd := buildNotifierCommand(notifierPath, []string{"-title", "Test"})
	if cmd.Path != notifierPath {
		t.Fatalf("Expected direct notifier path, got %s", cmd.Path)
	}
	if len(cmd.Args) < 2 || cmd.Args[0] != notifierPath {
		t.Fatalf("Unexpected command args: %v", cmd.Args)
	}
}

func TestRunClaudeNotifierApp_PermissionDeniedError(t *testing.T) {
	restoreExecCommand := installFakeOpen(t, fmt.Sprintf("Error: %s", macOSPermissionDeniedMessage), 0)
	defer restoreExecCommand()

	err := runClaudeNotifierApp("/tmp/ClaudeNotifier.app", []string{"-title", "Test"})
	if err == nil {
		t.Fatal("expected permission denied error, got nil")
	}

	var permissionErr *NotificationPermissionDeniedError
	if !errors.As(err, &permissionErr) {
		t.Fatalf("expected NotificationPermissionDeniedError, got %T: %v", err, err)
	}
}

func TestRunClaudeNotifierApp_ReportsGenericStderr(t *testing.T) {
	restoreExecCommand := installFakeOpen(t, "Error: unexpected notifier failure", 0)
	defer restoreExecCommand()

	err := runClaudeNotifierApp("/tmp/ClaudeNotifier.app", []string{"-title", "Test"})
	if err == nil {
		t.Fatal("expected generic notifier error, got nil")
	}
	if strings.Contains(err.Error(), macOSPermissionDeniedMessage) {
		t.Fatalf("did not expect permission denied classification, got %v", err)
	}
	if !strings.Contains(err.Error(), "ClaudeNotifier reported an error") {
		t.Fatalf("expected generic stderr error, got %v", err)
	}
}

// === Helper functions ===

func installFakeOpen(t *testing.T, stderrMessage string, exitCode int) func() {
	t.Helper()

	originalExecCommand := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestHelperProcess", "--", name}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDERR="+stderrMessage,
			"GO_HELPER_EXIT_CODE="+strconv.Itoa(exitCode),
		)
		return cmd
	}

	return func() {
		execCommand = originalExecCommand
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	doubleDash := -1
	for i, arg := range args {
		if arg == "--" {
			doubleDash = i
			break
		}
	}
	if doubleDash == -1 || doubleDash+1 >= len(args) {
		os.Exit(97)
	}

	helperArgs := args[doubleDash+2:]
	var stdoutPath string
	var stderrPath string
	for i := 0; i < len(helperArgs); i++ {
		switch helperArgs[i] {
		case "-o":
			if i+1 < len(helperArgs) {
				stdoutPath = helperArgs[i+1]
				i++
			}
		case "--stderr":
			if i+1 < len(helperArgs) {
				stderrPath = helperArgs[i+1]
				i++
			}
		}
	}

	if stdoutPath != "" {
		_ = os.WriteFile(stdoutPath, []byte{}, 0o644)
	}
	if stderrPath != "" {
		_ = os.WriteFile(stderrPath, []byte(os.Getenv("GO_HELPER_STDERR")), 0o644)
	}
	if stderrText := os.Getenv("GO_HELPER_STDERR"); stderrText != "" {
		_, _ = os.Stderr.WriteString(stderrText)
	}
	if stdoutText := os.Getenv("GO_HELPER_STDOUT"); stdoutText != "" {
		_, _ = os.Stdout.WriteString(stdoutText)
	}

	exitCode, err := strconv.Atoi(os.Getenv("GO_HELPER_EXIT_CODE"))
	if err != nil {
		exitCode = 0
	}
	os.Exit(exitCode)
}

func containsArg(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func getArgValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// === Tests for terminal-notifier argument validation ===

func TestBuildTerminalNotifierArgs_ArgumentOrder(t *testing.T) {
	args := buildTerminalNotifierArgs("Title", "Message", "com.test.app", "", true)

	// Verify argument structure: each flag should be followed by its value
	// Note: -sender was removed because it conflicts with -activate on macOS Sequoia
	expectedPairs := map[string]string{
		"-title":    "Title",
		"-message":  "Message",
		"-activate": "com.test.app",
	}

	for flag, expectedValue := range expectedPairs {
		actualValue := getArgValue(args, flag)
		if actualValue != expectedValue {
			t.Errorf("For flag %s: expected %q, got %q", flag, expectedValue, actualValue)
		}
	}
}

func TestBuildTerminalNotifierArgs_NoNilValues(t *testing.T) {
	args := buildTerminalNotifierArgs("Title", "Message", "com.test", "", true)

	for i, arg := range args {
		if arg == "" && i > 0 && args[i-1] != "-title" && args[i-1] != "-message" {
			// Empty values are only acceptable for -title and -message
			t.Errorf("Unexpected empty value at index %d after %s", i, args[i-1])
		}
	}
}

func TestBuildTerminalNotifierArgs_GroupIDFormat(t *testing.T) {
	args := buildTerminalNotifierArgs("Title", "Message", "com.test", "", true)

	groupID := getArgValue(args, "-group")
	if groupID == "" {
		t.Fatal("Group ID is empty")
	}

	// Group ID should start with "claude-notif-"
	if !strings.HasPrefix(groupID, "claude-notif-") {
		t.Errorf("Group ID should start with 'claude-notif-', got: %s", groupID)
	}
}

func TestBuildTerminalNotifierArgs_ClickToFocusDisabledOmitsAction(t *testing.T) {
	args := buildTerminalNotifierArgs("Title", "Message", "com.test.app", "/tmp/project", false)

	if getArgValue(args, "-activate") != "" {
		t.Fatalf("Did not expect -activate when click-to-focus is disabled, got: %v", args)
	}
	if getArgValue(args, "-execute") != "" {
		t.Fatalf("Did not expect -execute when click-to-focus is disabled, got: %v", args)
	}
}

// === Additional coverage tests ===

func TestSendWithTerminalNotifier_PathNotFound(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping macOS-only test")
	}

	// Save and restore CLAUDE_PLUGIN_ROOT
	originalPluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT")
	defer os.Setenv("CLAUDE_PLUGIN_ROOT", originalPluginRoot)

	// Set invalid plugin root to force path lookup to fail (if system doesn't have it)
	os.Setenv("CLAUDE_PLUGIN_ROOT", "/nonexistent/path/12345")

	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false

	n := New(cfg)

	// This may succeed if terminal-notifier is installed system-wide
	// or fail if not - both are valid outcomes
	err := n.sendWithTerminalNotifier("Test", "Message", "", "", false, "", true)
	_ = err // We just want to exercise the code path
}

func TestSendDesktop_AppIconNotFound(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.AppIcon = "/nonexistent/icon/path.png"

	n := New(cfg)

	// Should handle missing icon gracefully
	err := n.SendDesktop(analyzer.StatusTaskComplete, "[test] Icon test", "", "")
	// Error acceptable in CI
	_ = err
}

func TestSendDesktop_EmptyMessage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false

	n := New(cfg)

	// Empty message should still work
	err := n.SendDesktop(analyzer.StatusTaskComplete, "", "", "")
	// Error acceptable in CI
	_ = err
}

func TestSendDesktop_VeryLongMessage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false

	n := New(cfg)

	// Very long message
	longMessage := "[test-session] " + strings.Repeat("This is a very long message. ", 100)
	err := n.SendDesktop(analyzer.StatusTaskComplete, longMessage, "", "")
	// Error acceptable in CI
	_ = err
}

func TestSendDesktop_SpecialCharactersInMessage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false

	n := New(cfg)

	// Message with special characters
	specialMessage := "[test] Message with \"quotes\", 'apostrophes', <brackets>, & ampersand, \n newline"
	err := n.SendDesktop(analyzer.StatusTaskComplete, specialMessage, "", "")
	// Error acceptable in CI
	_ = err
}

func TestSendDesktop_UnicodeMessage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.Desktop.ClickToFocus = false

	n := New(cfg)

	// Unicode message
	unicodeMessage := "[тест] Сообщение на русском 你好 🎉 émojis"
	err := n.SendDesktop(analyzer.StatusTaskComplete, unicodeMessage, "", "")
	// Error acceptable in CI
	_ = err
}

func TestExtractSessionInfo_Unicode(t *testing.T) {
	tests := []struct {
		message          string
		expectedSession  string
		expectedBranch   string
		expectedCleanMsg string
	}{
		{"[тест-сессия] Сообщение", "тест-сессия", "", "Сообщение"},
		{"[日本語] Japanese text", "日本語", "", "Japanese text"},
		{"[émoji-🎉] Fun message", "émoji-🎉", "", "Fun message"},
		{"[тест|ветка] С веткой", "тест", "ветка", "С веткой"},
	}

	for _, tt := range tests {
		session, branch, cleanMsg := extractSessionInfo(tt.message)
		if session != tt.expectedSession {
			t.Errorf("extractSessionInfo(%q) session = %q, want %q", tt.message, session, tt.expectedSession)
		}
		if branch != tt.expectedBranch {
			t.Errorf("extractSessionInfo(%q) branch = %q, want %q", tt.message, branch, tt.expectedBranch)
		}
		if cleanMsg != tt.expectedCleanMsg {
			t.Errorf("extractSessionInfo(%q) cleanMsg = %q, want %q", tt.message, cleanMsg, tt.expectedCleanMsg)
		}
	}
}

// Note: Concurrent SendDesktop is not tested because beeep.AppName is a global
// variable and the beeep library is not thread-safe. In practice, notifications
// are sent sequentially from hooks, so this is not a real use case.

func TestNotifier_RapidClose(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false

	// Create and close rapidly multiple times
	for i := 0; i < 10; i++ {
		n := New(cfg)
		_ = n.Close()
	}
}

func TestSendTerminalBell_DoesNotPanic(t *testing.T) {
	// sendTerminalBell should never panic regardless of TTY availability.
	// In CI there is no /dev/tty so the open will fail silently;
	// in a real terminal the BEL character is written.
	sendTerminalBell()
}

func TestSendDesktop_CallsBell(t *testing.T) {
	// Verify SendDesktop does not panic when bell is enabled (default)
	// and desktop notifications are disabled.
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = false

	n := New(cfg)

	// Should not panic — bell is sent, then returns nil for disabled desktop
	err := n.SendDesktop(analyzer.StatusTaskComplete, "test message", "", "")
	if err != nil {
		t.Errorf("Expected nil error when disabled, got: %v", err)
	}
}

func TestSendDesktop_BellDisabledByConfig(t *testing.T) {
	// Verify SendDesktop respects terminalBell=false config.
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = false
	bellOff := false
	cfg.Notifications.Desktop.TerminalBell = &bellOff

	n := New(cfg)

	// Should not panic — bell is skipped, then returns nil for disabled desktop
	err := n.SendDesktop(analyzer.StatusTaskComplete, "test message", "", "")
	if err != nil {
		t.Errorf("Expected nil error when disabled, got: %v", err)
	}
}

func TestBuildTerminalNotifierArgs_AllKnownBundleIDs(t *testing.T) {
	// Without cwd, all bundle IDs should use -activate (existing behavior)
	bundleIDs := []string{
		"com.apple.Terminal",
		"com.googlecode.iterm2",
		"dev.warp.Warp-Stable",
		"net.kovidgoyal.kitty",
		"com.mitchellh.ghostty",
		"com.github.wez.wezterm",
		"org.alacritty",
		"co.zeit.hyper",
		"com.microsoft.VSCode",
		"com.todesktop.230313mzl4w4u92", // Cursor
	}

	for _, bundleID := range bundleIDs {
		args := buildTerminalNotifierArgs("Title", "Message", bundleID, "", true)
		actualBundleID := getArgValue(args, "-activate")
		if actualBundleID != bundleID {
			t.Errorf("Bundle ID mismatch: expected %s, got %s", bundleID, actualBundleID)
		}
	}
}

// === Tests for isTimeSensitiveStatus ===

func TestIsTimeSensitiveStatus(t *testing.T) {
	tests := []struct {
		status   analyzer.Status
		expected bool
	}{
		{analyzer.StatusAPIError, true},
		{analyzer.StatusAPIErrorOverloaded, true},
		{analyzer.StatusSessionLimitReached, true},
		{analyzer.StatusTaskComplete, false},
		{analyzer.StatusReviewComplete, false},
		{analyzer.StatusQuestion, false},
		{analyzer.StatusPlanReady, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			got := isTimeSensitiveStatus(tt.status)
			if got != tt.expected {
				t.Errorf("isTimeSensitiveStatus(%s) = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

// === Tests for subtitle building ===

func TestSendDesktop_SubtitleFromBranchAndFolder(t *testing.T) {
	// Verify that subtitle is correctly extracted from the "[session|branch folder] message" format
	_, gitBranch, _ := extractSessionInfo("[peak|main notification_plugin_go] Task complete")
	if gitBranch != "main notification_plugin_go" {
		t.Errorf("expected gitBranch='main notification_plugin_go', got %q", gitBranch)
	}

	// Verify subtitle construction: "main · notification_plugin_go"
	parts := strings.SplitN(gitBranch, " ", 2)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	subtitle := parts[0] + " \u00B7 " + parts[1]
	if subtitle != "main · notification_plugin_go" {
		t.Errorf("subtitle = %q, want 'main · notification_plugin_go'", subtitle)
	}
}

func TestSendDesktop_SubtitleBranchOnly(t *testing.T) {
	// When git branch has no space (no folder), subtitle is just the branch
	_, gitBranch, _ := extractSessionInfo("[peak|develop] Task complete")
	if gitBranch != "develop" {
		t.Errorf("expected gitBranch='develop', got %q", gitBranch)
	}

	parts := strings.SplitN(gitBranch, " ", 2)
	if len(parts) != 1 {
		t.Errorf("expected 1 part for branch-only, got %d", len(parts))
	}
}

// === Tests for buildFocusScript and helpers ===

func TestBuildFocusScript_EmptyCWD(t *testing.T) {
	t.Setenv(iTerm2SessionIDEnv, "")
	script := buildFocusScript("com.microsoft.VSCode", "")
	if script != "" {
		t.Errorf("buildFocusScript with empty cwd should return empty, got: %s", script)
	}
}

func TestBuildFocusScript_VSCode_UsesBinaryCallback(t *testing.T) {
	script := buildFocusScript("com.microsoft.VSCode", "/home/user/my-project")
	// VS Code uses the binary focus-window subcommand, not osascript
	if !strings.Contains(script, "focus-window") {
		t.Errorf("VS Code focus script should use focus-window subcommand, got: %s", script)
	}
	if !strings.Contains(script, "com.microsoft.VSCode") {
		t.Errorf("VS Code focus script should contain bundle ID, got: %s", script)
	}
	if !strings.Contains(script, "/home/user/my-project") {
		t.Errorf("VS Code focus script should contain cwd, got: %s", script)
	}
	if strings.Contains(script, "code --reuse-window") {
		t.Errorf("VS Code focus script should not use code CLI, got: %s", script)
	}
}

func TestBuildFocusScript_VSCodeInsiders_UsesBinaryCallback(t *testing.T) {
	script := buildFocusScript("com.microsoft.VSCodeInsiders", "/home/user/my-project")
	if !strings.Contains(script, "focus-window") {
		t.Errorf("VS Code Insiders focus script should use focus-window subcommand, got: %s", script)
	}
	if !strings.Contains(script, "com.microsoft.VSCodeInsiders") {
		t.Errorf("VS Code Insiders focus script should contain bundle ID, got: %s", script)
	}
	if strings.Contains(script, "osascript") {
		t.Errorf("VS Code Insiders focus script should not use osascript, got: %s", script)
	}
}

func TestBuildFocusScript_Cursor_UsesBinaryCallback(t *testing.T) {
	script := buildFocusScript("com.todesktop.230313mzl4w4u92", "/home/user/my-project")
	if !strings.Contains(script, "focus-window") {
		t.Errorf("Cursor focus script should use focus-window subcommand, got: %s", script)
	}
	if !strings.Contains(script, "com.todesktop.230313mzl4w4u92") {
		t.Errorf("Cursor focus script should contain bundle ID, got: %s", script)
	}
	if !strings.Contains(script, "/home/user/my-project") {
		t.Errorf("Cursor focus script should contain cwd, got: %s", script)
	}
	if strings.Contains(script, "osascript") {
		t.Errorf("Cursor focus script should not use osascript, got: %s", script)
	}
}

func TestBuildFocusScript_Ghostty_UsesFocusWindow(t *testing.T) {
	script := buildFocusScript("com.mitchellh.ghostty", "/home/user/my-project")
	if !strings.Contains(script, "focus-window") {
		t.Errorf("Ghostty focus script should use focus-window subcommand, got: %s", script)
	}
	if !strings.Contains(script, "com.mitchellh.ghostty") {
		t.Errorf("Ghostty focus script should contain bundle ID, got: %s", script)
	}
	if !strings.Contains(script, "/home/user/my-project") {
		t.Errorf("Ghostty focus script should contain cwd, got: %s", script)
	}
	if strings.Contains(script, "osascript") {
		t.Errorf("Ghostty focus script should not use osascript, got: %s", script)
	}
	if strings.Contains(script, "code --reuse-window") {
		t.Errorf("Ghostty focus script should not use code CLI, got: %s", script)
	}
}

func TestBuildFocusScriptWithOptions_Ghostty_IncludesTerminalID(t *testing.T) {
	script := buildFocusScriptWithOptions("com.mitchellh.ghostty", "/home/user/my-project", "terminal-42")
	if !strings.Contains(script, "--ghostty-terminal-id 'terminal-42'") {
		t.Fatalf("Ghostty focus script should pass exact terminal ID, got: %s", script)
	}
}

func TestCwdToFileURL(t *testing.T) {
	tests := []struct {
		cwd      string
		expected string
	}{
		{"/home/user/project", "file:///home/user/project/"},
		{"/home/user/my project", "file:///home/user/my%20project/"},
		{"/path/with#hash", "file:///path/with%23hash/"},
		{"/home/user/100%done", "file:///home/user/100%25done/"},
		{"/home/user/project/", "file:///home/user/project/"},
	}
	for _, tt := range tests {
		got := cwdToFileURL(tt.cwd)
		if got != tt.expected {
			t.Errorf("cwdToFileURL(%q) = %q, want %q", tt.cwd, got, tt.expected)
		}
	}
}

func TestBuildFocusScript_RegularTerminal_UsesFocusWindow(t *testing.T) {
	t.Setenv(iTerm2SessionIDEnv, "")
	clearWarpFocusEnv(t)
	script := buildFocusScript("dev.warp.Warp-Stable", "/home/user/my-project")
	// Regular terminals now use focus-window subcommand instead of AppleScript.
	// This avoids the Automation permission issue on macOS Tahoe (26.x).
	if !strings.Contains(script, "focus-window") {
		t.Errorf("Regular terminal focus script should use focus-window subcommand, got: %s", script)
	}
	if !strings.Contains(script, "dev.warp.Warp-Stable") {
		t.Errorf("Regular terminal focus script should contain bundle ID, got: %s", script)
	}
	if !strings.Contains(script, "/home/user/my-project") {
		t.Errorf("Regular terminal focus script should contain cwd, got: %s", script)
	}
	if strings.Contains(script, "osascript") {
		t.Errorf("Regular terminal focus script should NOT use osascript, got: %s", script)
	}
}

func TestBuildFocusScript_Iterm2PrefersExactSessionHelper(t *testing.T) {
	setupFakeiTerm2Env(t)
	overrideIterm2Healthcheck(t, iTerm2HelperReady)
	t.Setenv(iTerm2SessionIDEnv, "w0t0p0:abc-123")

	script := buildFocusScript("com.googlecode.iterm2", "/home/user/my-project")

	if !strings.Contains(script, "iterm2-select-tab.py") {
		t.Fatalf("iTerm2 script should use the Python helper, got: %s", script)
	}
	if !strings.Contains(script, "--termid 'w0t0p0:abc-123'") {
		t.Errorf("iTerm2 helper should receive the exact termid, got: %s", script)
	}
	if !strings.Contains(script, "--cwd '/home/user/my-project'") {
		t.Errorf("iTerm2 helper should receive cwd fallback, got: %s", script)
	}
	if !strings.Contains(script, "||") {
		t.Errorf("iTerm2 reveal script should preserve app-focus fallback, got: %s", script)
	}
	if !strings.Contains(script, "open -a iTerm") {
		t.Errorf("iTerm2 reveal script should fall back to plain iTerm activation, got: %s", script)
	}
}

func TestBuildFocusScript_Iterm2WithoutCWDStillTargetsSession(t *testing.T) {
	setupFakeiTerm2Env(t)
	overrideIterm2Healthcheck(t, iTerm2HelperReady)
	t.Setenv(iTerm2SessionIDEnv, "w0t0p1:xyz")

	script := buildFocusScript("com.googlecode.iterm2", "")

	if !strings.Contains(script, "iterm2-select-tab.py") {
		t.Fatalf("iTerm2 script should use the Python helper, got: %s", script)
	}
	if !strings.Contains(script, "--termid 'w0t0p1:xyz'") {
		t.Errorf("iTerm2 helper should receive the exact termid, got: %s", script)
	}
	if strings.Contains(script, "focus-window") {
		t.Errorf("iTerm2 script without cwd should not add focus-window fallback, got: %s", script)
	}
}

func TestBuildFocusScript_Iterm2WithoutHelperFallsBackToActivate(t *testing.T) {
	withIsolatedEnv(t)
	t.Setenv(iTerm2SessionIDEnv, "w0t0p9:no-helper")

	script := buildFocusScript("com.googlecode.iterm2", "/home/user/my-project")

	if script != "" {
		t.Fatalf("iTerm2 should fall back to plain app activation when helper is unavailable, got: %s", script)
	}
	if strings.Contains(script, "iterm2-select-tab.py") {
		t.Errorf("iTerm2 should not reference helper when it is unavailable, got: %s", script)
	}
}

func TestBuildFocusScript_Iterm2DisabledAPIFallsBackToActivateAndPromptsOnce(t *testing.T) {
	withIsolatedEnv(t)
	setupFakeiTerm2Env(t)
	t.Setenv(iTerm2SessionIDEnv, "w0t0p9:disabled")

	restoreExecCommand := installFakeOpen(t, "", iTerm2HealthcheckExitDisabled)
	defer restoreExecCommand()

	originalSendQuickNotification := sendQuickNotification
	defer func() {
		sendQuickNotification = originalSendQuickNotification
	}()

	promptCount := 0
	sendQuickNotification = func(title, message, executeCmd string) error {
		promptCount++
		return nil
	}

	script := buildFocusScript("com.googlecode.iterm2", "/home/user/my-project")
	if script != "" {
		t.Fatalf("disabled iTerm2 API should fall back to plain app activation, got: %s", script)
	}
	if strings.Contains(script, "iterm2-select-tab.py") {
		t.Fatalf("disabled iTerm2 API should not keep helper in execute script, got: %s", script)
	}
	if promptCount != 1 {
		t.Fatalf("expected one prompt on first disabled-api detection, got %d", promptCount)
	}

	script = buildFocusScript("com.googlecode.iterm2", "/home/user/my-project")
	if script != "" {
		t.Fatalf("disabled iTerm2 API should still fall back to plain app activation, got: %s", script)
	}
	if promptCount != 1 {
		t.Fatalf("disabled-api prompt should be throttled, got %d prompts", promptCount)
	}
}

func TestBuildFocusScript_Iterm2HealthcheckConnectFailureFallsBackToActivateAndPromptsOnce(t *testing.T) {
	withIsolatedEnv(t)
	setupFakeiTerm2Env(t)
	t.Setenv(iTerm2SessionIDEnv, "w0t0p9:connect-failure")

	restoreExecCommand := installFakeOpen(t, "There was a problem connecting to iTerm2.\nIf you have downgraded from iTerm2 3.3.12+ to an older version, you must manually delete the file at /Users/test/Library/Application Support/iTerm2/private/socket.", iTerm2HealthcheckExitOther)
	defer restoreExecCommand()

	originalSendQuickNotification := sendQuickNotification
	defer func() {
		sendQuickNotification = originalSendQuickNotification
	}()

	promptCount := 0
	sendQuickNotification = func(title, message, executeCmd string) error {
		promptCount++
		if !strings.Contains(message, "Settings > General > Magic > Enable Python API") {
			t.Fatalf("prompt should mention exact iTerm2 settings path, got: %s", message)
		}
		return nil
	}

	script := buildFocusScript("com.googlecode.iterm2", "/home/user/my-project")
	if script != "" {
		t.Fatalf("connect failure should fall back to plain app activation, got: %s", script)
	}
	if strings.Contains(script, "iterm2-select-tab.py") {
		t.Fatalf("connect failure should not keep helper in execute script, got: %s", script)
	}
	if promptCount != 1 {
		t.Fatalf("expected one prompt on first connect failure, got %d", promptCount)
	}

	script = buildFocusScript("com.googlecode.iterm2", "/home/user/my-project")
	if script != "" {
		t.Fatalf("connect failure should still fall back to plain app activation, got: %s", script)
	}
	if promptCount != 1 {
		t.Fatalf("connect-failure prompt should be throttled, got %d prompts", promptCount)
	}
}

func TestBuildTmuxCCNotifierArgs_DisabledAPIErrors(t *testing.T) {
	setupFakeiTerm2Env(t)

	restoreExecCommand := installFakeOpen(t, "", iTerm2HealthcheckExitDisabled)
	defer restoreExecCommand()

	_, err := buildTmuxCCNotifierArgs("Title", "Msg", "%42", iTerm2BundleID)
	if err == nil {
		t.Fatal("expected error when iTerm2 Python API is disabled")
	}
}

func TestBuildTerminalNotifierArgs_WithCWD_UsesExecute(t *testing.T) {
	args := buildTerminalNotifierArgs("Title", "Message", "com.microsoft.VSCode", "/home/user/proj", true)
	if containsArg(args, "-activate", "com.microsoft.VSCode") {
		t.Error("When cwd is set, should use -execute not -activate")
	}
	execVal := getArgValue(args, "-execute")
	if execVal == "" {
		t.Error("When cwd is set, -execute should be present")
	}
}

func TestBuildTerminalNotifierArgs_WithCWD_NonItermTerminalUsesFocusWindow(t *testing.T) {
	t.Setenv(iTerm2SessionIDEnv, "")
	clearWarpFocusEnv(t)
	args := buildTerminalNotifierArgs("Title", "Message", "dev.warp.Warp-Stable", "/home/user/my-project", true)
	execVal := getArgValue(args, "-execute")
	if execVal == "" {
		t.Error("-execute should be present when cwd is set")
	}
	// Should use focus-window subcommand, not osascript (AppleScript is broken on macOS Tahoe)
	if !strings.Contains(execVal, "focus-window") {
		t.Errorf("-execute value should contain focus-window, got: %s", execVal)
	}
	if strings.Contains(execVal, "osascript") {
		t.Errorf("-execute value should NOT contain osascript, got: %s", execVal)
	}
	if !strings.Contains(execVal, "my-project") {
		t.Errorf("-execute value should contain cwd path, got: %s", execVal)
	}
}

func TestBuildTerminalNotifierArgs_Iterm2SessionIDUsesExecuteWithoutCWD(t *testing.T) {
	setupFakeiTerm2Env(t)
	overrideIterm2Healthcheck(t, iTerm2HelperReady)
	t.Setenv(iTerm2SessionIDEnv, "w0t0p7:test")

	args := buildTerminalNotifierArgs("Title", "Message", "com.googlecode.iterm2", "", true)

	if containsArg(args, "-activate", "com.googlecode.iterm2") {
		t.Error("iTerm2 exact session targeting should prefer -execute over -activate")
	}
	execVal := getArgValue(args, "-execute")
	if !strings.Contains(execVal, "iterm2-select-tab.py") {
		t.Errorf("-execute should contain iTerm2 helper, got: %s", execVal)
	}
	if !strings.Contains(execVal, "--termid 'w0t0p7:test'") {
		t.Errorf("-execute should pass termid to helper, got: %s", execVal)
	}
}

// === Tests for SendQuickNotification ===

func TestSendQuickNotification_DoesNotPanic(t *testing.T) {
	// SendQuickNotification should never panic regardless of environment.
	// In CI where neither terminal-notifier nor osascript may work,
	// an error is acceptable.
	err := SendQuickNotification("Test Title", "Test message", "")
	_ = err
}

func TestSendQuickNotification_WithExecuteCmd(t *testing.T) {
	// Should not panic when executeCmd is provided
	err := SendQuickNotification("Title", "Message", "echo hello")
	_ = err
}

func TestSendQuickNotification_EmptyFields(t *testing.T) {
	// Edge case: all empty strings
	err := SendQuickNotification("", "", "")
	_ = err
}

func TestBuildFocusScript_RegularTerminal_InvalidCWD_FallbackToActivate(t *testing.T) {
	t.Setenv(iTerm2SessionIDEnv, "")
	// When cwd is "." (invalid), buildFocusScript returns "" and
	// buildTerminalNotifierArgs falls back to -activate (app-level focus)
	args := buildTerminalNotifierArgs("Title", "Msg", "com.googlecode.iterm2", ".", true)
	if !containsArg(args, "-activate", "com.googlecode.iterm2") {
		t.Error("Should fallback to -activate when cwd is invalid")
	}
}
