// Package codexsource is the only product package that imports
// plugin-kit-ai/sdk. It adapts one pre-read Codex hook payload into plain
// host DTOs; it never classifies, notifies, or touches product policy.
package codexsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/codex"
	"github.com/777genius/plugin-kit-ai/sdk/hostdetect"
)

// MaxPayloadBytes re-exports the SDK's single wire limit so the rest of the
// product never imports the SDK or duplicates the number.
const MaxPayloadBytes = pluginkitai.MaxPayloadBytes

// ValidateProductOverride validates an explicit --product override through
// the SDK host detector. An unknown override is an error, never an arbitrary
// platform; detection fails closed.
func ValidateProductOverride(override string) (string, error) {
	platform, err := hostdetect.Detect(hostdetect.DefaultRegistry(), override, nil, nil)
	if err != nil {
		return "", err
	}
	return string(platform), nil
}

// StopData mirrors the decoded Codex Stop payload without exposing SDK types.
type StopData struct {
	SessionID            string
	TurnID               string
	TranscriptPath       string
	CWD                  string
	HookEventName        string
	Model                string
	PermissionMode       string
	StopHookActive       bool
	LastAssistantMessage string
}

// SubagentStopData mirrors the decoded Codex SubagentStop payload.
type SubagentStopData struct {
	Stop                StopData
	AgentID             string
	AgentType           string
	AgentTranscriptPath string
}

// PreToolUseData mirrors the decoded Codex PreToolUse payload.
type PreToolUseData struct {
	SessionID      string
	TurnID         string
	TranscriptPath string
	CWD            string
	HookEventName  string
	Model          string
	PermissionMode string
	ToolName       string
	ToolInput      json.RawMessage
	ToolUseID      string
	AgentID        string
	AgentType      string
}

// PermissionRequestData mirrors the decoded Codex PermissionRequest payload.
type PermissionRequestData struct {
	SessionID      string
	TurnID         string
	TranscriptPath string
	CWD            string
	HookEventName  string
	Model          string
	PermissionMode string
	ToolName       string
	ToolInput      json.RawMessage
	AgentID        string
	AgentType      string
}

// Decoded holds exactly one non-nil DTO for the dispatched event.
type Decoded struct {
	Stop              *StopData
	SubagentStop      *SubagentStopData
	PreToolUse        *PreToolUseData
	PermissionRequest *PermissionRequestData
}

// InvocationForEvent maps the validated public event name to the prefixed SDK
// invocation name.
func InvocationForEvent(publicEvent string) (string, bool) {
	switch publicEvent {
	case "Stop":
		return "CodexStop", true
	case "SubagentStop":
		return "CodexSubagentStop", true
	case "PreToolUse":
		return "CodexPreToolUse", true
	case "PermissionRequest":
		return "CodexPermissionRequest", true
	}
	return "", false
}

// bufferedIO feeds the saved payload to the SDK and captures every byte the
// SDK tries to write; nothing is forwarded to the host process.
type bufferedIO struct {
	payload []byte
	reads   int
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func (b *bufferedIO) ReadStdin(ctx context.Context) ([]byte, error) {
	b.reads++
	cp := make([]byte, len(b.payload))
	copy(cp, b.payload)
	return cp, ctx.Err()
}

func (b *bufferedIO) WriteStdout(p []byte) error {
	_, err := b.stdout.Write(p)
	return err
}

func (b *bufferedIO) WriteStderr(s string) error {
	_, err := b.stderr.WriteString(s)
	return err
}

type osEnv struct{}

func (osEnv) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

// Decode dispatches the payload through the SDK with synthetic argv and
// in-memory IO and returns the typed host DTO. Any SDK failure is an error;
// the caller owns the fail-open observation contract.
func Decode(ctx context.Context, publicEvent string, payload []byte) (Decoded, error) {
	decoded, _, err := decodeWithIO(ctx, publicEvent, payload)
	return decoded, err
}

// decodeWithIO exposes the IO adapter to tests so they can assert the SDK
// consumed the payload exactly once and produced no process output.
func decodeWithIO(ctx context.Context, publicEvent string, payload []byte) (Decoded, *bufferedIO, error) {
	invocation, ok := InvocationForEvent(publicEvent)
	if !ok {
		return Decoded{}, nil, fmt.Errorf("unsupported codex event %q", publicEvent)
	}

	io := &bufferedIO{payload: payload}
	app := pluginkitai.New(pluginkitai.Config{
		Name: "claude-notifications",
		Args: []string{"claude-notifications", invocation},
		IO:   io,
		Env:  osEnv{},
	})

	var out Decoded
	registrar := app.Codex()
	registrar.OnStop(func(e *codex.StopEvent) *codex.Response {
		out.Stop = &StopData{
			SessionID:            e.SessionID,
			TurnID:               e.TurnID,
			TranscriptPath:       e.TranscriptPath,
			CWD:                  e.CWD,
			HookEventName:        e.HookEventName,
			Model:                e.Model,
			PermissionMode:       e.PermissionMode,
			StopHookActive:       e.StopHookActive,
			LastAssistantMessage: e.LastAssistantMessage,
		}
		return codex.Continue()
	})
	registrar.OnSubagentStop(func(e *codex.SubagentStopEvent) *codex.Response {
		out.SubagentStop = &SubagentStopData{
			Stop: StopData{
				SessionID:            e.SessionID,
				TurnID:               e.TurnID,
				TranscriptPath:       e.TranscriptPath,
				CWD:                  e.CWD,
				HookEventName:        e.HookEventName,
				Model:                e.Model,
				PermissionMode:       e.PermissionMode,
				StopHookActive:       e.StopHookActive,
				LastAssistantMessage: e.LastAssistantMessage,
			},
			AgentID:             e.AgentID,
			AgentType:           e.AgentType,
			AgentTranscriptPath: e.AgentTranscriptPath,
		}
		return codex.Continue()
	})
	registrar.OnPreToolUse(func(e *codex.PreToolUseEvent) *codex.Response {
		out.PreToolUse = &PreToolUseData{
			SessionID:      e.SessionID,
			TurnID:         e.TurnID,
			TranscriptPath: e.TranscriptPath,
			CWD:            e.CWD,
			HookEventName:  e.HookEventName,
			Model:          e.Model,
			PermissionMode: e.PermissionMode,
			ToolName:       e.ToolName,
			ToolInput:      cloneRaw(e.ToolInput),
			ToolUseID:      e.ToolUseID,
			AgentID:        e.AgentID,
			AgentType:      e.AgentType,
		}
		return codex.Continue()
	})
	registrar.OnPermissionRequest(func(e *codex.PermissionRequestEvent) *codex.Response {
		out.PermissionRequest = &PermissionRequestData{
			SessionID:      e.SessionID,
			TurnID:         e.TurnID,
			TranscriptPath: e.TranscriptPath,
			CWD:            e.CWD,
			HookEventName:  e.HookEventName,
			Model:          e.Model,
			PermissionMode: e.PermissionMode,
			ToolName:       e.ToolName,
			ToolInput:      cloneRaw(e.ToolInput),
			AgentID:        e.AgentID,
			AgentType:      e.AgentType,
		}
		return codex.Continue()
	})

	if code := app.RunContext(ctx); code != 0 {
		return Decoded{}, io, fmt.Errorf("sdk dispatch for %s failed: exit %d, stderr: %q", invocation, code, io.stderr.String())
	}
	if out.Stop == nil && out.SubagentStop == nil && out.PreToolUse == nil && out.PermissionRequest == nil {
		return Decoded{}, io, fmt.Errorf("sdk dispatch for %s produced no callback result", invocation)
	}
	return out, io, nil
}

// cloneRaw defensively copies raw JSON so SDK-owned buffers cannot mutate the
// normalized event after decode.
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	cp := make(json.RawMessage, len(raw))
	copy(cp, raw)
	return cp
}
