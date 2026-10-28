// Package commandrun bounds trusted notification helper commands and owns their
// process trees. It is not a shell or a retrying command executor.
package commandrun

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const OutputLimit = 64 << 10

var ErrOutputLimit = errors.New("helper_output_limit")

// Run starts exactly one command, cancels its process tree, and waits exactly
// once. On natural exit any remaining descendants are also stopped. Callers
// supply argv and trusted environment directly; no shell is introduced.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if ctx == nil || cmd == nil {
		return errors.New("invalid_helper_command")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tree, err := prepareTree(cmd)
	if err != nil {
		return err
	}
	defer tree.close()
	cmd.WaitDelay = 100 * time.Millisecond
	if err = cmd.Start(); err != nil {
		return err
	}
	if err = tree.attach(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
		return err
	case <-ctx.Done():
		_ = tree.kill()
		err = <-done
		return errors.Join(ctx.Err(), err)
	}
}

// Do not embed bytes.Buffer: its promoted ReadFrom would let io.Copy bypass
// Write and therefore the output limit.
type limitedBuffer struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := OutputLimit - b.buffer.Len()
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.overflow = true
		b.cancel()
		return remaining, ErrOutputLimit
	}
	return b.buffer.Write(p)
}

// Output bounds both output streams together; cancellation on overflow prevents
// a producer from holding the caller until its ordinary deadline.
func Output(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	if ctx == nil || cmd == nil {
		return nil, errors.New("invalid_helper_command")
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &limitedBuffer{cancel: cancel}
	// os/exec serializes calls when stdout/stderr are the same writer.
	cmd.Stdout, cmd.Stderr = io.Writer(b), io.Writer(b)
	err := Run(operation, cmd)
	if b.overflow {
		err = errors.Join(ErrOutputLimit, err)
	}
	return b.buffer.Bytes(), err
}
