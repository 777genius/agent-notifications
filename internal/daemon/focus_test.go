//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// useFakeWindowTool puts a fake xdotool or kdotool on PATH: search lists ids in
// order, getwindowname prints titles[id], and windowactivate records its last
// argument (the window ID). It returns the trace path for readActivatedWindows.
func useFakeWindowTool(t *testing.T, tool string, ids []string, titles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	var script strings.Builder
	script.WriteString("#!/bin/sh\ncase \"$1\" in\nsearch)\n")
	for _, id := range ids {
		fmt.Fprintf(&script, "  echo '%s'\n", id)
	}
	script.WriteString("  ;;\ngetwindowname)\n  case \"$2\" in\n")
	for id, title := range titles {
		fmt.Fprintf(&script, "  '%s') echo '%s' ;;\n", id, title)
	}
	script.WriteString("  esac\n  ;;\nwindowactivate)\n  for arg; do last=$arg; done\n  echo \"$last\" >> \"$WINDOW_TOOL_TRACE\"\n  ;;\nesac\n")
	if err := os.WriteFile(filepath.Join(dir, tool), []byte(script.String()), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("WINDOW_TOOL_TRACE", trace)
	return trace
}

func readActivatedWindows(t *testing.T, trace string) []string {
	t.Helper()
	data, err := os.ReadFile(trace)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// xdotool lists bottom-most windows first; TryXdotool must activate the
// top-most window whose title names the folder.
func TestTryXdotool_PrefersTopMostTitleMatch(t *testing.T) {
	titles := map[string]string{
		"1": "project-a - Visual Studio Code",
		"2": "project-b - Visual Studio Code",
		"3": "project-a - Visual Studio Code",
	}

	tests := []struct {
		name   string
		folder string
		ids    []string
		want   string
	}{
		{"match below a non-match", "project-a", []string{"1", "2"}, "1"},
		{"top-most of several matches", "project-a", []string{"1", "2", "3"}, "3"},
		{"no match keeps the top-most window", "other", []string{"1", "2"}, "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := useFakeWindowTool(t, "xdotool", tt.ids, titles)

			if err := TryXdotool("code", tt.folder); err != nil {
				t.Fatalf("TryXdotool() error = %v", err)
			}
			if got := readActivatedWindows(t, trace); !reflect.DeepEqual(got, []string{tt.want}) {
				t.Errorf("activated %v, want [%s]", got, tt.want)
			}
		})
	}
}

// --- GetFocusMethods tests ---

func TestGetFocusMethods_Order(t *testing.T) {
	methods := GetFocusMethods()

	expectedNames := []string{
		"activate-window-by-title extension",
		"GNOME Shell Eval (by window title)",
		"GNOME Shell Eval (by app)",
		"GNOME Shell FocusApp",
		"wlrctl",
		"kdotool",
		"xdotool",
	}

	if len(methods) != len(expectedNames) {
		t.Fatalf("GetFocusMethods() returned %d methods, want %d", len(methods), len(expectedNames))
	}

	for i, method := range methods {
		if method.Name != expectedNames[i] {
			t.Errorf("GetFocusMethods()[%d].Name = %q, want %q", i, method.Name, expectedNames[i])
		}
		if method.Fn == nil {
			t.Errorf("GetFocusMethods()[%d].Fn is nil", i)
		}
	}
}

func TestGetFocusMethods_NotEmpty(t *testing.T) {
	methods := GetFocusMethods()
	if len(methods) == 0 {
		t.Fatal("GetFocusMethods() returned empty slice")
	}
}

func TestGetFocusMethods_AllHaveFunctions(t *testing.T) {
	for _, m := range GetFocusMethods() {
		if m.Name == "" {
			t.Error("FocusMethod has empty Name")
		}
		if m.Fn == nil {
			t.Errorf("FocusMethod %q has nil Fn", m.Name)
		}
	}
}

func TestNormalizeX11WindowID_Decimal(t *testing.T) {
	got, err := normalizeX11WindowID("12345")
	if err != nil {
		t.Fatalf("normalizeX11WindowID returned error: %v", err)
	}
	if got != "12345" {
		t.Errorf("normalizeX11WindowID(decimal) = %q, want %q", got, "12345")
	}
}

func TestNormalizeX11WindowID_Hex(t *testing.T) {
	got, err := normalizeX11WindowID("0x3039")
	if err != nil {
		t.Fatalf("normalizeX11WindowID returned error: %v", err)
	}
	if got != "12345" {
		t.Errorf("normalizeX11WindowID(hex) = %q, want %q", got, "12345")
	}
}

func TestNormalizeX11WindowID_Invalid(t *testing.T) {
	if _, err := normalizeX11WindowID("not-a-window"); err == nil {
		t.Fatal("normalizeX11WindowID should reject invalid input")
	}
}

func TestBuildXdotoolSearches_Order(t *testing.T) {
	searches := buildXdotoolSearches("terminator", "project")

	got := make([][]string, 0, len(searches))
	for _, search := range searches {
		got = append(got, search.args)
	}

	want := [][]string{
		{"search", "--onlyvisible", "--class", "Terminator"},
		{"search", "--class", "Terminator"},
		{"search", "--onlyvisible", "--name", "terminator"},
		{"search", "--name", "terminator"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildXdotoolSearches() = %#v, want %#v", got, want)
	}
}

func TestSplitWindowIDs_TrimsBlankLines(t *testing.T) {
	got := splitWindowIDs("\n123\n 456 \n\n789\n")
	want := []string{"123", "456", "789"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitWindowIDs() = %#v, want %#v", got, want)
	}
}

func TestTryFocusWithHints_WarpURLShortCircuits(t *testing.T) {
	const hex = "6b7be92641ae8ced80188a4d87e4b200"
	opened := ""
	orig := openFocusURL
	t.Cleanup(func() { openFocusURL = orig })
	openFocusURL = func(url string) error {
		opened = url
		return nil
	}

	if err := TryFocusWithHints(FocusHints{TerminalName: "WarpTerminal", FolderName: "proj", WarpFocusURL: "warp://session/" + hex}); err != nil {
		t.Fatalf("TryFocusWithHints: %v", err)
	}
	if opened != "warp://session/"+hex {
		t.Fatalf("opened %q, want warp session URL", opened)
	}
}

func TestTryFocusWithHints_RejectsNonSessionWarpURL(t *testing.T) {
	orig := openFocusURL
	t.Cleanup(func() { openFocusURL = orig })
	openFocusURL = func(url string) error {
		t.Fatalf("should not open non-session Warp URL %q", url)
		return nil
	}

	// Invalid URL must fall through to the compositor chain instead of xdg-open.
	_ = TryFocusWithHints(FocusHints{TerminalName: "WarpTerminal", FolderName: "proj", WarpFocusURL: "warp://action/new_tab"})
}
