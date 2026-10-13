package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type busyTranscript struct {
	mu     sync.Mutex
	text   bytes.Buffer
	writes chan string
}

func (w *busyTranscript) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.text.Write(p)
	w.mu.Unlock()
	select {
	case w.writes <- string(p):
	default:
	}
	return n, err
}

func (w *busyTranscript) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.String()
}

func busyNextWrite(t *testing.T, writes <-chan string) string {
	t.Helper()
	select {
	case text := <-writes:
		return text
	case <-time.After(2 * time.Second):
		t.Fatal("busy indicator did not write before operation completion")
		return ""
	}
}

// Regression: slow inspection completes before its first frame, or the busy
// writer races the subsequent prompt after the wrapper returns.
func TestSetupProductsBusyFirstFrameAndJoin(t *testing.T) {
	out := &busyTranscript{writes: make(chan string, 32)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	label := "Checking installed clients…"
	err := runSetupProductsBusy(ctx, out, true, label, func() error {
		first := busyNextWrite(t, out.writes)
		if !strings.Contains(first, label) {
			t.Fatalf("operation started without phase: %q", first)
		}
		second := busyNextWrite(t, out.writes)
		if second == first || !strings.Contains(second, label) {
			t.Fatalf("inspection did not animate while pending: %q", second)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "\r\x1b[2K") {
		t.Fatal("busy line was not cleared before returning")
	}
	_, _ = io.WriteString(out, "NEXT PROMPT\n")
	finished := out.String()
	time.Sleep(2 * setupProductsBusyInterval)
	if got := out.String(); got != finished {
		t.Fatalf("detached busy writer changed next prompt: %q", got)
	}
}

// Regression: cancellation leaves an animation active until the inspection
// returns, or cleanup is suppressed by the cancelled context.
func TestSetupProductsBusyCancellationStopsAnimation(t *testing.T) {
	out := &busyTranscript{writes: make(chan string, 32)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := runSetupProductsBusy(ctx, out, true, "Preparing installation plan…", func() error {
		_ = busyNextWrite(t, out.writes)
		cancel()
		for !strings.HasSuffix(out.String(), "\r\x1b[2K") {
			_ = busyNextWrite(t, out.writes)
		}
		cancelled := out.String()
		time.Sleep(2 * setupProductsBusyInterval)
		if got := out.String(); got != cancelled {
			t.Fatalf("cancelled animation continued: %q", got)
		}
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost operation cancellation: %v", err)
	}
	finished := out.String()
	time.Sleep(2 * setupProductsBusyInterval)
	if got := out.String(); got != finished {
		t.Fatal("busy writer remained after cancellation return")
	}
}

func TestSetupProductsBusyCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, rich := range []bool{false, true} {
		var prompts bytes.Buffer
		called := false
		err := runSetupProductsBusy(ctx, &prompts, rich, "Checking installed clients…", func() error {
			called = true
			return nil
		})
		if !errors.Is(err, context.Canceled) || called || prompts.Len() != 0 {
			t.Fatalf("cancelled before start: rich=%v err=%v called=%v prompts=%q", rich, err, called, prompts.String())
		}
	}
}

// Regression: plain fallback contains cursor escapes or repetitive animation,
// or phase text is routed to the machine-result stream.
func TestSetupProductsBusyPlainOutput(t *testing.T) {
	var prompts, result bytes.Buffer
	err := runSetupProductsBusy(context.Background(), &prompts, false, "Rechecking installation…", func() error {
		if got := prompts.String(); got != "Rechecking installation…\n" {
			t.Fatalf("plain first phase: %q", got)
		}
		return writeSelectorResult(context.Background(), &result, "approved\n")
	})
	if err != nil || prompts.String() != "Rechecking installation…\n" || result.String() != "approved\n" {
		t.Fatalf("plain/result streams: err=%v prompts=%q result=%q", err, prompts.String(), result.String())
	}
	if setupProductsBusyRich(strings.NewReader(""), &prompts, "auto") || setupProductsBusyRich(strings.NewReader(""), &prompts, "rich") {
		t.Fatal("injected line seam qualified as a rich terminal")
	}
}

type busyFailingWriter struct{ writes int }

func (w *busyFailingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// Regression: an animation write failure strands the ticker or causes the
// bounded operation to run again.
func TestSetupProductsBusyDisplayFailureJoins(t *testing.T) {
	out := &busyFailingWriter{}
	calls := 0
	err := runSetupProductsBusy(context.Background(), out, true, "Checking installed clients…", func() error {
		calls++
		time.Sleep(2 * setupProductsBusyInterval)
		return nil
	})
	if !errors.Is(err, io.ErrClosedPipe) || calls != 1 {
		t.Fatalf("display failure: err=%v operations=%d", err, calls)
	}
	writes := out.writes
	time.Sleep(2 * setupProductsBusyInterval)
	if out.writes != writes {
		t.Fatal("failed display left a detached writer")
	}
}
