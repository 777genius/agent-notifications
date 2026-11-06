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
		t.Setenv("TMUX", "/tmp/test-tmux.sock,2,0")
		t.Setenv("TMUX_PANE", "%17")
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

// When TMUX_PANE is unavailable, tmux's active-pane lookup can select an
// unrelated pane after a focus change. Ghostty must never use that target.
func TestGhosttyRejectsUnboundTmuxActivePaneFallback(t *testing.T) {
	t.Setenv("TMUX", "/tmp/test-tmux.sock,2,0")
	t.Setenv("TMUX_PANE", "")
	cfg := config.DefaultConfig()
	called := false
	detector := func(_, _, _ string, _ *config.Config) ([]string, string) {
		called = true
		return []string{"-title", "Title", "-message", "Body", "-execute", "wrong-active-pane"}, "tmux"
	}
	args := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.mitchellh.ghostty", "/repo", "", true, cfg, detector)
	if called {
		t.Fatal("unverified Ghostty tmux consulted the guessed active pane")
	}
	for _, arg := range args {
		if arg == "-activate" || arg == "-execute" {
			t.Fatalf("unverified origin installed a focus action: %v", args)
		}
	}
	withID := selectTerminalNotifierArgsWithDetector("Title", "Body", "com.mitchellh.ghostty", "/repo", "term-17", true, cfg, detector)
	if called {
		t.Fatal("exact Ghostty ID must not consult the guessed tmux pane")
	}
	found := false
	for _, arg := range withID {
		if strings.Contains(arg, "--ghostty-terminal-id") && strings.Contains(arg, "term-17") {
			found = true
		}
	}
	if !found {
		t.Fatalf("exact Ghostty terminal callback missing: %v", withID)
	}
}

func TestGhosttyExactCallbackConstructionNeverDegradesToActivate(t *testing.T) {
	cfg := config.DefaultConfig()
	noMux := func(_, _, _ string, _ *config.Config) ([]string, string) { return nil, "" }
	cases := []struct {
		name    string
		cwd     string
		id      string
		wantCmd bool
	}{
		{name: "missing origin", cwd: "/repo", id: "", wantCmd: false},
		{name: "missing cwd despite origin", cwd: "", id: "term-17", wantCmd: false},
		{name: "valid origin and cwd", cwd: "/repo", id: "term-17", wantCmd: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := selectTerminalNotifierArgsWithDetector(
				"Title", "Body", "com.mitchellh.ghostty", tc.cwd, tc.id, true, cfg, noMux,
			)
			hasExecute := false
			for _, v := range args {
				if v == "-activate" {
					t.Fatalf("Ghostty must never degrade to an approximate app focus: %v", args)
				}
				if v == "-execute" {
					hasExecute = true
				}
			}
			if hasExecute != tc.wantCmd {
				t.Fatalf("exact action=%t, want=%t: %v", hasExecute, tc.wantCmd, args)
			}
		})
	}
	// Simulate an unavailable focus executable or script resolver. The
	// final action assembler must treat an empty script as banner-only.
	for _, arg := range exactGhosttyNotifierArgs("Title", "Body", "") {
		if arg == "-activate" || arg == "-execute" {
			t.Fatal("failed script resolution still installed a click action")
		}
	}
	t.Setenv("TMUX", "/tmp/test-tmux.sock,2,0")
	t.Setenv("TMUX_PANE", "")
	args := selectTerminalNotifierArgsWithDetector(
		"Title", "Body", "com.mitchellh.ghostty", "", "term-17", true, cfg,
		func(_, _, _ string, _ *config.Config) ([]string, string) {
			t.Fatal("unverified tmux fallback must not be queried")
			return nil, ""
		},
	)
	for _, arg := range args {
		if arg == "-activate" || arg == "-execute" {
			t.Fatalf("missing cwd and tmux pane installed a click action: %v", args)
		}
	}
}
