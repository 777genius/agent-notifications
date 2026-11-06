package notifier

import (
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

// A disabled click must not even inspect mux state: target detection can
// execute native commands or capture the wrong pane after a focus change.
func TestSelectTerminalNotifierArgsDisabledSkipsMultiplexerDetection(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, mux := range []string{"tmux", "zellij", "wezterm", "kitty"} {
		t.Run(mux, func(t *testing.T) {
			detected := false
			detector := func(_, _, _ string, _ *config.Config) ([]string, string) {
				detected = true
				return []string{"-title", "Title", "-message", "Body", "-execute", "callback"}, mux
			}
			args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.apple.Terminal", "/tmp/project", "", false, cfg, detector)
			if detected {
				t.Fatal("disabled click-to-focus called the multiplexer detector")
			}
			for _, a := range args {
				if a == "-activate" || a == "-execute" {
					t.Fatalf("disabled click-to-focus still installed action: %v", args)
				}
			}

			args = selectTerminalNotifierArgsWithDetector("Title", "Body", "com.apple.Terminal", "/tmp/project", "", true, cfg, detector)
			if !detected {
				t.Fatal("enabled click-to-focus did not try multiplexer detection")
			}
			if len(args) != 6 || args[4] != "-execute" || args[5] != "callback" {
				t.Fatalf("enabled multiplexer target not used: %v", args)
			}
		})
	}
}

func TestSelectTerminalNotifierArgsEnabledFallsBackAfterUnqualifiedMux(t *testing.T) {
	cfg := config.DefaultConfig()
	detected := false
	detector := func(_, _, _ string, _ *config.Config) ([]string, string) {
		detected = true
		return nil, "tmux"
	}
	args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.apple.Terminal", "", "", true, cfg, detector)
	if !detected {
		t.Fatal("enabled click-to-focus never tried the selected mux")
	}
	found := false
	for i, arg := range args {
		if arg == "-activate" && i+1 < len(args) && args[i+1] == "com.apple.Terminal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mux target failure must preserve terminal-app fallback: %v", args)
	}
}

func TestSelectTerminalNotifierArgsGhosttyRequiresOrigin(t *testing.T) {
	cfg := config.DefaultConfig()
	noMux := func(_, _, _ string, _ *config.Config) ([]string, string) { return nil, "" }
	assertAction := func(t *testing.T, args []string, want bool) {
		t.Helper()
		hasAction := false
		for _, arg := range args {
			if arg == "-execute" || arg == "-activate" {
				hasAction = true
			}
		}
		if hasAction != want {
			t.Fatalf("action present=%t, want %t: %v", hasAction, want, args)
		}
	}
	t.Run("missing bound id is banner only", func(t *testing.T) {
		args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.mitchellh.ghostty", "/repo", "", true, cfg, noMux)
		assertAction(t, args, false)
	})
	t.Run("bound id retains exact action", func(t *testing.T) {
		args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.mitchellh.ghostty", "/repo", "term-42", true, cfg, noMux)
		assertAction(t, args, true)
		found := false
		for _, arg := range args {
			if strings.Contains(arg, "--ghostty-terminal-id") && strings.Contains(arg, "term-42") {
				found = true
			}
		}
		if !found {
			t.Fatalf("verified terminal id was lost: %v", args)
		}
	})
	t.Run("verified multiplexer remains eligible", func(t *testing.T) {
		detector := func(_, _, _ string, _ *config.Config) ([]string, string) {
			return []string{"-title", "Title", "-message", "Body", "-execute", "verified-pane"}, "tmux"
		}
		args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.mitchellh.ghostty", "/repo", "", true, cfg, detector)
		assertAction(t, args, true)
		if args[5] != "verified-pane" {
			t.Fatalf("verified multiplexer action lost: %v", args)
		}
	})
	t.Run("disabled focus skips even verified mux", func(t *testing.T) {
		called := false
		detector := func(_, _, _ string, _ *config.Config) ([]string, string) {
			called = true
			return []string{"-execute", "wrong"}, "tmux"
		}
		args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.mitchellh.ghostty", "/repo", "term-42", false, cfg, detector)
		assertAction(t, args, false)
		if called {
			t.Fatal("disabled focus invoked multiplexer detector")
		}
	})
}
