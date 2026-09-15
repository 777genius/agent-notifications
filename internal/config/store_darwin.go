//go:build darwin

package config

import "golang.org/x/sys/unix"

func publishNewAt(fd int, temp, name string) error {
	return unix.RenameatxNp(fd, temp, fd, name, unix.RENAME_EXCL)
}
