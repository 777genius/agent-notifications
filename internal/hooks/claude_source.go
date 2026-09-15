package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/777genius/agent-notifications/internal/logging"
)

// ClaudeSource decodes the legacy Claude Code hook wire format into the
// product event contract. It preserves the historical decoder semantics:
// UTF-8 BOM skipping, decoding exactly the first JSON value from the reader
// without waiting for EOF, and ignoring trailing data.
type ClaudeSource struct{}

// Decode maps one Claude hook invocation to an Event. An unrecognized hook
// event name yields a nil payload (not an error) so the policy router can
// report it exactly where the legacy switch did.
func (ClaudeSource) Decode(_ context.Context, hookEvent string, input io.Reader) (Event, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(skipUTF8BOM(input)).Decode(&raw); err != nil {
		return Event{}, fmt.Errorf("failed to parse hook data: %w", err)
	}

	var wire HookData
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Event{}, fmt.Errorf("failed to parse hook data: %w", err)
	}

	if wire.SessionID == "" {
		wire.SessionID = "unknown"
		logging.Warn("Session ID is empty, using 'unknown'")
	}

	ev := Event{
		Product:          ProductClaude,
		PayloadEventName: wire.HookEventName,
		Session: SessionContext{
			SessionID:      wire.SessionID,
			CWD:            wire.CWD,
			TranscriptPath: wire.TranscriptPath,
		},
		Raw: append(json.RawMessage(nil), raw...),
	}

	switch hookEvent {
	case "PreToolUse":
		ev.Payload = PreToolUsePayload{ToolName: wire.ToolName}
	case "Notification":
		ev.Payload = NotificationPayload{}
	case "Stop":
		ev.Payload = StopPayload{}
	case "SubagentStop":
		ev.Payload = SubagentStopPayload{Stop: StopPayload{}}
	case "TeammateIdle":
		ev.Payload = TeammateIdlePayload{
			TeamName:     wire.TeamName,
			TeammateName: wire.TeammateName,
		}
	default:
		// Unknown event: leave Payload nil so the router reports the legacy
		// "unknown hook event" error in its original position.
		return ev, nil
	}

	if err := ValidateEvent(ev); err != nil {
		return Event{}, err
	}
	return ev, nil
}
