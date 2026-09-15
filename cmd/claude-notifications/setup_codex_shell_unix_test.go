//go:build !windows

package main

import "os/exec"

func configureE2EShell(_ *exec.Cmd, _ string, _ []string) {}
