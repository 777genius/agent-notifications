//go:build !windows

package main

import (
	"errors"
	"io"
	"os"
)

func readBootstrapIntentDocument(_ string, root *os.Root, leaf string) ([]byte, error) {
	info, err := root.Lstat(leaf)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBootstrapIntent || info.Size() == 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("invalid private intent leaf")
	}
	f, err := root.Open(leaf)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("intent leaf changed")
	}
	return io.ReadAll(io.LimitReader(f, maxBootstrapIntent+1))
}
