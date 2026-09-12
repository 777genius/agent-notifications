//go:build linux || darwin

package main

import (
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func agentNotifyFile(source *os.File) (*agentNotifyStream, error) {
	// Fd() can itself clear O_NONBLOCK on a Go-managed source. Control borrows
	// the descriptor without changing its flags or transferring ownership.
	raw, err := source.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var setupErr error
	err = raw.Control(func(sourceFD uintptr) {
		fd, setupErr = unix.FcntlInt(sourceFD, unix.F_DUPFD_CLOEXEC, 0)
	})
	if err != nil {
		return nil, err
	}
	if setupErr != nil {
		return nil, setupErr
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, err
	}
	kind := stat.Mode & unix.S_IFMT
	local := kind == unix.S_IFREG
	if kind == unix.S_IFCHR {
		var null unix.Stat_t
		local = unix.Stat("/dev/null", &null) == nil && stat.Rdev == null.Rdev && null.Mode&unix.S_IFMT == unix.S_IFCHR
	}
	// Only pipes, sockets and deadline-capable terminals take the poller path.
	if !local && kind != unix.S_IFIFO && kind != unix.S_IFSOCK && kind != unix.S_IFCHR {
		unix.Close(fd)
		return nil, errors.New("stdio_unavailable")
	}
	release := func() {}
	if !local {
		flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if e != nil {
			unix.Close(fd)
			return nil, e
		}
		if flags&unix.O_NONBLOCK == 0 {
			// Keep an owned reference for restoration after Close has joined OS I/O.
			anchor, e := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
			if e != nil {
				unix.Close(fd)
				return nil, e
			}
			var once sync.Once
			release = func() {
				once.Do(func() {
					// Preserve unrelated status flags changed during the invocation.
					current, e := unix.FcntlInt(uintptr(anchor), unix.F_GETFL, 0)
					if e == nil {
						_, _ = unix.FcntlInt(uintptr(anchor), unix.F_SETFL, current&^unix.O_NONBLOCK)
					}
					_ = unix.Close(anchor)
				})
			}
			if e = unix.SetNonblock(fd, true); e != nil {
				unix.Close(fd)
				release()
				return nil, e
			}
		}
	}
	file := os.NewFile(uintptr(fd), "explicit-notify-stdio")
	if file == nil {
		unix.Close(fd)
		release()
		return nil, errors.New("stdio_unavailable")
	}
	if !local {
		if err = file.SetDeadline(time.Time{}); err != nil {
			_ = file.Close()
			release()
			return nil, errors.New("stdio_requires_pollable_pipe_or_terminal")
		}
	}
	return &agentNotifyStream{file: file, pollable: !local, release: release}, nil
}
