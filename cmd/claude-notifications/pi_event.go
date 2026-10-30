package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/pievent"
	"github.com/777genius/agent-notifications/internal/pisource"
)

// The composition root supplies the receipt-backed resolver. No frame field,
// ambient HOME, or executable path grants authority in this transport.
type piRuntimeResolver func(context.Context, string, pisource.Facts) (pievent.Consumer, error)

var piLocatorName = regexp.MustCompile(`^agent-notify-[0-9a-f]{64}\.json$`)

func parsePiEventArgs(args []string) (string, bool) {
	if len(args) != 2 || args[0] != "--binding" || len(args[1]) > 4096 || !filepath.IsAbs(args[1]) || filepath.Clean(args[1]) != args[1] || !piLocatorName.MatchString(filepath.Base(args[1])) {
		return "", false
	}
	return args[1], true
}

// runPiEvent is intentionally composed explicitly by main: the resolver must
// be the installed Pi authority adapter, never a generic/default source.
func runPiEvent(args []string, input io.ReadCloser, output io.Writer, resolve piRuntimeResolver) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runPiEventWith(ctx, notifier.SystemBootClock{}, args, input, output, resolve)
}

func readPiPayload(parent context.Context, input io.ReadCloser) ([]byte, bool) {
	if input == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{err: io.ErrUnexpectedEOF}
			}
		}()
		data, err := io.ReadAll(io.LimitReader(input, pisource.MaxPayloadBytes+1))
		done <- result{data, err}
	}()
	select {
	case r := <-done:
		return r.data, r.err == nil && ctx.Err() == nil && len(r.data) > 0 && len(r.data) <= pisource.MaxPayloadBytes
	case <-ctx.Done():
		_ = input.Close()
		return nil, false
	}
}

func runPiEventWith(parent context.Context, clock observation.Clock, args []string, input io.ReadCloser, output io.Writer, resolve piRuntimeResolver) (code int) {
	defer func() {
		_ = recover()
		if input != nil {
			_ = input.Close()
		}
		if output != nil {
			_, _ = io.WriteString(output, "{}\n")
		}
	}()
	ctx, deadline, cancel, err := pievent.Admission(parent, clock)
	if err != nil {
		return 0
	}
	defer cancel()
	pievent.TraceTest(ctx, "admission", "ready")
	selector, ok := parsePiEventArgs(args)
	if !ok || resolve == nil {
		return 0
	}
	payload, ok := readPiPayload(ctx, input)
	if !ok {
		return 0
	}
	facts, err := pisource.Decode(ctx, payload)
	// The native extension starts this executable directly without a shell or
	// detachment. A claimed Pi PID must be that actual parent, not JSON authority.
	if err != nil || ctx.Err() != nil || facts.PiPID != os.Getppid() {
		return 0
	}
	consumer, err := resolve(ctx, selector, facts)
	if err != nil || ctx.Err() != nil {
		pievent.TraceTest(ctx, "resolve", "refused")
		return 0
	}
	pievent.TraceTest(ctx, "resolve", "ready")
	receipt := consumer.Consume(ctx, facts, deadline)
	pievent.TraceTest(ctx, "desktop_result", receipt.Desktop)
	pievent.TraceTest(ctx, "webhook_result", receipt.Webhook)
	pievent.TraceTest(ctx, "consumer_result", receipt.Reason)
	return 0
}
