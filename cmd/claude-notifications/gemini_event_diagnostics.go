package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/777genius/agent-notifications/internal/geminievent"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

// The pinned observer deliberately discards stdout/stderr. Diagnostics use
// only a pre-existing private local datagram receiver, never a new authority or
// delivery endpoint. Missing/unsafe receivers and expired budgets drop hints.
type geminiDiagnosticOutput struct {
	ctx                                   context.Context
	mu                                    sync.Mutex
	root, event, sessionSHA, timestampSHA string
}

// This is a semantic hint join only. It does not canonicalize JSON, claim byte
// equality with SDK telemetry, or authorize any business event or delivery.
func (w *geminiDiagnosticOutput) input(root, event string, payload []byte) {
	if event != "AfterAgent" && event != "Notification" || strictjson.Validate(payload, strictjson.Budget{Bytes: geminiPayloadLimit, Depth: 64, Entries: 4096}) != nil {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return
	}
	values := make([]string, 3)
	for i, key := range []string{"hook_event_name", "session_id", "timestamp"} {
		if json.Unmarshal(fields[key], &values[i]) != nil || len(values[i]) == 0 || len(values[i]) > 4096 {
			return
		}
	}
	if values[0] != event {
		return
	}
	session, timestamp := sha256.Sum256([]byte(values[1])), sha256.Sum256([]byte(values[2]))
	w.mu.Lock()
	w.root, w.event, w.sessionSHA, w.timestampSHA = root, event, hex.EncodeToString(session[:]), hex.EncodeToString(timestamp[:])
	w.mu.Unlock()
}

func (w *geminiDiagnosticOutput) Write(data []byte) (int, error) {
	deadline, ok := w.ctx.Deadline()
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if !ok || len(data) > 256 {
		return 0, io.ErrShortWrite
	}
	w.mu.Lock()
	root, event, session, timestamp := w.root, w.event, w.sessionSHA, w.timestampSHA
	w.mu.Unlock()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" || root == "" || len(session) != 64 || len(timestamp) != 64 {
		return len(data), nil
	}
	path := filepath.Join(root, ".gd.sock")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || len(path) > 100 || installruntime.CheckPrivateControlRoot(root) != nil {
		return len(data), nil
	}
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSocket == 0 {
		return len(data), nil
	}
	frame, err := json.Marshal(struct {
		Version         int    `json:"version"`
		Event           string `json:"event"`
		SessionSHA256   string `json:"session_sha256"`
		TimestampSHA256 string `json:"timestamp_sha256"`
		Result          string `json:"result"`
	}{1, event, session, timestamp, string(data)})
	if err != nil || len(frame) > 512 {
		return 0, io.ErrShortWrite
	}
	conn, err := (&net.Dialer{}).DialContext(w.ctx, "unixgram", path)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return len(data), nil
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-w.ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer func() { close(done); <-joined }()
	if _, err := conn.Write(frame); err != nil {
		return 0, err
	}
	return len(data), nil
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
