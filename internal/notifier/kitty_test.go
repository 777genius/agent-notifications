package notifier

import (
	"os"
	"strings"
	"testing"
)

func TestIsKitty(t *testing.T) {
	oldWindowID := os.Getenv("KITTY_WINDOW_ID")
	oldListenOn := os.Getenv("KITTY_LISTEN_ON")
	t.Cleanup(func() {
		if oldWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldWindowID)
		} else {
			os.Unsetenv("KITTY_WINDOW_ID")
		}
		if oldListenOn != "" {
			os.Setenv("KITTY_LISTEN_ON", oldListenOn)
		} else {
			os.Unsetenv("KITTY_LISTEN_ON")
		}
	})

	os.Setenv("KITTY_WINDOW_ID", "1")
	os.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-sock")
	if !IsKitty() {
		t.Error("IsKitty() should return true when both KITTY_WINDOW_ID and KITTY_LISTEN_ON are set")
	}
}

func TestIsKitty_NoListenOn(t *testing.T) {
	oldWindowID := os.Getenv("KITTY_WINDOW_ID")
	oldListenOn := os.Getenv("KITTY_LISTEN_ON")
	t.Cleanup(func() {
		if oldWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldWindowID)
		} else {
			os.Unsetenv("KITTY_WINDOW_ID")
		}
		if oldListenOn != "" {
			os.Setenv("KITTY_LISTEN_ON", oldListenOn)
		} else {
			os.Unsetenv("KITTY_LISTEN_ON")
		}
	})

	os.Setenv("KITTY_WINDOW_ID", "1")
	os.Unsetenv("KITTY_LISTEN_ON")
	if IsKitty() {
		t.Error("IsKitty() should return false when KITTY_LISTEN_ON is not set (remote control disabled)")
	}
}

func TestIsKitty_NoWindowID(t *testing.T) {
	oldWindowID := os.Getenv("KITTY_WINDOW_ID")
	oldListenOn := os.Getenv("KITTY_LISTEN_ON")
	t.Cleanup(func() {
		if oldWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldWindowID)
		} else {
			os.Unsetenv("KITTY_WINDOW_ID")
		}
		if oldListenOn != "" {
			os.Setenv("KITTY_LISTEN_ON", oldListenOn)
		} else {
			os.Unsetenv("KITTY_LISTEN_ON")
		}
	})

	os.Unsetenv("KITTY_WINDOW_ID")
	os.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-sock")
	if IsKitty() {
		t.Error("IsKitty() should return false when KITTY_WINDOW_ID is not set")
	}
}

func TestGetKittyWindowTarget(t *testing.T) {
	oldWindowID := os.Getenv("KITTY_WINDOW_ID")
	oldListenOn := os.Getenv("KITTY_LISTEN_ON")
	t.Cleanup(func() {
		if oldWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldWindowID)
		} else {
			os.Unsetenv("KITTY_WINDOW_ID")
		}
		if oldListenOn != "" {
			os.Setenv("KITTY_LISTEN_ON", oldListenOn)
		} else {
			os.Unsetenv("KITTY_LISTEN_ON")
		}
	})

	os.Setenv("KITTY_WINDOW_ID", "5")
	os.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-sock")

	windowID, listenOn, err := GetKittyWindowTarget()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if windowID != "5" {
		t.Errorf("windowID = %q, want %q", windowID, "5")
	}
	if listenOn != "unix:/tmp/kitty-sock" {
		t.Errorf("listenOn = %q, want %q", listenOn, "unix:/tmp/kitty-sock")
	}
}

func TestGetKittyWindowTarget_NoWindowID(t *testing.T) {
	oldWindowID := os.Getenv("KITTY_WINDOW_ID")
	os.Unsetenv("KITTY_WINDOW_ID")
	t.Cleanup(func() {
		if oldWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldWindowID)
		}
	})

	_, _, err := GetKittyWindowTarget()
	if err == nil {
		t.Error("expected error when KITTY_WINDOW_ID is not set")
	}
}

func TestGetKittyWindowTarget_NoListenOn(t *testing.T) {
	oldWindowID := os.Getenv("KITTY_WINDOW_ID")
	oldListenOn := os.Getenv("KITTY_LISTEN_ON")
	t.Cleanup(func() {
		if oldWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldWindowID)
		} else {
			os.Unsetenv("KITTY_WINDOW_ID")
		}
		if oldListenOn != "" {
			os.Setenv("KITTY_LISTEN_ON", oldListenOn)
		} else {
			os.Unsetenv("KITTY_LISTEN_ON")
		}
	})

	os.Setenv("KITTY_WINDOW_ID", "5")
	os.Unsetenv("KITTY_LISTEN_ON")

	_, _, err := GetKittyWindowTarget()
	if err == nil {
		t.Error("expected error when KITTY_LISTEN_ON is not set")
	}
}

func TestBuildKittyNotifierArgs(t *testing.T) {
	args := buildKittyNotifierArgs("Title", "Message", "5", "unix:/tmp/kitty-sock", "com.test.app")

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

func TestBuildKittyNotifierArgs_ExecuteContainsWindowAndSocket(t *testing.T) {
	args := buildKittyNotifierArgs("Title", "Message", "5", "unix:/tmp/kitty-sock", "com.test.app")

	executeCmd := getArgValue(args, "-execute")
	if executeCmd == "" {
		t.Fatal("Missing -execute argument")
	}

	if !strings.Contains(executeCmd, "focus-window") {
		t.Errorf("-execute should contain 'focus-window', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "--match id:5") {
		t.Errorf("-execute should contain '--match id:5', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "--to") {
		t.Errorf("-execute should contain '--to', got: %s", executeCmd)
	}
	if !strings.Contains(executeCmd, "unix:/tmp/kitty-sock") {
		t.Errorf("-execute should contain socket path, got: %s", executeCmd)
	}
}
