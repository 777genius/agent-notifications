// Package cli implements the bounded, one-shot notify command, without global initialization.
package cli

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

const (
	RawLimit     = 64 << 10
	DecodedLimit = 16 << 10
	ContextLimit = 16 << 10
	ExitSuccess  = 0
	ExitRejected = 1
	ExitInvalid  = 2
	ExitUnknown  = 3
)

// Backend is borrowed from composition. Notify must honor cancellation and the
// original boot-continuous deadline. Run calls it at most once and never closes it.
type Backend interface {
	Notify(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt
}

type Options struct {
	Backend Backend
	Clock   agentnotify.Clock
	// ReadContext is a test/composition seam, not caller input. Nil uses the secure
	// platform reader. Overrides must honor cancellation and return promptly.
	ReadContext func(context.Context, string) ([]byte, error)
	// CloseResources releases only resources exclusively transferred to this Run.
	// It runs exactly once, after input and all adapter work have joined, even on help.
	// Do not use it to close a shared backend.
	CloseResources func() error
}

func absent(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return r.IsNil()
	}
	return false
}

// Run accepts arguments AFTER "notify". Stdin is one JSON document framed by EOF;
// it owns input, whose Close MUST unblock Read, and closes it exactly once.
// Output and diagnostics are borrowed writers that must return promptly (or be
// cancellation-aware); Run never closes them. Errors never include input or paths.
// Help does not read stdin, context, clock, or backend. No argument is content.
func Run(ctx context.Context, args []string, input io.ReadCloser, output, diagnostics io.Writer, o Options) (code int) {
	var once sync.Once
	closeInput := func() {
		once.Do(func() {
			if !absent(input) {
				_ = input.Close()
			}
		})
	}
	defer func() {
		closeInput()
		if o.CloseResources != nil && o.CloseResources() != nil {
			if !absent(diagnostics) {
				_, _ = io.WriteString(diagnostics, "resource_close_failed\n")
			}
			if code == ExitSuccess {
				code = ExitInvalid
			}
		}
	}()
	fail := func(reason string) int {
		if !absent(diagnostics) {
			_, _ = io.WriteString(diagnostics, reason+"\n")
		}
		return ExitInvalid
	}
	path, help, valid := flags(args)
	if !valid {
		return fail("invalid_flags")
	}
	if absent(output) {
		return fail("invalid_options")
	}
	if help {
		if _, err := io.WriteString(output, "Usage: notify [--context-file PATH] [--help]\nRead one literal notification JSON document from stdin through EOF.\n"); err != nil {
			return fail("output_failed")
		}
		return ExitSuccess
	}
	if ctx == nil || absent(input) || absent(o.Backend) || absent(o.Clock) {
		return fail("invalid_options")
	}
	// The watcher only closes the owned reader; joining it prevents a late Close.
	stop, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			closeInput()
		case <-stop:
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(input, RawLimit+1))
	// Capture before any JSON decoding, context loading, or cleanup. Oversize/error
	// frames have no backend deadline because they never become requests.
	var deadline notification.Deadline
	if err == nil && len(raw) <= RawLimit {
		deadline = o.Clock.Now()
		deadline.NotAfter += 15
	}
	close(stop)
	<-joined
	closeInput()
	if ctx.Err() != nil {
		return fail("input_cancelled")
	}
	if err != nil {
		return fail("input_failed")
	}
	if len(raw) > RawLimit {
		return fail("input_too_large")
	}
	p, ok := payload(raw)
	if !ok {
		return fail("invalid_content_json")
	}
	now := o.Clock.Now()
	if !remaining(now, deadline) {
		return emit(output, diagnostics, rejection(p.RequestID, "deadline_expired"))
	}
	work, cancel := context.WithTimeout(ctx, time.Duration((deadline.NotAfter-now.NotAfter)*float64(time.Second)))
	// A Go timer alone omits suspend on some platforms. This joined watcher uses
	// the injected continuous clock, and fails closed on an epoch change.
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				if !remaining(o.Clock.Now(), deadline) {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	orig := origin.Context{Provider: "cli", Namespace: "cli", AnonymousCaller: "local_cli", Provenance: origin.CallerAsserted, Locality: origin.LocalityUnknown, Interface: origin.InterfaceUnknown}
	if path != "" {
		read := o.ReadContext
		if read == nil {
			read = ReadSecureContext
		}
		b, e := read(work, path)
		if e != nil {
			return emit(output, diagnostics, rejection(p.RequestID, "context_unavailable"))
		}
		if orig, ok = envelope(b); !ok {
			return emit(output, diagnostics, rejection(p.RequestID, "invalid_context"))
		}
	}
	if orig.SessionID == "" && p.Navigation != notification.None {
		return emit(output, diagnostics, rejection(p.RequestID, "session_required"))
	}
	if work.Err() != nil || !remaining(o.Clock.Now(), deadline) {
		return emit(output, diagnostics, rejection(p.RequestID, "deadline_expired"))
	}
	// Keep a private copy: even a faulty backend cannot mutate the echoed ID.
	var requestID *string
	if p.RequestID != nil {
		id := *p.RequestID
		requestID = &id
	}
	r := call(work, o.Backend, p, orig, deadline)
	// The caller's validated request ID survives every backend outcome unchanged.
	r.RequestID = requestID
	return emit(output, diagnostics, r)
}

func flags(args []string) (path string, help, valid bool) {
	if len(args) > 3 {
		return "", false, false
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if seen[a] {
			return "", false, false
		}
		seen[a] = true
		switch a {
		case "--help":
			help = true
		case "--context-file":
			i++
			if i == len(args) || args[i] == "" || len(args[i]) > 4096 {
				return "", false, false
			}
			path = args[i]
		default:
			return "", false, false
		}
	}
	return path, help, true
}
func remaining(now, d notification.Deadline) bool {
	return origin.Text(now.BootID, 256, true) && now.NotAfter >= 0 && now.BootID == d.BootID && !math.IsNaN(now.NotAfter) && !math.IsInf(now.NotAfter, 0) && !math.IsNaN(d.NotAfter) && !math.IsInf(d.NotAfter, 0) && d.NotAfter > now.NotAfter && d.NotAfter-now.NotAfter <= 15
}
func rejection(id *string, reason string) agentnotify.Receipt {
	return agentnotify.Receipt{RequestID: id, Status: "rejected", Reason: reason, RetrySafe: true}
}
func call(ctx context.Context, b Backend, p agentnotify.Payload, o origin.Context, d notification.Deadline) (r agentnotify.Receipt) {
	defer func() {
		if recover() != nil {
			r = agentnotify.Receipt{Status: "unknown", Reason: "internal_error"}
		}
	}()
	return b.Notify(ctx, p, o, d)
}
func emit(out, diagnostics io.Writer, r agentnotify.Receipt) int {
	valid := r.Status == "submitted" || r.Status == "suppressed" || r.Status == "rejected" || r.Status == "unknown"
	for _, s := range []string{r.TrackingID, string(r.KeyKind), r.Reason, r.Backend, r.Navigation.Capability, r.Navigation.Precision, r.Navigation.Scope, r.Navigation.Reason} {
		valid = valid && origin.Text(s, 256, false)
	}
	if !valid {
		r = agentnotify.Receipt{RequestID: r.RequestID, Status: "unknown", Reason: "invalid_backend_result"}
	}
	if r.Status == "unknown" {
		r.RetrySafe = false
	}
	if err := json.NewEncoder(out).Encode(r); err != nil {
		if !absent(diagnostics) {
			_, _ = io.WriteString(diagnostics, "output_failed\n")
		}
		return ExitUnknown
	}
	switch r.Status {
	case "submitted", "suppressed":
		return ExitSuccess
	case "rejected":
		return ExitRejected
	default:
		return ExitUnknown
	}
}
