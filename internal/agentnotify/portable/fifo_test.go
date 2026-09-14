//go:build linux || darwin

package portable

import "golang.org/x/sys/unix"

func makeFIFO(path string) error { return unix.Mkfifo(path, 0600) }
