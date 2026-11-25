//go:build linux || darwin

package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The pre-open Lstat can describe a regular file while the path is swapped to
// a FIFO before open(2). The notification hook must not block in that race.
func TestClaudePermissionTranscriptOpenRejectsSwappedFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test-transcript.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		t.Fatalf("expected a regular file before swap: %v", err)
	}
	if err := os.Rename(path, path+".retired"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		file, openErr := openClaudePermissionTranscript(path)
		if openErr != nil {
			done <- openErr
			return
		}
		defer func() { _ = file.Close() }()
		info, statErr := file.Stat()
		if statErr != nil {
			done <- statErr
			return
		}
		if info.Mode().IsRegular() || os.SameFile(before, info) {
			done <- os.ErrInvalid
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("swapped FIFO was not safely rejected:", err)
		}
	case <-time.After(2 * time.Second):
		// An os.Open regression would block here until the FIFO gets a
		// writer. Unblock the test goroutine before reporting failure.
		writer, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			_ = unix.Close(writer)
		}
		t.Fatal("opening swapped FIFO blocked the permission notifier")
	}
}

func TestClaudePermissionTranscriptOpenDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "regular.jsonl")
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(regular, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regular, path); err != nil {
		t.Fatal(err)
	}
	if file, err := openClaudePermissionTranscript(path); err == nil {
		_ = file.Close()
		t.Fatal("opener followed a swapped symlink")
	}
}
