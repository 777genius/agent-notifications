package notifier

import (
	"os"
	"strings"
	"testing"
)

func TestIsWezTerm(t *testing.T) {
	oldVal := os.Getenv("WEZTERM_PANE")
	t.Cleanup(func() {
		if oldVal != "" {
			os.Setenv("WEZTERM_PANE", oldVal)
		} else {
			os.Unsetenv("WEZTERM_PANE")
		}
	})

	os.Setenv("WEZTERM_PANE", "42")
	if !IsWezTerm() {
		t.Error("IsWezTerm() should return true when WEZTERM_PANE is set")
	}

	os.Unsetenv("WEZTERM_PANE")
	if IsWezTerm() {
		t.Error("IsWezTerm() should return false when WEZTERM_PANE is not set")
	}
}

func TestGetWezTermPaneTarget(t *testing.T) {
	oldPane := os.Getenv("WEZTERM_PANE")
	oldSocket := os.Getenv("WEZTERM_UNIX_SOCKET")
	t.Cleanup(func() {
		if oldPane != "" {
			os.Setenv("WEZTERM_PANE", oldPane)
		} else {
			os.Unsetenv("WEZTERM_PANE")
		}
		if oldSocket != "" {
			os.Setenv("WEZTERM_UNIX_SOCKET", oldSocket)
		} else {
			os.Unsetenv("WEZTERM_UNIX_SOCKET")
		}
	})

	os.Setenv("WEZTERM_PANE", "7")
	os.Setenv("WEZTERM_UNIX_SOCKET", "/tmp/wezterm-sock")

	paneID, socketPath, err := GetWezTermPaneTarget()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "7" {
		t.Errorf("paneID = %q, want %q", paneID, "7")
	}
	if socketPath != "/tmp/wezterm-sock" {
		t.Errorf("socketPath = %q, want %q", socketPath, "/tmp/wezterm-sock")
	}
}

func TestGetWezTermPaneTarget_NoPaneEnv(t *testing.T) {
	oldPane := os.Getenv("WEZTERM_PANE")
	os.Unsetenv("WEZTERM_PANE")
	t.Cleanup(func() {
		if oldPane != "" {
			os.Setenv("WEZTERM_PANE", oldPane)
		}
	})

	_, _, err := GetWezTermPaneTarget()
	if err == nil {
		t.Error("expected error when WEZTERM_PANE is not set")
	}
}

func TestGetWezTermPaneTarget_NoSocket(t *testing.T) {
	oldPane := os.Getenv("WEZTERM_PANE")
	oldSocket := os.Getenv("WEZTERM_UNIX_SOCKET")
	t.Cleanup(func() {
		if oldPane != "" {
			os.Setenv("WEZTERM_PANE", oldPane)
		} else {
			os.Unsetenv("WEZTERM_PANE")
		}
		if oldSocket != "" {
			os.Setenv("WEZTERM_UNIX_SOCKET", oldSocket)
		} else {
			os.Unsetenv("WEZTERM_UNIX_SOCKET")
		}
	})

	os.Setenv("WEZTERM_PANE", "3")
	os.Unsetenv("WEZTERM_UNIX_SOCKET")

	paneID, socketPath, err := GetWezTermPaneTarget()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paneID != "3" {
		t.Errorf("paneID = %q, want %q", paneID, "3")
	}
	if socketPath != "" {
		t.Errorf("socketPath should be empty, got %q", socketPath)
	}
}

func TestBuildWezTermNotifierArgs(t *testing.T) {
	args := buildWezTermNotifierArgs("Title", "Message", "42", "/tmp/wezterm-sock", "com.test.app")

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
}

func TestBuildWezTermNotifierArgs_ExecuteContainsPaneAndSocket(t *testing.T) {
	args := buildWezTermNotifierArgs("Title", "Message", "42", "/tmp/wezterm-sock", "com.test.app")

	executeCmd := getArgValue(args, "-execute")
	if executeCmd == "" {
		t.Fatal("Missing -execute argument")
	}

	if !strings.Contains(executeCmd, "activate-pane") {
		t.Errorf("-execute should contain 'activate-pane', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "--pane-id 42") {
		t.Errorf("-execute should contain '--pane-id 42', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "--unix-socket") {
		t.Errorf("-execute should contain '--unix-socket', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "/tmp/wezterm-sock") {
		t.Errorf("-execute should contain socket path, got: %s", executeCmd)
	}
}

func TestBuildWezTermNotifierArgs_NoSocket(t *testing.T) {
	args := buildWezTermNotifierArgs("Title", "Message", "5", "", "com.test.app")

	executeCmd := getArgValue(args, "-execute")
	if executeCmd == "" {
		t.Fatal("Missing -execute argument")
	}

	if !strings.Contains(executeCmd, "--pane-id 5") {
		t.Errorf("-execute should contain '--pane-id 5', got: %s", executeCmd)
	}
	if strings.Contains(executeCmd, "--unix-socket") {
		t.Errorf("-execute should NOT contain '--unix-socket' when socket is empty, got: %s", executeCmd)
	}
}
