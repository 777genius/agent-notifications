//go:build linux || darwin

package installruntime

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func configOnlyDirectory(root string) (*os.File, error) {
	directory, _, err := anchoredParent(filepath.Join(root, "config.json"), false)
	return directory, err
}

func configOnlyEntryInfo(directory *os.File, name string) (os.FileInfo, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() { _ = file.Close() }()
	// The same UID, single-link, private regular-inode custody as an existing
	// lock, but opened read-only without flock, creation, or metadata adoption.
	if err = validateLockFile(file, false); err != nil {
		return nil, err
	}
	return file.Stat()
}
