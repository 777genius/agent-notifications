package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/777genius/agent-notifications/internal/geminievent"
)

// Operator diagnostics are best effort. Borrow the existing stdio adapter's
// owned, cancelable descriptor, never the caller's descriptor itself. Sinks
// without poller deadlines (regular files and synchronous Windows pipes) are
// skipped rather than introducing an unbounded syscall or a new timeout.
type geminiDiagnosticOutput struct {
	ctx  context.Context
	file *os.File
}

func (w geminiDiagnosticOutput) Write(data []byte) (int, error) {
	deadline, ok := w.ctx.Deadline()
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if !ok || len(data) > 512 {
		return 0, io.ErrShortWrite
	}
	stream, err := agentNotifyFile(w.file)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stream.Close(); stream.release() }()
	if !stream.pollable || stream.write != nil {
		return len(data), nil
	}
	if err := stream.file.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-w.ctx.Done():
			_ = stream.Close()
		case <-done:
		}
	}()
	defer func() { close(done); <-joined }()
	// Do not use stream.Write: it intentionally starts a separate one-second
	// MCP write timeout, whereas this hook must retain its admission deadline.
	return stream.file.Write(data)
}

// This diagnostic observes the ordinary hook. It never changes admission or
// delivery and contains only closed labels, never input or raw error strings.
// The callback can outlive the transport deadline, so snapshots are synchronized.
type geminiEventDiagnostic struct {
	mu           sync.Mutex
	enabled      bool
	output       io.Writer
	phase, class string
	result       geminievent.Receipt
	finished     bool
}

func (d *geminiEventDiagnostic) recoverPanic() {
	if value := recover(); value != nil {
		d.fail(nil, true)
		panic(value) // The existing transport still owns neutral panic recovery.
	}
}

func (d *geminiEventDiagnostic) enter(phase string) {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	d.phase = phase
	d.mu.Unlock()
}

func (d *geminiEventDiagnostic) fail(err error, panicValue bool) {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.class, d.finished = "unavailable", true
	if panicValue {
		d.class = "panic"
	} else if errors.Is(err, context.DeadlineExceeded) {
		d.class = "deadline"
	} else if errors.Is(err, context.Canceled) {
		d.class = "canceled"
	}
}

func (d *geminiEventDiagnostic) consumed(result geminievent.Receipt) {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	d.result, d.class, d.finished = result, "completed", true
	d.mu.Unlock()
}

// Call before canceling the admission context ourselves. A timeout can return
// while the callback remains blocked, without granting it a fresh budget.
func (d *geminiEventDiagnostic) expired(err error) {
	if !d.enabled || err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.finished {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		d.class = "deadline"
	} else if errors.Is(err, context.Canceled) {
		d.class = "canceled"
	}
}

func diagnosticWord(value string, allowed ...string) string {
	for _, word := range allowed {
		if value == word {
			return value
		}
	}
	return "none"
}

func (d *geminiEventDiagnostic) emit() {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	phase, class, result := d.phase, d.class, d.result
	d.mu.Unlock()
	phase = diagnosticWord(phase, "admission", "input", "decode", "executable", "snapshot", "registration", "config", "consume")
	class = diagnosticWord(class, "unavailable", "deadline", "canceled", "panic", "completed")
	status := diagnosticWord(result.Status, "suppressed", "submitted", "unknown", "rejected")
	reason := diagnosticWord(result.Reason, "invalid_fact", "consumer_unavailable", "unsupported_fact", "expired", "not_registered", "invalid_config", "channels_disabled", "delivery_uncertain", "delivery_unavailable", "cache_unavailable", "no_attempt")
	channel := func(value string) string {
		return diagnosticWord(value, "expired", "cache_unavailable", "duplicate", "revoked", "unavailable", "unknown", "submitted", "rejected", "suppressed")
	}
	_, _ = fmt.Fprintf(d.output, "gemini.event.result phase=%s class=%s status=%s reason=%s desktop=%s webhook=%s\n", phase, class, status, reason, channel(result.Desktop), channel(result.Webhook))
}
