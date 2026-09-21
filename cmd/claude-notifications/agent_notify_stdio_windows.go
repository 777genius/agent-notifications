//go:build windows

package main

import (
	"errors"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

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
	var write func([]byte) (int, error)
	if kind == windows.FILE_TYPE_CHAR {
		pollable = file.SetDeadline(time.Time{}) == nil
	} else if pollable {
		// Handles inherited for stdio are commonly synchronous anonymous pipes,
		// for which os.File deadlines are unsupported. Bound a blocked response by
		// cancelling the write thread, with duplicate-handle close as fallback; the
		// caller-owned stdout remains untouched.
		write = boundedWindowsPipeWrite(file)
	}
	return &agentNotifyStream{file: file, pollable: pollable, write: write, release: func() {}}, nil
}

func boundedWindowsPipeWrite(file *os.File) func([]byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	return func(p []byte) (int, error) {
		done := make(chan result, 1)
		threadReady := make(chan windows.Handle, 1)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
			if err != nil {
				threadReady <- 0
				done <- result{err: err}
				return
			}
			threadReady <- thread
			n, err := file.Write(p)
			done <- result{n: n, err: err}
		}()
		thread := <-threadReady
		if thread == 0 {
			out := <-done
			return out.n, out.err
		}
		defer windows.CloseHandle(thread)
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case out := <-done:
			return out.n, out.err
		case <-timer.C:
			cancelled, _, cancelErr := cancelSynchronousIO.Call(uintptr(thread))
			if cancelled == 0 {
				_ = file.Close()
			}
			out := <-done
			if out.err != nil {
				if cancelled == 0 {
					return out.n, errors.Join(os.ErrDeadlineExceeded, cancelErr, out.err)
				}
				return out.n, errors.Join(os.ErrDeadlineExceeded, out.err)
			}
			return out.n, os.ErrDeadlineExceeded
		}
	}
}
