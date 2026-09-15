package hooks

import (
	"io"
	"os"
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
