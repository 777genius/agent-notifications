package notifier

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// killRecordedProcess kills the process whose PID the stub recorded. It stays
// quiet about every failure: the process may already be gone, and the stub may
// have been killed before it got as far as writing the file.
func killRecordedProcess(pidPath string) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
}

// dump-layout is answered by the running zellij server, so a session that has
// stopped answering would otherwise hold up the notification behind it.
func TestGetZellijTabTarget_TimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub zellij is a /bin/sh script")
	}

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	// The stub hangs in a child rather than in itself, which is the case the
	// deadline alone does not cover: killing the stub does not close the stdout
	// pipe the child still holds, and Output waits on that pipe. Written as
	// `exec sleep 30` this test would pass with WaitDelay deleted.
	script := "#!/bin/sh\nsleep 30 &\necho $! > \"" + pidPath + "\"\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "zellij"), []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write the stub zellij: %v", err)
	}
	// The kill orphans that child, so the test reaps it rather than leaving a
	// sleep behind for every run.
	t.Cleanup(func() { killRecordedProcess(pidPath) })
	// The real $PATH stays behind the stub because sleep is a program rather
	// than a shell builtin; without it the stub would exit at once and the test
	// would pass for the wrong reason. The stub still wins for the name zellij.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")

	original := zellijLayoutTimeout
	zellijLayoutTimeout = 50 * time.Millisecond
	t.Cleanup(func() { zellijLayoutTimeout = original })

	start := time.Now()
	_, _, err := GetZellijTabTarget()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("GetZellijTabTarget() = nil error, want a timeout")
	}
	// The reason has to survive the wrapping: "signal: killed" on its own would
	// send a reader looking for a crash that never happened.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("GetZellijTabTarget() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("GetZellijTabTarget() took %v, want it to give up at the timeout", elapsed)
	}
}
