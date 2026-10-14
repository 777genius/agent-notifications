//go:build !windows

package setup

import (
	"context"
	"encoding/json"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
)

func stageWindowsRoute(context.Context, Options, Request, map[string]json.RawMessage) (*installruntime.WindowsChange, error) {
	return nil, nil
}
func verifyWindowsRoute(context.Context, notification.WindowsBinding) error {
	return fail("windows_callback_unavailable", installruntime.ErrPolicyRecovery)
}
