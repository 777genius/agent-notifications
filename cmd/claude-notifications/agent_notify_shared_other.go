//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package main

import "os/exec"

func configureSharedDaemonProcess(cmd *exec.Cmd) {}
