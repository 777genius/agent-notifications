// Package claudesource adapts legacy Claude Code hook input through the
// typed plugin-kit-ai Claude SDK. It decodes and maps events only; product
// policy remains in internal/hooks.
package claudesource

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/777genius/agent-notifications/internal/hooks"
	"github.com/777genius/agent-notifications/internal/logging"
	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/claude"
)

const sdkPlaceholder = "agent-notifications-sdk-placeholder"

// Source implements hooks.EventSource for the typed Claude SDK adapter.
type Source struct{}

// Decode preserves the legacy Claude decoder contract while dispatching known
// events through the typed SDK callbacks.
func Decode(ctx context.Context, hookEvent string, input io.Reader) (hooks.Event, error) {
	event, _, err := decodeWithIO(ctx, hookEvent, input)
	return event, err
}

// Decode makes Source a drop-in hooks.EventSource.
func (Source) Decode(ctx context.Context, hookEvent string, input io.Reader) (hooks.Event, error) {
	return Decode(ctx, hookEvent, input)
}

type bufferedIO struct {
	payload []byte
	reads   int
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func (b *bufferedIO) ReadStdin(ctx context.Context) ([]byte, error) {
	b.reads++
	payload := append([]byte(nil), b.payload...)
	return payload, ctx.Err()
}

func (b *bufferedIO) WriteStdout(payload []byte) error {
	_, err := b.stdout.Write(payload)
	return err
}

func (b *bufferedIO) WriteStderr(message string) error {
	_, err := b.stderr.WriteString(message)
	return err
}

type sdkValues struct {
	sessionID            string
	cwd                  string
	transcriptPath       string
	hookEventName        string
	toolName             string
	lastAssistantMessage string
	teamName             string
	teammateName         string
}

type sdkEnvelope struct {
	SessionID            string  `json:"session_id"`
	CWD                  string  `json:"cwd"`
	TranscriptPath       string  `json:"transcript_path"`
	HookEventName        string  `json:"hook_event_name"`
	ToolName             *string `json:"tool_name,omitempty"`
	LastAssistantMessage *string `json:"last_assistant_message,omitempty"`
	TeamName             *string `json:"team_name,omitempty"`
	TeammateName         *string `json:"teammate_name,omitempty"`
}

type sdkObservation struct {
	preToolUseCalled     bool
	notificationCalled   bool
	stopCalled           bool
	subagentStopCalled   bool
	teammateIdleCalled   bool
	sessionID            string
	cwd                  string
	transcriptPath       string
	hookEventName        string
	toolName             string
	lastAssistantMessage string
	teamName             string
	teammateName         string
}

func decodeWithIO(ctx context.Context, hookEvent string, input io.Reader) (hooks.Event, *bufferedIO, error) {
	raw, wire, err := decodeLegacyPayload(input)
	if err != nil {
		return hooks.Event{}, nil, err
	}

	sessionID := wire.SessionID
	if sessionID == "" {
		sessionID = "unknown"
		logging.Warn("Session ID is empty, using 'unknown'")
	}
	event := hooks.Event{
		Product:          hooks.ProductClaude,
		PayloadEventName: wire.HookEventName,
		Session: hooks.SessionContext{
			SessionID:      sessionID,
			CWD:            wire.CWD,
			TranscriptPath: wire.TranscriptPath,
		},
		Raw: append(json.RawMessage(nil), raw...),
	}

	if !knownEvent(hookEvent) {
		return event, nil, nil
	}

	sdkPayload, restored, err := dispatchPayload(hookEvent, wire)
	if err != nil {
		return hooks.Event{}, nil, err
	}
	sdkIO := &bufferedIO{payload: sdkPayload}
	app := pluginkitai.New(pluginkitai.Config{
		Args:   []string{"agent-notifications", hookEvent},
		IO:     sdkIO,
		Logger: pluginkitai.NopLogger{},
	})

	var observed sdkObservation
	registrar := app.Claude()
	registrar.OnPreToolUse(func(event *claude.PreToolUseEvent) *claude.PreToolResponse {
		observed.preToolUseCalled = true
		observed.sessionID = event.SessionID
		observed.cwd = event.CWD
		observed.transcriptPath = event.TranscriptPath
		observed.hookEventName = event.HookEventName
		observed.toolName = event.ToolName
		return nil
	})
	registrar.OnNotification(func(event *claude.NotificationEvent) *claude.NotificationResponse {
		observed.notificationCalled = true
		observed.sessionID = event.SessionID
		observed.cwd = event.CWD
		observed.transcriptPath = event.TranscriptPath
		observed.hookEventName = event.HookEventName
		return nil
	})
	registrar.OnStop(func(event *claude.StopEvent) *claude.Response {
		observed.stopCalled = true
		observed.sessionID = event.SessionID
		observed.cwd = event.CWD
		observed.transcriptPath = event.TranscriptPath
		observed.hookEventName = event.HookEventName
		observed.lastAssistantMessage = event.LastAssistantMessage
		return nil
	})
	registrar.OnSubagentStop(func(event *claude.SubagentStopEvent) *claude.SubagentStopResponse {
		observed.subagentStopCalled = true
		observed.sessionID = event.SessionID
		observed.cwd = event.CWD
		observed.transcriptPath = event.TranscriptPath
		observed.hookEventName = event.HookEventName
		return nil
	})
	registrar.OnTeammateIdle(func(event *claude.TeammateIdleEvent) *claude.TeammateIdleResponse {
		observed.teammateIdleCalled = true
		observed.sessionID = event.SessionID
		observed.cwd = event.CWD
		observed.transcriptPath = event.TranscriptPath
		observed.hookEventName = event.HookEventName
		observed.teamName = event.TeamName
		observed.teammateName = event.TeammateName
		return nil
	})

	if code := app.RunContext(ctx); code != 0 {
		return hooks.Event{}, sdkIO, fmt.Errorf("sdk dispatch for %s failed: exit %d", hookEvent, code)
	}
	if !callbackCalled(&observed, hookEvent) {
		return hooks.Event{}, sdkIO, fmt.Errorf("sdk dispatch for %s produced no callback result", hookEvent)
	}

	event.Session = hooks.SessionContext{
		SessionID:      restoreString(observed.sessionID, sessionID, restored),
		CWD:            restoreString(observed.cwd, wire.CWD, restored),
		TranscriptPath: restoreString(observed.transcriptPath, wire.TranscriptPath, restored),
	}
	event.PayloadEventName = restoreString(observed.hookEventName, wire.HookEventName, restored)

	switch hookEvent {
	case "PreToolUse":
		event.Payload = hooks.PreToolUsePayload{
			ToolName: restoreString(observed.toolName, wire.ToolName, restored),
		}
	case "Notification":
		event.Payload = hooks.NotificationPayload{}
	case "Stop":
		event.Payload = hooks.StopPayload{
			AssistantMessage: restoreString(observed.lastAssistantMessage, wire.LastAssistantMessage, restored),
		}
	case "SubagentStop":
		event.Payload = hooks.SubagentStopPayload{
			Stop: hooks.StopPayload{AssistantMessage: wire.LastAssistantMessage},
		}
	case "TeammateIdle":
		event.Payload = hooks.TeammateIdlePayload{
			TeamName:     restoreString(observed.teamName, wire.TeamName, restored),
			TeammateName: restoreString(observed.teammateName, wire.TeammateName, restored),
		}
	}

	if err := hooks.ValidateEvent(event); err != nil {
		return hooks.Event{}, sdkIO, err
	}
	return event, sdkIO, nil
}

func decodeLegacyPayload(input io.Reader) (json.RawMessage, hooks.HookData, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(skipUTF8BOM(input)).Decode(&raw); err != nil {
		return nil, hooks.HookData{}, fmt.Errorf("failed to parse hook data: %w", err)
	}

	var wire hooks.HookData
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, hooks.HookData{}, fmt.Errorf("failed to parse hook data: %w", err)
	}
	return append(json.RawMessage(nil), raw...), wire, nil
}

// skipUTF8BOM probes three bytes only after a possible UTF-8 BOM prefix.
func skipUTF8BOM(input io.Reader) io.Reader {
	reader := bufio.NewReader(input)
	prefix, err := reader.Peek(1)
	if err == nil && prefix[0] == 0xEF {
		prefix, err = reader.Peek(3)
		if err == nil && bytes.Equal(prefix, []byte{0xEF, 0xBB, 0xBF}) {
			_, _ = reader.Discard(3)
		}
	}
	return reader
}

func knownEvent(event string) bool {
	switch event {
	case "PreToolUse", "Notification", "Stop", "SubagentStop", "TeammateIdle":
		return true
	default:
		return false
	}
}

func callbackCalled(observed *sdkObservation, event string) bool {
	switch event {
	case "PreToolUse":
		return observed.preToolUseCalled
	case "Notification":
		return observed.notificationCalled
	case "Stop":
		return observed.stopCalled
	case "SubagentStop":
		return observed.subagentStopCalled
	case "TeammateIdle":
		return observed.teammateIdleCalled
	default:
		return false
	}
}

// dispatchPayload sends only the fields used by the product through the SDK.
// The legacy wire remains authoritative for the returned event and Raw: SDK
// DTOs also define fields that the legacy decoder ignored, and rejecting an
// incompatible value in one of those fields would change CLI behavior.
func dispatchPayload(event string, wire hooks.HookData) ([]byte, bool, error) {
	maxPayloadBytes := pluginkitai.MaxPayloadBytes

	values := sdkValues{
		sessionID:            sdkString(wire.SessionID),
		cwd:                  sdkString(wire.CWD),
		transcriptPath:       sdkString(wire.TranscriptPath),
		hookEventName:        event,
		toolName:             sdkString(wire.ToolName),
		lastAssistantMessage: sdkString(wire.LastAssistantMessage),
		teamName:             sdkString(wire.TeamName),
		teammateName:         sdkString(wire.TeammateName),
	}
	payload, err := marshalSDKEnvelope(event, values)
	if err != nil {
		return nil, false, err
	}
	if len(payload) > maxPayloadBytes {
		values = sdkValues{
			sessionID:            sdkPlaceholder,
			cwd:                  sdkPlaceholder,
			transcriptPath:       sdkPlaceholder,
			hookEventName:        event,
			toolName:             sdkPlaceholder,
			lastAssistantMessage: sdkPlaceholder,
			teamName:             sdkPlaceholder,
			teammateName:         sdkPlaceholder,
		}
		payload, err = marshalSDKEnvelope(event, values)
		if err != nil {
			return nil, false, err
		}
	}
	if len(payload) > maxPayloadBytes {
		return nil, false, fmt.Errorf("sdk projection for %s exceeds %d bytes", event, maxPayloadBytes)
	}
	return payload, true, nil
}

func marshalSDKEnvelope(event string, values sdkValues) ([]byte, error) {
	envelope := sdkEnvelope{
		SessionID:      values.sessionID,
		CWD:            values.cwd,
		TranscriptPath: values.transcriptPath,
		HookEventName:  values.hookEventName,
	}
	switch event {
	case "PreToolUse":
		envelope.ToolName = &values.toolName
	case "Stop":
		envelope.LastAssistantMessage = &values.lastAssistantMessage
	case "TeammateIdle":
		envelope.TeamName = &values.teamName
		envelope.TeammateName = &values.teammateName
	}
	return json.Marshal(envelope)
}

func sdkString(value string) string {
	if strings.TrimSpace(value) == "" || len(value) > pluginkitai.MaxPayloadBytes {
		return sdkPlaceholder
	}
	return value
}

func restoreString(decoded, original string, restored bool) string {
	if restored {
		return original
	}
	return decoded
}
