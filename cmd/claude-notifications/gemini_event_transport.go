package main

import (
	"context"
	"flag"
	"io"
	"path/filepath"
	"time"
)

const geminiPayloadLimit = 1 << 20

type geminiEventArgs struct {
	Event, ControlRoot, Binding string
}

// These are installer-owned arguments. Native JSON fields and shell command
// rendering belong to the UAP source/planner, never to this transport.
func parseGeminiEventArgs(args []string) (geminiEventArgs, bool) {
	var parsed geminiEventArgs
	f := flag.NewFlagSet("gemini-event", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&parsed.Event, "event", "", "native event selector")
	f.StringVar(&parsed.ControlRoot, "control-root", "", "managed control root")
	f.StringVar(&parsed.Binding, "binding", "", "installed binding")
	// FlagSet normally accepts repeated flags. A command is fixed at setup;
	// conflicting/repeated selectors must not pick whichever appeared last.
	seen := make(map[string]bool)
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) || seen[args[i]] {
			return parsed, false
		}
		switch args[i] {
		case "--event", "--control-root", "--binding":
			seen[args[i]] = true
		default:
			return parsed, false
		}
	}
	if f.Parse(args) != nil || f.NArg() != 0 || len(seen) != 3 ||
		(parsed.Event != "AfterAgent" && parsed.Event != "Notification") ||
		!filepath.IsAbs(parsed.ControlRoot) || parsed.Binding == "" || len(parsed.Binding) > 128 {
		return parsed, false
	}
	return parsed, true
}

type geminiPayloadResult struct {
	data []byte
	err  error
}

// The event process exclusively owns input. Closing it on expiration ensures
// a still-open native pipe cannot extend the one-second input budget. The source
// later validates exactly one JSON value through its captured SDK invocation.
func readGeminiPayload(ctx context.Context, input io.ReadCloser) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result := make(chan geminiPayloadResult, 1)
	go func() {
		defer func() {
			if recover() != nil {
				result <- geminiPayloadResult{err: io.ErrUnexpectedEOF}
			}
		}()
		data, err := io.ReadAll(io.LimitReader(input, geminiPayloadLimit+1))
		result <- geminiPayloadResult{data: data, err: err}
	}()
	select {
	case r := <-result:
		return r.data, ctx.Err() == nil && r.err == nil && len(r.data) > 0 && len(r.data) <= geminiPayloadLimit
	case <-ctx.Done():
		_ = input.Close()
		return nil, false
	}
}

// The composition callback receives only bounded bytes and fixed argv. It runs
// under the remaining total budget; N1's admission context can further shorten
// this deadline. Only this outer transport writes the neutral native response,
// even if the source/consumer panics or fails. SDK diagnostics stay in source IO.
func runGeminiEventWith(parent context.Context, args []string, input io.ReadCloser, output io.Writer,
	consume func(context.Context, geminiEventArgs, []byte)) (code int) {
	defer func() {
		_ = recover()
		_, _ = io.WriteString(output, "{}\n")
	}()
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	parsed, ok := parseGeminiEventArgs(args)
	if !ok {
		return 0
	}
	payload, ok := readGeminiPayload(ctx, input)
	if !ok || consume == nil {
		return 0
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		consume(ctx, parsed, payload)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return 0
}
