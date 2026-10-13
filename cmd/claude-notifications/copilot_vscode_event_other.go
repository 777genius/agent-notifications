//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package main

import "io"

func prepareLocalInput(io.ReadCloser) (io.ReadCloser, bool) { return nil, false }
