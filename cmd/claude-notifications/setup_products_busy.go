package main

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/777genius/plugin-kit-ai/cli/installerui"
)

const setupProductsBusyInterval = 100 * time.Millisecond

// Inspection borrows only the prompt output. Mode qualification does not start
// a prompt, consume input, or change terminal settings.
func setupProductsBusyRich(input io.Reader, prompts io.Writer, mode string) bool {
	if mode == "plain" || os.Getenv("NO_COLOR") != "" {
		return false
	}
	in, inputFile := input.(*os.File)
	out, outputFile := prompts.(*os.File)
	if !inputFile || !outputFile {
		return false
	}
	terminal, err := installerui.NewTerminal(installerui.TerminalConfig{
		Input: in, Output: out, Mode: installerui.TerminalMode(mode),
	})
	return err == nil && terminal.Mode() == installerui.ModeRich
}

// The first frame precedes the operation. Animation owns the writer until it
// has stopped and cleared its line, so the next prompt or diagnostic cannot
// race it. The bounded operation stays on the caller's goroutine.
func runSetupProductsBusy(ctx context.Context, prompts io.Writer, rich bool, label string, operation func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !rich {
		if err := writeSelectorResult(ctx, prompts, label+"\n"); err != nil {
			return err
		}
		return operation()
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := func(index int) string {
		return "\r\x1b[2K\x1b[36m" + frames[index] + "\x1b[0m " + label
	}
	clear := func() error {
		// Cleanup must remain writable after cancellation.
		return writeSelectorResult(context.Background(), prompts, "\r\x1b[2K")
	}
	if err := writeSelectorResult(ctx, prompts, frame(0)); err != nil {
		// A partially written first frame also needs cleanup.
		return errors.Join(err, clear())
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var displayErr error
		defer func() {
			done <- errors.Join(displayErr, clear())
		}()
		ticker := time.NewTicker(setupProductsBusyInterval)
		defer ticker.Stop()
		index := 0
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				index = (index + 1) % len(frames)
				if displayErr = writeSelectorResult(ctx, prompts, frame(index)); displayErr != nil {
					return
				}
			}
		}
	}()
	defer func() {
		close(stop)
		err = errors.Join(err, <-done)
	}()
	return operation()
}
