package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Product identifies the host product that emitted a hook event.
type Product string

const (
	ProductClaude Product = "claude"
	ProductCodex  Product = "codex"
)

// EventKind is the product-level event classification derived from the
// sealed payload type, never stored separately.
type EventKind string

const (
	EventUnknown           EventKind = ""
	EventPreToolUse        EventKind = "pre_tool_use"
	EventNotification      EventKind = "notification"
	EventStop              EventKind = "stop"
	EventSubagentStop      EventKind = "subagent_stop"
	EventPermissionRequest EventKind = "permission_request"
	EventTeammateIdle      EventKind = "teammate_idle"
)

// SessionContext carries the host-neutral session identity shared by all events.
type SessionContext struct {
	SessionID      string
	TurnID         string
	CWD            string
	TranscriptPath string
	Model          string
	PermissionMode string
}

// AgentContext identifies a subagent on hosts that report one.
type AgentContext struct {
	ID              string
	Type            string
	TranscriptPath  string
	ParentSessionID string
	ParentToolUseID string
}

// Event is the product-owned envelope. Host DTO types never cross this boundary.
type Event struct {
	Product          Product
	PayloadEventName string // diagnostic only; never selects the route
	Session          SessionContext
	Payload          EventPayload
	Raw              json.RawMessage // sensitive, retained for forward compatibility
}

// Kind derives the event kind from the sealed payload type.
func (e Event) Kind() EventKind {
	if e.Payload == nil {
		return EventUnknown
	}
	return e.Payload.eventKind()
}

// EventPayload is sealed inside this package by the unexported method.
type EventPayload interface {
	eventKind() EventKind
}

// StopPayload is the main-agent turn completion.
type StopPayload struct {
	AssistantMessage string
	Continuation     bool
}

// SubagentStopPayload is a subagent turn completion.
type SubagentStopPayload struct {
	Stop  StopPayload
	Agent *AgentContext // required for Codex, absent for legacy Claude payloads
}

// PermissionRequestPayload is a host request for user approval of a tool call.
type PermissionRequestPayload struct {
	ToolName  string
	ToolInput json.RawMessage
	Agent     *AgentContext // optional on the Codex wire
}

// PreToolUsePayload is an interactive-tool event (Claude PreToolUse, or
// Codex PreToolUse for notification-relevant tools). ToolUseID is set on
// the Codex wire and empty for legacy Claude payloads.
type PreToolUsePayload struct {
	ToolName  string
	ToolUseID string
	ToolInput json.RawMessage
}

// NotificationPayload is the Claude permission-prompt Notification event.
type NotificationPayload struct{}

// TeammateIdlePayload is the Claude team event.
type TeammateIdlePayload struct {
	TeamName     string
	TeammateName string
}

func (StopPayload) eventKind() EventKind              { return EventStop }
func (SubagentStopPayload) eventKind() EventKind      { return EventSubagentStop }
func (PermissionRequestPayload) eventKind() EventKind { return EventPermissionRequest }
func (PreToolUsePayload) eventKind() EventKind        { return EventPreToolUse }
func (NotificationPayload) eventKind() EventKind      { return EventNotification }
func (TeammateIdlePayload) eventKind() EventKind      { return EventTeammateIdle }

// EventSource decodes one host invocation into the product event contract.
// The string argument is the validated public event name from argv.
type EventSource interface {
	Decode(context.Context, string, io.Reader) (Event, error)
}

// ValidateEvent checks the invariants every source must satisfy immediately
// after mapping, before policy or side effects run.
func ValidateEvent(e Event) error {
	switch e.Product {
	case ProductClaude, ProductCodex:
	default:
		return fmt.Errorf("unknown event product %q", e.Product)
	}
	if e.Payload == nil {
		return fmt.Errorf("event has no payload (raw event name %q)", e.PayloadEventName)
	}
	if e.Session.SessionID == "" {
		return fmt.Errorf("event is missing a session id")
	}
	if p, ok := e.Payload.(SubagentStopPayload); ok {
		if e.Product == ProductCodex && (p.Agent == nil || p.Agent.ID == "") {
			return fmt.Errorf("codex subagent stop event is missing agent identity")
		}
	}
	return nil
}
