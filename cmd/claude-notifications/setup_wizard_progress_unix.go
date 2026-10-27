//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func validateBootstrapProgressLeaf(_ string, root *os.Root) error {
	info, err := root.Lstat("progress.events")
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxBootstrapIntent || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return errors.New("invalid private progress journal")
	}
	return nil
}
