package hooks

import (
	"fmt"

	"github.com/777genius/agent-notifications/internal/analyzer"
)

// classifyQoderHook is payload-only. Qoder supplies no owned team authority or
// transcript contract; similar Claude hook names do not grant either capability.
func (h *Handler) classifyQoderHook(ev Event, keys eventKeys) (*hookClassification, error) {
	switch p := ev.Payload.(type) {
	case PreToolUsePayload:
		return h.classifyInteractiveTool(ev, p, keys.stateKey), nil
	case NotificationPayload:
		return &hookClassification{status: analyzer.StatusQuestion}, nil
	case StopPayload:
		if p.Continuation {
			return nil, nil
		}
		return &hookClassification{status: analyzer.StatusTaskComplete, cleanup: true}, nil
	case StopFailurePayload:
		status := analyzer.StatusAgentError
		if p.RateLimited {
			status = analyzer.StatusAPIErrorOverloaded
		}
		return &hookClassification{status: status, cleanup: true}, nil
	case SubagentStopPayload:
		if h.cfg.ShouldSuppressForSubagents() || !h.cfg.Notifications.NotifyOnSubagentStop || p.Stop.Continuation {
			return nil, nil
		}
		return &hookClassification{status: analyzer.StatusTaskComplete, cleanup: true}, nil
	case PermissionRequestPayload:
		return &hookClassification{status: analyzer.StatusPermissionRequest}, nil
	case TeammateIdlePayload:
		// Keep the installed hook and decoding contract. No Qoder-owned team
		// source is available, so there is no team state to mutate or claim.
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown hook event: %s", ev.Kind())
	}
}
