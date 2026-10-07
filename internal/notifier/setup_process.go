package notifier

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"time"
)

// ProbeSetup is used only after the installer has verified the managed artifact
// and its base capabilities. It never requests OS authorization.
func (ManagedNativeProcess) ProbeSetup(ctx context.Context, executable string) ([]byte, error) {
	return runSetupProcess(ctx, executable, []string{"--capabilities-json", "--setup"}, time.Second)
}

// RequestPermission is an explicit installer action, never a notify fallback.
// The caller must hold a setup lease and first validate setup capabilities.
// A timeout cannot retract an OS prompt already dispatched; never auto-retry.
func (ManagedNativeProcess) RequestPermission(ctx context.Context, executable, correlation, nonce string) ([]byte, error) {
	return runSetupProcess(ctx, executable, []string{"--request-permission-json", "--correlation-id", correlation, "--nonce", nonce}, 125*time.Second)
}

func runSetupProcess(ctx context.Context, executable string, args []string, budget time.Duration) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("unsupported platform")
	}
	if ctx == nil {
		return nil, errors.New("missing setup context")
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var output nativeOutput
	cmd := nativeSetupCommand(ctx, executable, args)
	cmd.Stdout = &output
	err := cmd.Run()
	return output.data, err
}
func nativeSetupCommand(ctx context.Context, executable string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = "/"
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}
