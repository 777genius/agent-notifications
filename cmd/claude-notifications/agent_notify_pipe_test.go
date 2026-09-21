//go:build linux || darwin

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

func agentNotifyFillPipe(w *os.File) error {
	fd := int(w.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return err
	}
	for {
		_, err := unix.Write(fd, make([]byte, 4096))
		if err == unix.EAGAIN {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
