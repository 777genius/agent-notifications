package installprogress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func eventFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlainPreservesPartialTruthWithoutANSI(t *testing.T) {
	// A mistaken committed=>success conversion, unstable duplicate output, or
	// terminal escapes in the plain fallback makes this observable contract fail.
	path := eventFile(t, "claude\tregistration\tregistration_committed\nclaude\tregistration\tregistration_committed\ncodex\tMCP\tstate_requires_inspection\ngemini\tcomplete\tcompleted\nstop\t\t\n")
	var out bytes.Buffer
	if err := Run(context.Background(), path, Options{Output: &out}); err != nil {
		t.Fatal(err)
	}
	want := "Claude Code: registration - registration committed (partial)\nCodex CLI: MCP - needs inspection\nGemini CLI: complete - done\n"
	if out.String() != want {
		t.Fatalf("plain output = %q", out.String())
	}
}

func TestRichPartialNeverHasSuccessMarkAndClips(t *testing.T) {
	path := eventFile(t, "claude\tregistration\tregistration_committed_follow_up_not_started\ncodex\tMCP\tstate_requires_inspection\nstop\t\t\n")
	var out bytes.Buffer
	if err := Run(context.Background(), path, Options{Output: &out, Rich: true, Width: 25}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "✓") || !strings.Contains(out.String(), "! Claude Code") {
		t.Fatalf("uncertain state displayed as success: %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "\x1b[0m\x1b[?25h") {
		t.Fatal("cursor not restored")
	}
	// Strip only the renderer's known control sequences before checking width.
	for _, line := range strings.Split(out.String(), "\n") {
		for _, control := range []string{"\x1b[?25l", "\x1b[?25h", "\x1b[0m", "\x1b[33m", "\x1b[2K", "\x1b[1A", "\x1b[2A", "\x1b[3A", "\r"} {
			line = strings.ReplaceAll(line, control, "")
		}
		if len([]rune(line)) > 24 {
			t.Fatalf("wrapped line: %q", line)
		}
	}
}

type observedOutput struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	frames chan string
}

func (o *observedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buf.Write(p)
	o.mu.Unlock()
	select {
	case o.frames <- string(p):
	default:
	}
	return n, err
}

func (o *observedOutput) String() string { o.mu.Lock(); defer o.mu.Unlock(); return o.buf.String() }

func waitFrame(t *testing.T, o *observedOutput, contains string) string {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case frame := <-o.frames:
			if strings.Contains(frame, contains) {
				return frame
			}
		case <-timer.C:
			t.Fatalf("missing live frame %q in %q", contains, o.String())
			return ""
		}
	}
}

func TestAppendLifecyclePaintsAndAnimatesBeforeCompletion(t *testing.T) {
	// Catch buffering the entire installer until stop, a frozen spinner, and an
	// EOF interpreted as completion. The writer appends the final event later.
	path := eventFile(t, "claude\tconfig\trunning\n")
	out := &observedOutput{frames: make(chan string, 32)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, path, Options{Output: out, Rich: true, Interval: time.Millisecond}) }()
	first := waitFrame(t, out, "Claude Code")
	second := waitFrame(t, out, "Claude Code")
	if first == second {
		t.Fatal("spinner did not advance")
	}
	select {
	case err := <-done:
		t.Fatalf("exited before completion: %v", err)
	default:
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("claude\tcomplete\tcompleted\nstop\t\t\n")
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "✓ Claude Code") || !strings.HasSuffix(out.String(), "\x1b[0m\x1b[?25h") {
		t.Fatal("final board or cursor restoration missing")
	}
}

func TestLiveUncertaintyShowsWorkButStopPreservesFinalTruth(t *testing.T) {
	path := eventFile(t, "global\tinstalling-claude\trunning\nclaude\tregistration\tstate_requires_inspection\ncodex\tregistration\tregistration_committed\n")
	out := &observedOutput{frames: make(chan string, 64)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, path, Options{Output: out, Rich: true, Interval: time.Millisecond}) }()
	live := waitFrame(t, out, "Registered · follow-up pending")
	if !strings.Contains(live, "Registering notifications") || strings.Contains(live, "needs inspection") || strings.Contains(live, "partial") || strings.Contains(live, "✓") {
		t.Fatalf("normal live work shown as an alarm or success: %q", live)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("stop\t\t\n")
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	final := waitFrame(t, out, "needs inspection")
	if !strings.Contains(final, "registration committed (partial)") || !strings.Contains(final, "! Claude Code") || !strings.Contains(final, "! Codex CLI") || strings.Contains(final, "✓") || strings.Contains(final, "Registering notifications") {
		t.Fatalf("stopped board lost authoritative uncertainty: %q", final)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFragmentedEventWaitsForNewlineAndCancellationRestoresCursor(t *testing.T) {
	path := eventFile(t, "codex\tconfig\trun")
	out := &observedOutput{frames: make(chan string, 32)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, path, Options{Output: out, Rich: true, Transient: true, Interval: time.Millisecond})
	}()
	waitFrame(t, out, "Agent Notifications")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("ning\n")
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	waitFrame(t, out, "Codex CLI")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel returned %v", err)
	}
	if !strings.HasSuffix(out.String(), "\x1b[0m\x1b[?25h") {
		t.Fatal("cancel left cursor hidden")
	}
}

func TestMalformedEventsAreBoundedAndCannotInjectTerminalControls(t *testing.T) {
	for _, content := range []string{
		"claude\t\x1b[2J\trunning\n", "evil\tconfig\trunning\n",
		"claude\tconfig\tunknown\n", "stop\tconfig\tdone\n",
		strings.Repeat("x", 257), strings.Repeat("x", 257) + "\n",
	} {
		t.Run(strings.ReplaceAll(content[:min(len(content), 18)], "\n", "newline"), func(t *testing.T) {
			var out bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := Run(ctx, eventFile(t, content), Options{Output: &out, Rich: true}); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("malformed input did not exit promptly: %v", err)
			}
			if strings.Contains(out.String(), "evil") || strings.Contains(out.String(), "\x1b[2J") {
				t.Fatal("untrusted input reached terminal")
			}
			if !strings.HasSuffix(out.String(), "\x1b[0m\x1b[?25h") {
				t.Fatal("error left cursor hidden")
			}
		})
	}
}

func TestTruncatedRecordCannotHangPastContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	err := Run(ctx, eventFile(t, "claude\tconfig\trunning"), Options{Output: &out, Interval: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unterminated event returned %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("unterminated event rendered")
	}
}

func TestOutputFailureStillAttemptsCursorRestoration(t *testing.T) {
	out := &failingOutput{}
	err := Run(context.Background(), eventFile(t, "stop\t\t\n"), Options{Output: out, Rich: true})
	if err == nil || out.calls != 2 || out.last != "\x1b[0m\x1b[?25h" {
		t.Fatalf("failed output cleanup: err=%v calls=%d last=%q", err, out.calls, out.last)
	}
}

type failingOutput struct {
	calls int
	last  string
}

func (o *failingOutput) Write(p []byte) (int, error) {
	o.calls++
	o.last = string(p)
	return 0, errors.New("closed output")
}

func TestShortWriteFailsObserverRatherThanCorruptingCursorAccounting(t *testing.T) {
	err := Run(context.Background(), eventFile(t, "stop\t\t\n"), Options{Output: shortOutput{}, Rich: true})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write returned %v", err)
	}
}

type shortOutput struct{}

func (shortOutput) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestCardRemainsWithinNarrowAndWideTerminalWidths(t *testing.T) {
	path := eventFile(t, "global\tshared-settings\trunning\nclaude\tcomplete\tcompleted\nstop\t\t\n")
	controls := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	for _, width := range []int{1, 2, 25, 39, 40, 41, 80, 108} {
		var out bytes.Buffer
		if err := Run(context.Background(), path, Options{Output: &out, Rich: true, Width: width}); err != nil {
			t.Fatal(err)
		}
		plain := strings.ReplaceAll(controls.ReplaceAllString(out.String(), ""), "\r", "")
		for _, line := range strings.Split(plain, "\n") {
			if len([]rune(line)) > width-1 {
				t.Fatalf("width %d wraps %q", width, line)
			}
		}
		if width == 108 && (!strings.Contains(plain, "╭") || !strings.Contains(plain, "Checking shared settings") || !strings.Contains(plain, "Installed · restart required")) {
			t.Fatalf("card missing active/final information: %q", plain)
		}
	}
}

// A shared "registration saved; activation" prefix used to hide the current
// attempt at narrow widths. Exercise the real renderer and its cursor bounds.
func TestActivationRowsRemainDistinctAtNarrowWidths(t *testing.T) {
	path := eventFile(t, "claude\tMCP\tactivation_timeout\ncodex\tMCP\tactivation_not_attempted\nstop\t\t\n")
	controls := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	for _, width := range []int{32, 50, 60, 80} {
		var out bytes.Buffer
		if err := Run(context.Background(), path, Options{Output: &out, Rich: true, Width: width}); err != nil {
			t.Fatal(err)
		}
		plain := strings.ReplaceAll(controls.ReplaceAllString(out.String(), ""), "\r", "")
		for _, want := range []string{"Claude Code: Timed out", "Codex CLI: Not started", "Registration saved"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("width %d hid %q: %q", width, want, plain)
			}
		}
		for _, line := range strings.Split(plain, "\n") {
			if len([]rune(line)) > width-1 {
				t.Fatalf("width %d wrapped %q", width, line)
			}
		}
		if strings.Contains(plain, "✓") || !strings.HasSuffix(out.String(), "\x1b[0m\x1b[?25h") {
			t.Fatalf("width %d lost partial/cursor truth: %q", width, out.String())
		}
	}
}
