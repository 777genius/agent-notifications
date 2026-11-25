//go:build linux || darwin

package hooks

import (
	"os"

	"golang.org/x/sys/unix"
)

// A transcript can be replaced after Lstat. O_NONBLOCK prevents a swapped FIFO
// from stalling the permission notification; O_NOFOLLOW rejects a swapped link.
// The caller also verifies SameFile, regular mode, and snapshot size.
func openClaudePermissionTranscript(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
