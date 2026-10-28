// Package commandrun bounds trusted notification helper commands and owns their
// process trees. It is not a shell or a retrying command executor.
package commandrun

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
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
type boundedOutput struct {
	mu       sync.Mutex
	stdout   bytes.Buffer
	written  int
	cancel   context.CancelFunc
	overflow bool
}

type outputWriter struct {
	output *boundedOutput
	stdout bool
}

func (w outputWriter) Write(p []byte) (int, error) {
	b := w.output
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := OutputLimit - b.written
	n := min(len(p), remaining)
	if w.stdout {
		_, _ = b.stdout.Write(p[:n])
	}
	b.written += n
	if len(p) > remaining {
		b.overflow = true
		b.cancel()
		return n, ErrOutputLimit
	}
	return n, nil
}

// Output returns stdout, like exec.Cmd.Output, while bounding stdout and stderr
// together. Diagnostics cannot corrupt parsed stdout, and either stream can
// exhaust the shared budget and cancel the helper before its ordinary deadline.
func Output(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	if ctx == nil || cmd == nil {
		return nil, errors.New("invalid_helper_command")
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &boundedOutput{cancel: cancel}
	cmd.Stdout = outputWriter{output: b, stdout: true}
	cmd.Stderr = outputWriter{output: b}
	err := Run(operation, cmd)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		err = errors.Join(ErrOutputLimit, err)
	}
	return b.stdout.Bytes(), err
}
