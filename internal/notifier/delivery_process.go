package notifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// The webhook destination belongs to event HTTP delivery, never native children.
// Preserve every other inherited value, including OS/provider configuration.
func nativeNotificationEnvironment() []string {
	env := make([]string, 0)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == config.OpenCodeWebhookURLEnv || runtime.GOOS == "windows" && strings.EqualFold(key, config.OpenCodeWebhookURLEnv) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

// ManagedNativeProcess executes only the paths returned by a verified lease.
// It never uses PATH lookup, bundle-ID lookup, or the legacy notifier finder.
type ManagedNativeProcess struct{}
type nativeOutput struct{ data []byte }

func (b *nativeOutput) Write(p []byte) (int, error) {
	if len(p) > nativeprotocol.MaxEnvelopeBytes-len(b.data) {
		return 0, errors.New("native output exceeds limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func (ManagedNativeProcess) Probe(ctx context.Context, executable string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("unsupported platform")
	}
	var output nativeOutput
	cmd := nativeProbeCommand(ctx, executable)
	cmd.Stdout = &output
	err := cmd.Run() // CommandContext kills/reaps only this owned direct child.
	return output.data, err
}
func (ManagedNativeProcess) Launch(ctx context.Context, bundle, request, receipt string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, errors.New("unsupported platform")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	cmd := nativeLaunchCommand(ctx, bundle, request, receipt)
	if err := cmd.Start(); err != nil {
		return false, err
	}
	// A nonzero exit can follow possible LaunchServices handoff. No retry and no
	// claim that killing/reaping open stopped a native process launched by the OS.
	return true, cmd.Wait()
}

// Command construction has no effects and is tested without executing an app.
func nativeProbeCommand(ctx context.Context, executable string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, "--capabilities-json")
	cmd.Env = nativeNotificationEnvironment()
	cmd.Dir = "/"
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}
func nativeLaunchCommand(ctx context.Context, bundle, request, receipt string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-a", bundle, "--args", "--send-json", "--request-file", request, "--receipt-file", receipt, "-launchedViaLaunchServices")
	cmd.Env = nativeNotificationEnvironment()
	cmd.Dir = "/"
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}

func (ManagedNativeProcess) ProbePermission(ctx context.Context, executable, correlation, nonce string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("unsupported platform")
	}
	var output nativeOutput
	cmd := nativePermissionCommand(ctx, executable, correlation, nonce)
	cmd.Stdout = &output
	err := cmd.Run() // kills/reaps only this direct probe, never a callback owner
	return output.data, err
}
func nativePermissionCommand(ctx context.Context, executable, correlation, nonce string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, "--capabilities-json", "--permission-status", "--correlation-id", correlation, "--nonce", nonce)
	cmd.Env = nativeNotificationEnvironment()
	cmd.Dir = "/"
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}
