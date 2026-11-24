package hooks

import (
	"fmt"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/benchmark"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/summary"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// hookClassification is the current handler's classification result. Native
// source payloads and their public Event contract remain unchanged. Cleanup is
// deferred by HandleHook, after content admission and effects have completed.
type hookClassification struct {
	status   analyzer.Status
	messages []jsonl.Message
	insight  *TurnInsight
	cleanup  bool
}

func (h *Handler) classifyHook(ev Event, keys eventKeys, hookEvent string, bench *benchmark.Context) (*hookClassification, error) {
	// Claude intentionally leaves unknown native events without a payload.
	// Keep the original argv-based error after normal pre-classification gates.
	if ev.Payload == nil {
		return nil, fmt.Errorf("unknown hook event: %s", hookEvent)
	}
	switch ev.Product {
	case ProductClaude:
		return h.classifyClaudeHook(ev, keys, bench)
	case ProductCodex:
		return h.classifyCodexHook(ev)
	case ProductQoder:
		return h.classifyQoderHook(ev, keys)
	default:
		return nil, fmt.Errorf("unsupported product: %q", ev.Product)
	}
}

// Claude and Qoder share these interactive tool names, but never state identity.
func (h *Handler) classifyInteractiveTool(ev Event, p PreToolUsePayload, stateKey string) *hookClassification {
	result := &hookClassification{status: h.handlePreToolUse(ev, p, stateKey)}
	if p.ToolName == "AskUserQuestion" {
		in := questionInsight(p.ToolInput, false)
		if in.Body == "" {
			in.Body = summary.GenerateSimple(result.status, h.cfg)
		}
		result.insight = &in
	}
	return result
}

// handlePreToolUse handles PreToolUse hook
func (h *Handler) handlePreToolUse(ev Event, p PreToolUsePayload, stateKey string) analyzer.Status {
	logging.Debug("PreToolUse: tool_name='%s'", p.ToolName)

	status := analyzer.GetStatusForPreToolUse(p.ToolName)

	// Write session state BEFORE returning (prevents race with Notification hook)
	// This matches bash version behavior: state is written BEFORE notification is sent
	if status == analyzer.StatusPlanReady || status == analyzer.StatusQuestion {
		if err := h.stateMgr.UpdateInteractiveTool(stateKey, p.ToolName, ev.Session.CWD); err != nil {
			logging.Warn("Failed to update interactive tool state: %v", err)
		} else {
			logging.Debug("PreToolUse: session state written (tool=%s)", p.ToolName)
		}
	}

	return status
}
