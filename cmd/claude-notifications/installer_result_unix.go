//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func openInstallerResult(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
			err = errors.New("installer result must be private")
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
