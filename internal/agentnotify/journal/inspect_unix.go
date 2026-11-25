//go:build linux || darwin

package journal

import (
	"os"

	"golang.org/x/sys/unix"
)

// A journal writer atomically replaces journal.json. If the replacement
// happens between openat and fstat, the old open inode can have nlink=0 and
// the strict reader reports ErrRepair even though the new snapshot is valid.
// Retry only when the *directory entry identity changed during this read*.
// All normal privacy, hard-link, schema and canonical checks remain in s.read.
func (s *Store) readInspection(dir *os.File) (*disk, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		var before unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), "journal.json", &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, wrap(err)
		}
		d, err := s.read(dir)
		if err == nil {
			return d, nil
		}
		var after unix.Stat_t
		if statErr := unix.Fstatat(int(dir.Fd()), "journal.json", &after, unix.AT_SYMLINK_NOFOLLOW); statErr != nil {
			return nil, err
		}
		if before.Dev == after.Dev && before.Ino == after.Ino {
			return nil, err
		}
		last = err
	}
	return nil, last
}
