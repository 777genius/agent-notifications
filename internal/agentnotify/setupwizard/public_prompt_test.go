//go:build linux || darwin

package setupwizard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/cli/installerui"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Red regression: production PlainUI's action/unit menu uses a blocking legacy
// read that survives cancellation and races the next owner of the borrowed TTY.
func TestWizardPlainMenuCancellationPTY(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("native PTY unavailable: %v", err)
	}
	defer func() { _ = slave.Close() }()
	defer func() { _ = master.Close() }()
	prompt, err := NewTerminalPublicPrompt(slave, slave, installerui.ModePlain, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, _, err := prompt.SelectUnits(ctx); finished <- err }()
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	rendered := make(chan bool, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 512)
		var text strings.Builder
		for text.Len() < 4096 && readCtx.Err() == nil {
			poll := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
			nready, e := unix.Poll(poll, 100)
			if errors.Is(e, unix.EINTR) {
				continue
			}
			if e != nil {
				rendered <- false
				return
			}
			if nready == 0 {
				continue
			}
			n, e := master.Read(buf)
			if n > 0 {
				text.Write(buf[:n])
			}
			if strings.Contains(text.String(), "Choose one by number or id") {
				rendered <- true
				return
			}
			if e != nil {
				rendered <- false
				return
			}
		}
		rendered <- false
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case ok := <-rendered:
		if !ok {
			t.Fatal("menu did not render")
		}
	case <-deadline.C:
		readCancel()
		<-readerDone
		t.Fatal("menu render timed out")
	}
	<-readerDone
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("menu cancellation=%v", err)
		}
	case <-deadline.C:
		_ = master.Close()
		t.Fatal("menu read survived cancellation")
	}
	if _, err := slave.Stat(); err != nil {
		t.Fatalf("borrowed descriptor closed: %v", err)
	}
	// The first reader is fully joined: the next shared menu can read the answer.
	nextCtx, nextCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer nextCancel()
	next := make(chan error, 1)
	go func() { _, _, err := prompt.SelectUnits(nextCtx); next <- err }()
	if _, err := master.Write([]byte("1\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatalf("next menu lost answer: %v", err)
		}
	case <-nextCtx.Done():
		t.Fatal("next terminal owner blocked")
	}
}
