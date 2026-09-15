package main

import (
	"os/exec"
	"strings"
	"syscall"
)

// cmd.exe does not use CommandLineToArgvW quoting. Preserve the generated
// command verbatim with the outer quotes used by Codex rust-v0.153.4's
// command_runner.rs, instead of adding Go's backslash escapes.
func configureE2EShell(c *exec.Cmd, binary string, args []string) {
	if strings.EqualFold(binary, "cmd.exe") && len(args) == 4 {
		c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /c "` + args[3] + `"`}
		c.Args = nil
	}
}
