package installruntime

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// lsof +D selects by filesystem object (including executable/text mappings and
// open resources), so rename of the old bundle does not hide a running reader.
// Permission warnings, partial scans, timeouts and output overflow are refusal.
// This is a bounded observation under the component lock, not a process killer.
func verifyNativeDrain(ctx context.Context, predecessor string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-Fpf", "+D", predecessor)
	cmd.WaitDelay = 100 * time.Millisecond
	out, diagnostic := &limitedOutput{}, &limitedOutput{}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	err := cmd.Run()
	if out.overflow || diagnostic.overflow {
		return fmt.Errorf("native resource scan output truncated")
	}
	return drainCommandResult(ctx.Err(), err, out.data, diagnostic.data)
}
