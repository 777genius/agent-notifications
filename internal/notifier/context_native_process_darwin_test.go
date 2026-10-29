//go:build darwin && cgo

package notifier

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestContextNativeStopRequiresExactKernelBirthAndExecutable(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "20")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	process, err := readContextNativeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	wrong := process
	wrong.StartSeconds++
	if err := stopContextNativeProcess(wrong, "/bin/sleep"); err == nil {
		t.Fatal("stale birth identity accepted")
	}
	if err := stopContextNativeProcess(process, "/TEST/foreign-helper"); err == nil {
		t.Fatal("foreign executable accepted")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("identity mismatch killed foreign process", err)
	}
	if err := stopContextNativeProcess(process, "/bin/sleep"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("owned process not collected")
	}
}
