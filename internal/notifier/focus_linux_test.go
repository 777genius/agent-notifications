//go:build linux

package notifier

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWindowID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want uint64
		ok   bool
	}{
		{"decimal", "31457289", 31457289, true},
		{"hex", "0x1e00009", 0x1e00009, true},
		{"whitespace trimmed", "  12345 \n", 12345, true},
		{"empty", "", 0, false},
		{"not a number", "window", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseWindowID(tt.raw)
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseWindowID(%q) = (%d, %v), want (%d, %v)", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTerminalHasFocus_Linux(t *testing.T) {
	restore, restoreIDE := activeWindowID, detectJetBrainsIDE
	defer func() { activeWindowID, detectJetBrainsIDE = restore, restoreIDE }()
	// Not a JetBrains terminal, even when the tests run in one.
	detectJetBrainsIDE = func() (string, int, bool) { return "", 0, false }

	t.Run("focused when active window matches WINDOWID", func(t *testing.T) {
		t.Setenv("WINDOWID", "0x1e00009")
		activeWindowID = func() (string, error) { return "31457289", nil } // == 0x1e00009
		if !terminalHasFocus("", "/repo") {
			t.Error("expected focus when the active window equals WINDOWID")
		}
	})

	t.Run("not focused when active window differs", func(t *testing.T) {
		t.Setenv("WINDOWID", "100")
		activeWindowID = func() (string, error) { return "200", nil }
		if terminalHasFocus("", "/repo") {
			t.Error("expected no focus when the active window differs")
		}
	})

	t.Run("unknown (notify) when WINDOWID is unset (e.g. Wayland)", func(t *testing.T) {
		t.Setenv("WINDOWID", "")
		activeWindowID = func() (string, error) { return "200", nil }
		if terminalHasFocus("", "/repo") {
			t.Error("expected no focus (deliver) when WINDOWID is unset")
		}
	})

	t.Run("unknown (notify) when the active-window query fails", func(t *testing.T) {
		t.Setenv("WINDOWID", "100")
		activeWindowID = func() (string, error) { return "", errors.New("xdotool missing") }
		if terminalHasFocus("", "/repo") {
			t.Error("expected no focus (deliver) when the query fails")
		}
	})
}

// ideProject creates a JetBrains project (a folder holding .idea) and returns
// its root.
func ideProject(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, ".idea"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTerminalHasFocus_JetBrains(t *testing.T) {
	restoreIDE, restoreWindow, restoreID := detectJetBrainsIDE, activeWindow, activeWindowID
	defer func() { detectJetBrainsIDE, activeWindow, activeWindowID = restoreIDE, restoreWindow, restoreID }()
	t.Setenv("WINDOWID", "")

	root := ideProject(t, "api")
	ide := func() (string, int, bool) { return "jetbrains-goland", 4242, true }

	tests := []struct {
		name   string
		ide    func() (string, int, bool)
		cwd    string
		window windowInfo
		err    error
		want   bool
	}{
		{"this project's window", ide, filepath.Join(root, "internal"), windowInfo{4242, "api – main.go"}, nil, true},
		{"this project, path shown", ide, root, windowInfo{4242, "api [" + root + "] – main.go"}, nil, true},
		{"same name, other path", ide, root, windowInfo{4242, "api [/srv/other/api] – main.go"}, nil, false},
		{"other project of the same IDE", ide, root, windowInfo{4242, "web – index.ts"}, nil, false},
		{"other IDE process", ide, root, windowInfo{7, "api – main.go"}, nil, false},
		{"active-window query fails", ide, root, windowInfo{}, errors.New("kdotool missing"), false},
		{"not a JetBrains terminal", func() (string, int, bool) { return "", 0, false }, root, windowInfo{4242, "api"}, nil, false},
		{"cwd outside a JetBrains project", ide, t.TempDir(), windowInfo{4242, "api"}, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.MkdirAll(tt.cwd, 0755); err != nil {
				t.Fatal(err)
			}
			detectJetBrainsIDE = tt.ide
			activeWindow = func() (windowInfo, error) { return tt.window, tt.err }

			if got := terminalHasFocus("", tt.cwd); got != tt.want {
				t.Errorf("terminalHasFocus() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A $WINDOWID in a JetBrains terminal was inherited from whatever launched the
// IDE. It must not decide focus: that terminal being active is not this session.
func TestTerminalHasFocus_JetBrainsIgnoresInheritedWindowID(t *testing.T) {
	restoreIDE, restoreWindow, restoreID := detectJetBrainsIDE, activeWindow, activeWindowID
	defer func() { detectJetBrainsIDE, activeWindow, activeWindowID = restoreIDE, restoreWindow, restoreID }()

	root := ideProject(t, "api")
	t.Setenv("WINDOWID", "100")
	activeWindowID = func() (string, error) { return "100", nil } // the launcher terminal is active
	detectJetBrainsIDE = func() (string, int, bool) { return "jetbrains-goland", 4242, true }

	activeWindow = func() (windowInfo, error) { return windowInfo{7, "xterm"}, nil }
	if terminalHasFocus("", root) {
		t.Error("launcher terminal active: expected no focus (deliver)")
	}

	activeWindow = func() (windowInfo, error) { return windowInfo{4242, "api – main.go"}, nil }
	if !terminalHasFocus("", root) {
		t.Error("IDE project window active: expected focus")
	}
}

// fakeWindowTool puts a script on PATH that prints output for the chained
// getactivewindow/getwindowpid/getwindowname call and records its arguments.
func fakeWindowTool(t *testing.T, tool, output string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$*\" > '" + args + "'\nprintf '%s' '" + output + "'\n"
	if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return args
}

func TestDefaultActiveWindow(t *testing.T) {
	for _, tt := range []struct {
		session string
		tool    string
	}{
		{"wayland", "kdotool"},
		{"x11", "xdotool"},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			t.Setenv("XDG_SESSION_TYPE", tt.session)
			args := fakeWindowTool(t, tt.tool, "4242\napi – main.go\n")

			got, err := defaultActiveWindow()
			if err != nil {
				t.Fatalf("defaultActiveWindow() error = %v", err)
			}
			want := windowInfo{4242, "api – main.go"}
			if got != want {
				t.Errorf("defaultActiveWindow() = %+v, want %+v", got, want)
			}
			called, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(called)) != "getactivewindow getwindowpid getwindowname" {
				t.Errorf("%s called with %q", tt.tool, called)
			}
		})
	}
}

func TestParseWindowInfo(t *testing.T) {
	tests := []struct {
		out  string
		want windowInfo
		ok   bool
	}{
		{"4242\napi – main.go\n", windowInfo{4242, "api – main.go"}, true},
		{"4242\napi\nsecond line\n", windowInfo{4242, "api\nsecond line"}, true},
		{"4242\n", windowInfo{4242, ""}, true},
		{"not-a-pid\napi\n", windowInfo{}, false},
		{"", windowInfo{}, false},
	}
	for _, tt := range tests {
		got, err := parseWindowInfo(tt.out)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseWindowInfo(%q) = (%+v, %v), want (%+v, ok=%v)", tt.out, got, err, tt.want, tt.ok)
		}
	}
}
