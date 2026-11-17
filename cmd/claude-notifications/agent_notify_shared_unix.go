//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"os/exec"
	"syscall"
)

func configureSharedDaemonProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
