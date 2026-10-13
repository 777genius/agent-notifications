//go:build !windows

package installprogress

import (
	"errors"
	"os"
	"syscall"
)

func openEvents(path string) (*os.File, error) {
	// A raced replacement with a FIFO cannot block this display process;
	// symlinks cannot redirect it outside the shell-owned private event file.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func ownerAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func privateOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0077 == 0
}
