//go:build linux

package config

import "golang.org/x/sys/unix"

func publishNewAt(fd int, temp, name string) error {
	return unix.Renameat2(fd, temp, fd, name, unix.RENAME_NOREPLACE)
}
