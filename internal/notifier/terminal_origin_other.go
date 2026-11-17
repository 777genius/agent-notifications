//go:build !linux

package notifier

import (
	"context"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
)

// Unqualified platform/terminal pairs keep the existing informational route.
func CaptureLocalTerminal(context.Context, *config.Config, config.AgentID, string, string, uint64) *notification.TerminalTarget {
	return nil
}
