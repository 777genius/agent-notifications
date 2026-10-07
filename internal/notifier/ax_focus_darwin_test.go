//go:build darwin

package notifier

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRetryWindowFocusWithDelays_SucceedsAfterRetries(t *testing.T) {
	t.Helper()

	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	var slept []time.Duration
	calls := 0

	result := retryWindowFocusWithDelays(func() int {
		calls++
		if calls < 3 {
			return 0
		}
		return 1
	}, delays, func(d time.Duration) {
		slept = append(slept, d)
	})

	if result != 1 {
		t.Fatalf("retryWindowFocusWithDelays() = %d, want 1", result)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
	if len(slept) != 3 {
		t.Fatalf("expected 3 sleeps, got %d", len(slept))
	}
	for i, want := range delays {
		if slept[i] != want {
			t.Fatalf("sleep %d = %v, want %v", i, slept[i], want)
		}
	}
}

func TestRetryWindowFocusWithDelays_ExhaustsRetries(t *testing.T) {
	t.Helper()

	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	sleeps := 0
	calls := 0

	result := retryWindowFocusWithDelays(func() int {
		calls++
		return 0
	}, delays, func(time.Duration) {
		sleeps++
	})

	if result != 0 {
		t.Fatalf("retryWindowFocusWithDelays() = %d, want 0", result)
	}
	if calls != len(delays) {
		t.Fatalf("expected %d attempts, got %d", len(delays), calls)
	}
	if sleeps != len(delays) {
		t.Fatalf("expected %d sleeps, got %d", len(delays), sleeps)
	}
}

func TestRetryWindowFocusWithDelays_ShortCircuitsOnPermissionError(t *testing.T) {
	t.Helper()

	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	sleeps := 0
	calls := 0

	result := retryWindowFocusWithDelays(func() int {
		calls++
		return -1
	}, delays, func(time.Duration) {
		sleeps++
	})

	if result != -1 {
		t.Fatalf("retryWindowFocusWithDelays() = %d, want -1", result)
	}
	if calls != 1 {
		t.Fatalf("expected 1 attempt, got %d", calls)
	}
	if sleeps != 1 {
		t.Fatalf("expected 1 sleep, got %d", sleeps)
	}
}

func TestRetryWindowFocusWithDelays_RestoredOnLastAttemptGetsExtraRetry(t *testing.T) {
	t.Helper()

	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	var slept []time.Duration
	calls := 0

	result := retryWindowFocusWithDelays(func() int {
		calls++
		switch calls {
		case 1, 2:
			return 0
		case 3:
			return windowFocusRetryAfterRestore
		case 4:
			return 1
		default:
			t.Fatalf("unexpected extra attempt %d", calls)
			return 0
		}
	}, delays, func(d time.Duration) {
		slept = append(slept, d)
	})

	if result != 1 {
		t.Fatalf("retryWindowFocusWithDelays() = %d, want 1", result)
	}
	if calls != 4 {
		t.Fatalf("expected 4 attempts including post-restore retry, got %d", calls)
	}
	if len(slept) != 4 {
		t.Fatalf("expected 4 sleeps including post-restore retry, got %d", len(slept))
	}
	if slept[3] != delays[len(delays)-1] {
		t.Fatalf("post-restore sleep = %v, want %v", slept[3], delays[len(delays)-1])
	}
}

func TestFocusGhosttyWindow_UsesExactFocusBeforeFallback(t *testing.T) {
	t.Run("exact success skips fallback", func(t *testing.T) {
		fallbackCalled := false
		err := focusGhosttyWindow(
			123,
			"com.mitchellh.ghostty",
			"/tmp/project",
			func(string) error { return nil },
			func(int, string, string) error {
				fallbackCalled = true
				return nil
			},
		)
		if err != nil {
			t.Fatalf("focusGhosttyWindow returned error: %v", err)
		}
		if fallbackCalled {
			t.Fatalf("fallback should not run when exact focus succeeds")
		}
	})

	t.Run("exact failure falls back", func(t *testing.T) {
		fallbackCalled := false
		wantErr := errors.New("fallback failed")
		err := focusGhosttyWindow(
			123,
			"com.mitchellh.ghostty",
			"/tmp/project",
			func(string) error { return errors.New("automation denied") },
			func(gotPID int, gotBundleID, gotCWD string) error {
				fallbackCalled = true
				if gotPID != 123 {
					t.Fatalf("fallback pid = %d, want 123", gotPID)
				}
				if gotBundleID != "com.mitchellh.ghostty" {
					t.Fatalf("fallback bundleID = %q", gotBundleID)
				}
				if gotCWD != "/tmp/project" {
					t.Fatalf("fallback cwd = %q", gotCWD)
				}
				return wantErr
			},
		)
		if !fallbackCalled {
			t.Fatalf("fallback should run when exact focus fails")
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("focusGhosttyWindow error = %v, want %v", err, wantErr)
		}
	})
}

func TestGhosttyFocusCandidates_UsesCleanAndResolvedPaths(t *testing.T) {
	tempDir := t.TempDir()
	realRoot := filepath.Join(tempDir, "real")
	realDir := filepath.Join(realRoot, "project")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll realDir: %v", err)
	}

	linkRoot := filepath.Join(tempDir, "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	input := filepath.Join(linkRoot, ".", "project") + "/"
	resolvedRealDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks realDir: %v", err)
	}
	got := ghosttyFocusCandidates(input)
	want := []string{
		filepath.Clean(input),
		resolvedRealDir,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ghosttyFocusCandidates(%q) = %#v, want %#v", input, got, want)
	}
}

func TestTryGhosttyAppleScriptFocus_UsesNormalizedCandidates(t *testing.T) {
	originalRunner := ghosttyAppleScriptRunner
	t.Cleanup(func() {
		ghosttyAppleScriptRunner = originalRunner
	})

	tempDir := t.TempDir()
	realRoot := filepath.Join(tempDir, "real")
	realDir := filepath.Join(realRoot, "project")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll realDir: %v", err)
	}

	linkRoot := filepath.Join(tempDir, "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	input := filepath.Join(linkRoot, ".", "project") + "/"
	resolvedRealDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks realDir: %v", err)
	}

	var gotCandidates []string
	ghosttyAppleScriptRunner = func(candidates []string) error {
		gotCandidates = append([]string(nil), candidates...)
		return nil
	}

	if err := tryGhosttyAppleScriptFocus(input); err != nil {
		t.Fatalf("tryGhosttyAppleScriptFocus returned error: %v", err)
	}

	wantCandidates := []string{
		filepath.Clean(input),
		resolvedRealDir,
	}
	if !reflect.DeepEqual(gotCandidates, wantCandidates) {
		t.Fatalf("runner candidates = %#v, want %#v", gotCandidates, wantCandidates)
	}
}

func TestTryGhosttyExactFocus_PrefersTerminalID(t *testing.T) {
	originalIDRunner := ghosttyAppleScriptIDRunner
	originalRunner := ghosttyAppleScriptRunner
	t.Cleanup(func() {
		ghosttyAppleScriptIDRunner = originalIDRunner
		ghosttyAppleScriptRunner = originalRunner
	})

	gotID := ""
	ghosttyAppleScriptIDRunner = func(terminalID string) error {
		gotID = terminalID
		return nil
	}
	ghosttyAppleScriptRunner = func(candidates []string) error {
		t.Fatalf("cwd fallback should not run when terminal ID succeeds, got %#v", candidates)
		return nil
	}

	err := tryGhosttyExactFocus("/tmp/project", FocusWindowOptions{GhosttyTerminalID: "term-7"})
	if err != nil {
		t.Fatalf("tryGhosttyExactFocus returned error: %v", err)
	}
	if gotID != "term-7" {
		t.Fatalf("terminal ID = %q, want term-7", gotID)
	}
}

func TestTryGhosttyExactFocus_FallsBackToCWDWhenTerminalIDFails(t *testing.T) {
	originalIDRunner := ghosttyAppleScriptIDRunner
	originalRunner := ghosttyAppleScriptRunner
	t.Cleanup(func() {
		ghosttyAppleScriptIDRunner = originalIDRunner
		ghosttyAppleScriptRunner = originalRunner
	})

	ghosttyAppleScriptIDRunner = func(terminalID string) error {
		return errors.New("not found")
	}

	var gotCandidates []string
	ghosttyAppleScriptRunner = func(candidates []string) error {
		gotCandidates = append([]string(nil), candidates...)
		return nil
	}

	err := tryGhosttyExactFocus("/tmp/project", FocusWindowOptions{GhosttyTerminalID: "term-7"})
	if err != nil {
		t.Fatalf("tryGhosttyExactFocus returned error: %v", err)
	}

	wantCandidates := []string{"/tmp/project"}
	if !reflect.DeepEqual(gotCandidates, wantCandidates) {
		t.Fatalf("runner candidates = %#v, want %#v", gotCandidates, wantCandidates)
	}
}

func TestGhosttyFocusAppleScript_RequiresUniqueCWDMatch(t *testing.T) {
	countMatch := strings.Index(ghosttyFocusAppleScript, "set matchCount to matchCount + 1")
	uniqueGuard := strings.Index(ghosttyFocusAppleScript, "if matchCount is 1 then")
	focusMatch := strings.Index(ghosttyFocusAppleScript, "focus matchedTerminal")
	ambiguousGuard := strings.Index(ghosttyFocusAppleScript, "if matchCount is greater than 1 then")

	if countMatch < 0 || uniqueGuard < 0 || focusMatch < 0 || ambiguousGuard < 0 {
		t.Fatalf("Ghostty cwd focus script must count matches and reject ambiguity:\n%s", ghosttyFocusAppleScript)
	}
	if countMatch >= uniqueGuard || uniqueGuard >= focusMatch || focusMatch >= ambiguousGuard {
		t.Fatalf("Ghostty cwd focus script must count all matches before focusing:\n%s", ghosttyFocusAppleScript)
	}
	if strings.Contains(ghosttyFocusAppleScript, "focus t") {
		t.Fatalf("Ghostty cwd focus script must not focus the first matching terminal:\n%s", ghosttyFocusAppleScript)
	}
}
