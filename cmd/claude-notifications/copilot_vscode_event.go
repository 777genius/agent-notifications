package main

import (
	"context"
	"io"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	source "github.com/777genius/agent-notifications/internal/copilotvscodesource"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
)

type localEventArgs struct{ Event, ControlRoot, Binding string }

func parseLocalEventArgs(argv []string) (a localEventArgs, ok bool) {
	if len(argv) != 6 {
		return a, false
	}
	seen := map[string]bool{}
	for i := 0; i < len(argv); i += 2 {
		key, value := argv[i], argv[i+1]
		if seen[key] || !localArgText(value) {
			return a, false
		}
		seen[key] = true
		switch key {
		case "--event":
			a.Event = value
		case "--control-root":
			a.ControlRoot = value
		case "--binding":
			a.Binding = value
		default:
			return a, false
		}
	}
	return a, a.Event == source.Stop && filepath.IsAbs(a.ControlRoot) && len(a.ControlRoot) <= 4096 && len(a.Binding) <= 128 && !strings.HasPrefix(a.Binding, "--")
}
func localArgText(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type localPayloadResult struct {
	data []byte
	err  error
}

// The process owns stdin. Cancellation closes the pipe and joins its reader,
// including panic recovery. The short input timeout only restricts admission.
func readLocalPayload(parent context.Context, input io.ReadCloser) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	results := make(chan localPayloadResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if recover() != nil {
				results <- localPayloadResult{err: io.ErrUnexpectedEOF}
			}
		}()
		data, err := io.ReadAll(io.LimitReader(input, source.MaxPayloadBytes+1))
		results <- localPayloadResult{data: data, err: err}
	}()
	defer func() { _ = input.Close(); <-done }()
	select {
	case r := <-results:
		return r.data, r.err == nil && ctx.Err() == nil && len(r.data) > 0 && len(r.data) <= source.MaxPayloadBytes
	case <-ctx.Done():
		return nil, false
	}
}

// Public N1 composition intentionally has no Gate/effect owner or config load.
// N2a/N2b must supply reviewed installed binding/policy composition. The typed
// source still executes under genuine pre-stdin admission, without diagnostics.
// Public7f3 Local observer encodes {}: this transport alone emits {} plus LF,
// including invalid input/panic. A failed write returns 1 (never blocking 2),
// honestly indicating neutral output could not be delivered. SDK IO is private.
func runCopilotVSCodeEvent(argv []string, input io.ReadCloser, output io.Writer) (code int) {
	signal.Ignore(syscall.SIGPIPE)
	defer func() {
		_ = recover()
		_ = input.Close()
		if _, err := io.WriteString(output, "{}\n"); err != nil {
			code = 1
		}
	}()
	ctx, _, cancel, err := observation.Admission(context.Background(), notifier.SystemBootClock{})
	if err != nil {
		return 0
	}
	defer cancel()
	a, ok := parseLocalEventArgs(argv)
	if !ok {
		return 0
	}
	owned, ok := prepareLocalInput(input)
	if !ok {
		return 0
	}
	defer func() { _ = owned.Close() }()
	payload, ok := readLocalPayload(ctx, owned)
	if !ok {
		return 0
	}
	_, _ = source.Decode(ctx, a.Event, payload)
	return 0
}
