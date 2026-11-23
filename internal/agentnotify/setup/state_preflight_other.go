//go:build !darwin && !linux && !windows

package setup

import (
	"fmt"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func inspectInitializedForEnable(_ Options, _ installruntime.PolicySnapshot) error {
	return fail("unsupported_platform", fmt.Errorf("private setup state is unavailable"))
}
