//go:build darwin

package setup

import "golang.org/x/sys/unix"

func renameExclusive(fd int, from, to string) error {
	return unix.RenameatxNp(fd, from, fd, to, unix.RENAME_EXCL)
}
