package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/777genius/agent-notifications/internal/qodersource"
)

// QoderSource decodes the Qoder plugin hook wire (Claude-style JSON on stdin).
// It deliberately ignores transcript_path: no Qoder transcript schema is
// assumed by the notification pipeline. Every route is observation-only.
type QoderSource struct{}

// QoderHookEvents is the notification-suitable hook set wired end to end.
// Mirrors the Claude adapter set (PreToolUse/Notification/Stop/SubagentStop/
// TeammateIdle) plus PermissionRequest, which Qoder supports natively.
var QoderHookEvents = []string{
	"Stop",
	"Notification",
	"PermissionRequest",
	"SubagentStop",
	"TeammateIdle",
	"PreToolUse",
}

// IsQoderHookEvent reports whether the event belongs to the supported set.
func IsQoderHookEvent(event string) bool {
	for _, e := range QoderHookEvents {
		if e == event {
			return true
		}
	}
	return false
}

// Decode maps one Qoder hook invocation to an Event. Stop keeps the Go SDK's
// Qoder transport; the remaining events use the documented Claude-style wire
// directly, with the payload's hook_event_name required to match exactly.
func (QoderSource) Decode(ctx context.Context, event string, input io.Reader) (Event, error) {
	raw, err := io.ReadAll(io.LimitReader(input, qodersource.MaxPayloadBytes+1))
	if err != nil {
		return Event{}, err
	}
	if len(raw) > qodersource.MaxPayloadBytes {
		return Event{}, fmt.Errorf("qoder payload exceeds limit")
	}
	if event == "Stop" {
		d, err := qodersource.Decode(ctx, event, raw)
		if err != nil {
			return Event{}, err
		}
		ev := Event{Product: ProductQoder, PayloadEventName: "Stop", Session: SessionContext{SessionID: d.SessionID, CWD: d.CWD}, Payload: StopPayload{AssistantMessage: d.LastAssistantMessage, Continuation: d.StopHookActive}, Raw: append(json.RawMessage(nil), raw...)}
		return ev, ValidateEvent(ev)
	}
	if !IsQoderHookEvent(event) {
		return Event{}, fmt.Errorf("unsupported qoder event %q", event)
	}
	var wire HookData
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Event{}, fmt.Errorf("qoder hook payload is invalid")
	}
	if wire.HookEventName != event {
		return Event{}, fmt.Errorf("qoder hook event mismatch: got %q", wire.HookEventName)
	}
	ev := Event{
		Product:          ProductQoder,
		PayloadEventName: event,
		Session:          SessionContext{SessionID: wire.SessionID, CWD: wire.CWD},
		Raw:              append(json.RawMessage(nil), raw...),
	}
	switch event {
	case "Notification":
		ev.Payload = NotificationPayload{}
	case "PermissionRequest":
		ev.Payload = PermissionRequestPayload{ToolName: wire.ToolName, ToolInput: append(json.RawMessage(nil), wire.ToolInput...)}
	case "SubagentStop":
		ev.Payload = SubagentStopPayload{Stop: StopPayload{AssistantMessage: wire.LastAssistantMessage}}
	case "TeammateIdle":
		ev.Payload = TeammateIdlePayload{TeamName: wire.TeamName, TeammateName: wire.TeammateName}
	case "PreToolUse":
		ev.Payload = PreToolUsePayload{ToolName: wire.ToolName, ToolInput: append(json.RawMessage(nil), wire.ToolInput...)}
	}
	return ev, ValidateEvent(ev)
}
