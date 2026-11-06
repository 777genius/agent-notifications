package hooks

import (
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/internal/summary"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// generateMessage generates a notification body and action summary.
// If messages are provided (from handleStopEvent), uses them directly to avoid re-reading the transcript.
func (h *Handler) generateMessage(ev Event, status analyzer.Status, messages []jsonl.Message, insight *TurnInsight) (body, actions string) {
	// A permission prompt must describe the actual host request, not an
	// unrelated transcript summary. Keep the body bounded and one-line.
	if ev.Product == ProductClaude && status == analyzer.StatusPermissionRequest {
		if p, ok := ev.Payload.(NotificationPayload); ok {
			if message := strings.Join(strings.Fields(p.Message), " "); message != "" {
				return truncateRunes(message, 180), ""
			}
		}
		return summary.GenerateSimple(status, h.cfg), ""
	}
	// Codex bodies come from payload fields, never from the Claude-format
	// transcript parser: the Codex rollout JSONL is a different schema.
	if ev.Product == ProductCodex || ev.Product == ProductQoder {
		if insight != nil && insight.Body != "" {
			return insight.Body, ""
		}
		return h.generateCodexMessage(ev, status), ""
	}
	if insight != nil && insight.Body != "" {
		if len(messages) > 0 {
			_, actions = summary.GenerateFromMessagesStructured(messages, status, h.cfg)
			if !claudeTranscriptHasClosingText(messages) {
				// The last assistant timestamp can still be the pre-tool call;
				// showing its duration as the completed turn would be misleading.
				if duration := strings.Index(actions, "⏱"); duration >= 0 {
					actions = strings.TrimSpace(actions[:duration])
				}
			}
		}
		return insight.Body, actions
	}

	// Use pre-parsed messages if available (eliminates ~234ms double I/O)
	if len(messages) > 0 {
		body, actions = summary.GenerateFromMessagesStructured(messages, status, h.cfg)
	} else if ev.Session.TranscriptPath != "" && platform.FileExists(ev.Session.TranscriptPath) {
		// Fallback: read transcript from file (for non-Stop hooks)
		if parsed, err := jsonl.ParseFile(ev.Session.TranscriptPath); err == nil {
			body, actions = summary.GenerateFromMessagesStructured(parsed, status, h.cfg)
		}
	}

	if body == "" {
		body = summary.GenerateSimple(status, h.cfg)
	}
	return body, actions
}

// generateCodexMessage projects a Codex payload into a notification body.
// ToolInput is intentionally never rendered: only the tool identity is safe
// to show without redaction.
func (h *Handler) generateCodexMessage(ev Event, status analyzer.Status) string {
	switch p := ev.Payload.(type) {
	case StopPayload:
		if text := strings.TrimSpace(p.AssistantMessage); text != "" {
			return truncateRunes(summary.CleanMarkdown(text), 150)
		}
	case SubagentStopPayload:
		if text := strings.TrimSpace(p.Stop.AssistantMessage); text != "" {
			return truncateRunes(summary.CleanMarkdown(text), 150)
		}
	case PermissionRequestPayload:
		if p.ToolName != "" {
			product := "Codex"
			if ev.Product == ProductQoder {
				product = "Qoder"
			}
			return fmt.Sprintf("%s requests permission: %s", product, p.ToolName)
		}
	}
	return summary.GenerateSimple(status, h.cfg)
}

// truncateRunes shortens s to at most max runes, appending an ellipsis.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
