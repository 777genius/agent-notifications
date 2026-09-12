//go:build !darwin && !linux

package setup

import (
	"context"
	"fmt"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func provision(_ context.Context, _ Options, s installruntime.PolicySnapshot, id installruntime.Identity, _ *Result) (installruntime.PolicySnapshot, installruntime.Identity, string, error) {
	return s, id, "", fail("unsupported_platform", fmt.Errorf("private setup state is unavailable"))
}

func checkPolicy(string) error { return fmt.Errorf("unsupported platform") }
func checkProvisioned(Options, installruntime.PolicySnapshot) error {
	return fmt.Errorf("unsupported platform")
}
