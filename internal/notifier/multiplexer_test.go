package notifier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMultiplexerArgs_NoMux(t *testing.T) {
	// Save and clear all multiplexer env vars
	oldTmux := os.Getenv("TMUX")
	oldZellij := os.Getenv("ZELLIJ")
	oldWezTermPane := os.Getenv("WEZTERM_PANE")
	oldKittyWindowID := os.Getenv("KITTY_WINDOW_ID")
	oldKittyListenOn := os.Getenv("KITTY_LISTEN_ON")
	os.Unsetenv("TMUX")
	os.Unsetenv("ZELLIJ")
	os.Unsetenv("WEZTERM_PANE")
	os.Unsetenv("KITTY_WINDOW_ID")
	os.Unsetenv("KITTY_LISTEN_ON")
	t.Cleanup(func() {
		if oldTmux != "" {
			os.Setenv("TMUX", oldTmux)
		}
		if oldZellij != "" {
			os.Setenv("ZELLIJ", oldZellij)
		}
		if oldWezTermPane != "" {
			os.Setenv("WEZTERM_PANE", oldWezTermPane)
		}
		if oldKittyWindowID != "" {
			os.Setenv("KITTY_WINDOW_ID", oldKittyWindowID)
		}
		if oldKittyListenOn != "" {
			os.Setenv("KITTY_LISTEN_ON", oldKittyListenOn)
		}
	})

	args, name := detectMultiplexerArgs("Title", "Message", "com.test.app", nil)
	if args != nil {
		t.Errorf("expected nil args when no multiplexer, got %v", args)
	}
	if name != "" {
		t.Errorf("expected empty name when no multiplexer, got %q", name)
	}
}

func TestDetectMultiplexerArgs_TmuxPriority(t *testing.T) {
	// When both TMUX and ZELLIJ are set, tmux should win (first in registry)
	oldTmux := os.Getenv("TMUX")
	oldZellij := os.Getenv("ZELLIJ")
	os.Setenv("TMUX", filepath.Join(t.TempDir(), "missing.sock")+",12345,0")
	os.Setenv("ZELLIJ", "0")
	t.Cleanup(func() {
		if oldTmux != "" {
			os.Setenv("TMUX", oldTmux)
		} else {
			os.Unsetenv("TMUX")
		}
		if oldZellij != "" {
			os.Setenv("ZELLIJ", oldZellij)
		} else {
			os.Unsetenv("ZELLIJ")
		}
	})

	// tmux will be detected but GetTmuxPaneTarget will fail (no real tmux server)
	// so we expect (nil, "tmux") — detected but target capture failed
	args, name := detectMultiplexerArgs("Title", "Message", "com.test.app", nil)
	if args != nil {
		t.Errorf("expected nil args (no real tmux server), got %v", args)
	}
	if name != "tmux" {
		t.Errorf("expected name = %q (tmux wins by priority), got %q", "tmux", name)
	}
}
