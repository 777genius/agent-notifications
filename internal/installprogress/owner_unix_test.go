//go:build !windows

package installprogress

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMissingOwnerExitsAndRestoresTerminal(t *testing.T) {
	if !ownerAlive(os.Getpid()) {
		t.Fatal("live installer reported dead")
	}
	var out bytes.Buffer
	err := Run(context.Background(), eventFile(t, "claude\tconfig\trunning\n"), Options{Output: &out, Rich: true, OwnerPID: 1 << 30})
	if err == nil {
		t.Fatal("observer stayed alive for missing producer")
	}
	if got := out.String(); !strings.HasSuffix(got, "\x1b[0m\x1b[?25h") {
		t.Fatalf("missing owner left cursor hidden: %q", got)
	}
}

func TestFIFOEventPathCannotBlockObserverStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events-fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), path, Options{Output: &bytes.Buffer{}}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("observer blocked opening a FIFO")
	}
}
