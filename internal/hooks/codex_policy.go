package hooks

import (
	"context"
	"fmt"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/logging"
)

// classifyCodexHook uses only admitted Codex payloads and the Codex enricher.
// It never reads a Claude transcript or accesses Claude team state.
func (h *Handler) classifyCodexHook(ev Event) (*hookClassification, error) {
	switch p := ev.Payload.(type) {
	case PreToolUsePayload:
		in := h.turnEnricher().EnrichPreToolUse(context.Background(), ev, p)
		if in.Status == analyzer.StatusUnknown {
			logging.Debug("Codex PreToolUse: tool %q is not notification-relevant, skipping", p.ToolName)
			return nil, nil
		}
		logging.Debug("Codex PreToolUse: tool=%s → status=%s", p.ToolName, in.Status)
		return &hookClassification{status: in.Status, insight: &in}, nil
	case StopPayload:
		if p.Continuation {
			logging.Debug("Codex Stop: continuation turn, suppressing")
			return nil, nil
		}
		in := h.turnEnricher().EnrichStop(context.Background(), ev, p)
		return &hookClassification{status: in.Status, insight: &in, cleanup: true}, nil
	case SubagentStopPayload:
		if h.cfg.ShouldSuppressForSubagents() || !h.cfg.Notifications.NotifyOnSubagentStop || p.Stop.Continuation {
			return nil, nil
		}
		in := h.turnEnricher().EnrichStop(context.Background(), ev, p.Stop)
		return &hookClassification{status: in.Status, insight: &in, cleanup: true}, nil
	case PermissionRequestPayload:
		return &hookClassification{status: analyzer.StatusPermissionRequest}, nil
	case NotificationPayload:
		return &hookClassification{status: analyzer.StatusQuestion}, nil
	case TeammateIdlePayload:
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown hook event: %s", ev.Kind())
	}
}
