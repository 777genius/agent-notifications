//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"io"
	"os"
	"syscall"
	"time"
)

// Inherited stdin is a blocking os.NewFile descriptor. Closing that wrapper
// cannot interrupt a syscall read on Unix. Own a nonblocking duplicate enrolled
// in Go's poller, and refuse descriptors that cannot support a pipe deadline.
func prepareLocalInput(input io.ReadCloser) (io.ReadCloser, bool) {
	f, ok := input.(*os.File)
	if !ok {
		return input, true
	}
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, false
	}
	syscall.CloseOnExec(fd)
	if err = syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, false
	}
	owned := os.NewFile(uintptr(fd), "Local stdin")
	if owned == nil {
		_ = syscall.Close(fd)
		return nil, false
	}
	if err = owned.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		_ = owned.Close()
		return nil, false
	}
	return owned, true
}
