//go:build linux

package setup

import "golang.org/x/sys/unix"

func renameExclusive(fd int, from, to string) error {
	return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE)
}
