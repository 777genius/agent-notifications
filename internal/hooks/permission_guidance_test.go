package hooks

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/notifier"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer reader.Close()

	os.Stdout = writer
	defer func() {
		os.Stdout = originalStdout
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close stdout writer: %v", err)
	}

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read stdout capture: %v", err)
	}
	return string(output)
}

func TestMaybeEmitDesktopPermissionGuidance_RateLimited(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific permission guidance")
	}

	tempHome := t.TempDir()
	setTestHome(t, tempHome)

	handler := &Handler{}
	err := &notifier.NotificationPermissionDeniedError{Details: "Error: Notification permission denied. Enable in System Settings > Notifications."}

	// A failed stdout write must not consume the reminder's 24-hour cooldown.
	cacheDir, cacheErr := os.UserCacheDir()
	if cacheErr != nil {
		t.Fatal(cacheErr)
	}
	stampPath := filepath.Join(cacheDir, "claude-notifications-go", "macos-notification-permission-reminder")
	closedOutput, openErr := os.CreateTemp(t.TempDir(), "closed-stdout")
	if openErr != nil {
		t.Fatal(openErr)
	}
	if closeErr := closedOutput.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	func() {
		originalStdout := os.Stdout
		os.Stdout = closedOutput
		defer func() { os.Stdout = originalStdout }()
		handler.maybeEmitDesktopPermissionGuidance(err)
	}()
	if _, statErr := os.Stat(stampPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed guidance output consumed reminder: %v", statErr)
	}

	first := captureStdout(t, func() {
		handler.maybeEmitDesktopPermissionGuidance(err)
	})
	if !strings.Contains(first, "systemMessage") {
		t.Fatalf("expected systemMessage on first permission denial, got %q", first)
	}
	if !strings.Contains(first, "Agent Notifications") {
		t.Fatalf("expected Agent Notifications guidance, got %q", first)
	}

	second := captureStdout(t, func() {
		handler.maybeEmitDesktopPermissionGuidance(err)
	})
	if second != "" {
		t.Fatalf("expected guidance to be rate-limited, got %q", second)
	}
}
