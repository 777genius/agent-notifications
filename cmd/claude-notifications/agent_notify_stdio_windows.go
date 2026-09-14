//go:build windows

package main

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func agentNotifyFile(source *os.File) (*agentNotifyStream, error) {
	raw, err := source.SyscallConn()
	if err != nil {
		return nil, err
	}
	var dup windows.Handle
	var setupErr error
	err = raw.Control(func(sourceFD uintptr) {
		setupErr = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(sourceFD), windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS)
	})
	if err != nil {
		return nil, err
	}
	if setupErr != nil {
		return nil, setupErr
	}
	kind, err := windows.GetFileType(dup)
	if err != nil {
		_ = windows.CloseHandle(dup)
		return nil, err
	}
	switch kind {
	case windows.FILE_TYPE_DISK:
	case windows.FILE_TYPE_PIPE, windows.FILE_TYPE_CHAR:
	default:
		_ = windows.CloseHandle(dup)
		return nil, errors.New("stdio_unavailable")
	}
	file := os.NewFile(uintptr(dup), "explicit-notify-stdio")
	if file == nil {
		_ = windows.CloseHandle(dup)
		return nil, errors.New("stdio_unavailable")
	}
	pollable := kind == windows.FILE_TYPE_PIPE
	if kind == windows.FILE_TYPE_CHAR {
		pollable = file.SetDeadline(time.Time{}) == nil
	} else if pollable {
		// Anonymous pipes on Windows often reject SetDeadline; they remain the
		// pollable stdio transport. CHAR consoles still require a working deadline.
		_ = file.SetDeadline(time.Time{})
	}
	return &agentNotifyStream{file: file, pollable: pollable, release: func() {}}, nil
}
