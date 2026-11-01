//go:build !windows

package commandrun

import (
	"errors"
	"os/exec"
	"syscall"
)

type processTree struct{ cmd *exec.Cmd }

func prepareTree(cmd *exec.Cmd) (*processTree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return &processTree{cmd: cmd}, nil
}
func (*processTree) attach() error { return nil }
func (t *processTree) kill() error {
	if t.cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func (t *processTree) close() { _ = t.kill() }
