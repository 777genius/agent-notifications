//go:build linux

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGetZellijFocusHints_InsideZellij(t *testing.T) {
	// Zellij sets ZELLIJ=0 inside a session; the value is a marker, not a flag.
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
	t.Setenv("ZELLIJ_PANE_ID", "2")

	session, paneID := GetZellijFocusHints()
	if session != "cubic-weasel" || paneID != "2" {
		t.Errorf("GetZellijFocusHints() = (%q, %q), want (%q, %q)", session, paneID, "cubic-weasel", "2")
	}
}

func TestGetZellijFocusHints_OutsideZellij(t *testing.T) {
	// Setting a variable empty is how these tests spell "unset": the code reads
	// every one of them through os.Getenv, which cannot tell the two apart.
	t.Setenv("ZELLIJ", "")
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	t.Setenv("ZELLIJ_PANE_ID", "")

	session, paneID := GetZellijFocusHints()
	if session != "" || paneID != "" {
		t.Errorf("GetZellijFocusHints() outside zellij = (%q, %q), want empty", session, paneID)
	}
}

// A background job inherits the pane variables without $ZELLIJ, and its pane is
// as real as any other. Observed on a Claude Code background session whose click
// raised the window but switched no pane.
func TestGetZellijFocusHints_WithoutZellijMarker(t *testing.T) {
	t.Setenv("ZELLIJ", "")
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
	t.Setenv("ZELLIJ_PANE_ID", "2")

	session, paneID := GetZellijFocusHints()
	if session != "cubic-weasel" || paneID != "2" {
		t.Errorf("GetZellijFocusHints() = (%q, %q), want (%q, %q)", session, paneID, "cubic-weasel", "2")
	}
}

func TestInZellij(t *testing.T) {
	cases := []struct {
		name    string
		marker  string
		session string
		paneID  string
		want    bool
	}{
		{"marker only", "0", "", "", true},
		{"pane variables only", "", "cubic-weasel", "2", true},
		{"everything set", "0", "cubic-weasel", "2", true},
		{"session without pane", "", "cubic-weasel", "", false},
		{"pane without session", "", "", "2", false},
		{"nothing set", "", "", "", false},
		{"blank pane variables", "", "  ", " ", false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("ZELLIJ", testCase.marker)
			t.Setenv("ZELLIJ_SESSION_NAME", testCase.session)
			t.Setenv("ZELLIJ_PANE_ID", testCase.paneID)

			if got := InZellij(); got != testCase.want {
				t.Errorf("InZellij() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestGetZellijFocusHints_TrimsWhitespace(t *testing.T) {
	t.Setenv("ZELLIJ", "0")
	t.Setenv("ZELLIJ_SESSION_NAME", "  cubic-weasel\n")
	t.Setenv("ZELLIJ_PANE_ID", " 2 ")

	session, paneID := GetZellijFocusHints()
	if session != "cubic-weasel" || paneID != "2" {
		t.Errorf("GetZellijFocusHints() = (%q, %q), want trimmed values", session, paneID)
	}
}

func TestTryZellijPane_MissingHints(t *testing.T) {
	cases := []struct {
		name    string
		session string
		paneID  string
	}{
		{"no session", "", "2"},
		{"no pane", "cubic-weasel", ""},
		{"neither", "", ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := TryZellijPane(testCase.session, testCase.paneID); err == nil {
				t.Errorf("TryZellijPane(%q, %q) = nil, want error", testCase.session, testCase.paneID)
			}
		})
	}
}

// zellij exits 2 both when the pane is already focused and when it does not
// exist, so the two have to be told apart by their message.
func TestIsZellijAlreadyFocused(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"already focused", "Pane Terminal(2) is already focused", true},
		{"already focused, different case", "PANE TERMINAL(2) IS ALREADY FOCUSED", true},
		{"pane not found shares the exit code", "Pane with id Terminal(9999) not found", false},
		{"session not found", "Session 'no-such-session' not found.", false},
		{"empty", "", false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isZellijAlreadyFocused(testCase.output); got != testCase.want {
				t.Errorf("isZellijAlreadyFocused(%q) = %v, want %v", testCase.output, got, testCase.want)
			}
		})
	}
}

func stubZellijAction(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zellij"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestRunZellijAction_Arguments(t *testing.T) {
	dir := stubZellijAction(t, `printf '%s\n' "$@" > "$ZELLIJ_TEST_ARGS"`)
	argsPath := filepath.Join(dir, "args")
	t.Setenv("ZELLIJ_TEST_ARGS", argsPath)
	if err := TryZellijPane("session with spaces", "42"); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args), "-s\nsession with spaces\naction\nfocus-pane-id\n42\n"; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
	if err := TryZellijTab("another session", "tab with spaces"); err != nil {
		t.Fatal(err)
	}
	args, err = os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args), "-s\nanother session\naction\ngo-to-tab-name\ntab with spaces\n"; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}

func TestRunZellijAction_NonzeroExit(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		wantError    bool
	}{
		{"already focused", "Pane Terminal(2) is already focused", false},
		{"missing pane", "Pane with id Terminal(9999) not found", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubZellijAction(t, `printf '%s' "$ZELLIJ_TEST_OUTPUT" >&2
exit 2
`)
			t.Setenv("ZELLIJ_TEST_OUTPUT", tc.output)
			err := TryZellijPane("test-session", "2")
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError %v", err, tc.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), tc.output) {
				t.Fatalf("error lost CLI output: %v", err)
			}
		})
	}
}

func TestRunZellijAction_TimeoutWithInheritedPipes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := stubZellijAction(t, `"$ZELLIJ_TEST_EXECUTABLE" -test.run=^TestZellijPipeHolder$ &
wait
`)
	release := filepath.Join(dir, "release")
	done := filepath.Join(dir, "done")
	t.Setenv("ZELLIJ_TEST_EXECUTABLE", executable)
	t.Setenv("ZELLIJ_TEST_PIPE_HOLDER", dir)
	// Release through a file, avoiding PID reuse races and orphaned sleep commands.
	t.Cleanup(func() {
		if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Error(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(done); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("pipe holder did not finish cleanup")
	})
	original := zellijActionTimeout
	zellijActionTimeout = 300 * time.Millisecond
	t.Cleanup(func() { zellijActionTimeout = original })
	start := time.Now()
	err = TryZellijPane("test-session", "2")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %v", elapsed)
	}
	if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil {
		t.Fatal("pipe holder never started:", err)
	}
}

// This subprocess retains both output pipes after the stub shell is killed.
// Its success-looking output must not conceal the deadline error.
func TestZellijPipeHolder(t *testing.T) {
	dir := os.Getenv("ZELLIJ_TEST_PIPE_HOLDER")
	if dir == "" {
		return
	}
	_, _ = os.Stdout.WriteString("already focused\n")
	_, _ = os.Stderr.WriteString("already focused\n")
	if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600); err != nil {
		os.Exit(2)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = os.Stdout.Close()
	_ = os.Stderr.Close()
	_ = os.WriteFile(filepath.Join(dir, "done"), nil, 0o600)
	os.Exit(0)
}
