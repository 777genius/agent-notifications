//go:build linux

package notifier

import (
	"context"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/notification"
)

// CaptureLocalTerminal is source-agnostic and uses only the invoking local
// process and selected config/installer binding, never an agent event payload.
func CaptureLocalTerminal(ctx context.Context, cfg *config.Config, agent config.AgentID, root, installationID string, generation uint64) *notification.TerminalTarget {
	target, ok := daemon.CaptureStrictTerminal(ctx, cfg, agent, root, installationID, generation)
	if !ok {
		return nil
	}
	return target
}
