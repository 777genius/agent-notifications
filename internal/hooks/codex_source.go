package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/777genius/agent-notifications/internal/codexsource"
)

// CodexDecodeFunc is the narrow DTO-returning seam between this package and
// internal/codexsource; this package never imports the SDK itself.
type CodexDecodeFunc func(ctx context.Context, publicEvent string, payload []byte) (codexsource.Decoded, error)

// CodexSource decodes Codex hook payloads through the SDK adapter into the
// product event contract. Wire names never leak past this mapping.
type CodexSource struct {
	DecodeFn CodexDecodeFunc
}

// NewCodexSource wires the source to the real SDK adapter.
func NewCodexSource() CodexSource {
	return CodexSource{DecodeFn: codexsource.Decode}
}

// Decode reads the bounded payload and maps it to an Event. The reader is
// expected to hold the pre-read stdin bytes; the size guard here is a second
// line of defense behind the composition root's bounded read.
func (s CodexSource) Decode(ctx context.Context, publicEvent string, input io.Reader) (Event, error) {
	if s.DecodeFn == nil {
		return Event{}, fmt.Errorf("codex source is not wired to a decoder")
	}

	raw, err := io.ReadAll(io.LimitReader(input, codexsource.MaxPayloadBytes+1))
	if err != nil {
		return Event{}, fmt.Errorf("failed to read codex payload: %w", err)
	}
	if len(raw) > codexsource.MaxPayloadBytes {
		return Event{}, fmt.Errorf("codex payload exceeds %d bytes", codexsource.MaxPayloadBytes)
	}

	decoded, err := s.DecodeFn(ctx, publicEvent, raw)
	if err != nil {
		return Event{}, err
	}

	ev := Event{
		Product: ProductCodex,
		Raw:     append(json.RawMessage(nil), raw...),
	}

	switch {
	case decoded.Stop != nil:
		d := decoded.Stop
		ev.PayloadEventName = d.HookEventName
		ev.Session = codexSession(d.SessionID, d.TurnID, d.CWD, d.TranscriptPath, d.Model, d.PermissionMode)
		ev.Payload = StopPayload{
			AssistantMessage: d.LastAssistantMessage,
			Continuation:     d.StopHookActive,
		}
	case decoded.SubagentStop != nil:
		d := decoded.SubagentStop
		ev.PayloadEventName = d.Stop.HookEventName
		ev.Session = codexSession(d.Stop.SessionID, d.Stop.TurnID, d.Stop.CWD, d.Stop.TranscriptPath, d.Stop.Model, d.Stop.PermissionMode)
		ev.Payload = SubagentStopPayload{
			Stop: StopPayload{
				AssistantMessage: d.Stop.LastAssistantMessage,
				Continuation:     d.Stop.StopHookActive,
			},
			Agent: &AgentContext{
				ID:             d.AgentID,
				Type:           d.AgentType,
				TranscriptPath: d.AgentTranscriptPath,
			},
		}
	case decoded.PreToolUse != nil:
		d := decoded.PreToolUse
		ev.PayloadEventName = d.HookEventName
		ev.Session = codexSession(d.SessionID, d.TurnID, d.CWD, d.TranscriptPath, d.Model, d.PermissionMode)
		ev.Payload = PreToolUsePayload{
			ToolName:  d.ToolName,
			ToolUseID: d.ToolUseID,
			ToolInput: append(json.RawMessage(nil), d.ToolInput...),
		}
	case decoded.PermissionRequest != nil:
		d := decoded.PermissionRequest
		ev.PayloadEventName = d.HookEventName
		ev.Session = codexSession(d.SessionID, d.TurnID, d.CWD, d.TranscriptPath, d.Model, d.PermissionMode)
		var agent *AgentContext
		if d.AgentID != "" || d.AgentType != "" {
			agent = &AgentContext{ID: d.AgentID, Type: d.AgentType}
		}
		ev.Payload = PermissionRequestPayload{
			ToolName:  d.ToolName,
			ToolInput: append(json.RawMessage(nil), d.ToolInput...),
			Agent:     agent,
		}
	default:
		return Event{}, fmt.Errorf("codex decode for %q produced no payload", publicEvent)
	}

	if err := ValidateEvent(ev); err != nil {
		return Event{}, err
	}
	return ev, nil
}

func codexSession(sessionID, turnID, cwd, transcriptPath, model, permissionMode string) SessionContext {
	return SessionContext{
		SessionID:      sessionID,
		TurnID:         turnID,
		CWD:            cwd,
		TranscriptPath: transcriptPath,
		Model:          model,
		PermissionMode: permissionMode,
	}
}
