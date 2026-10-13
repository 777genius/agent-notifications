package geminisource

import (
	"bytes"
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
	"github.com/777genius/plugin-kit-ai/sdk/gemini"
)

const MaxPayloadBytes = pluginkitai.MaxPayloadBytes

// InvocationForEvent binds only the two fixed trusted command selectors.
func InvocationForEvent(selector string) (string, bool) {
	switch selector {
	case AfterAgent:
		return "GeminiAfterAgent", true
	case Notification:
		return "GeminiNotification", true
	default:
		return "", false
	}
}

type bufferedIO struct {
	payload        []byte
	reads          int
	stdout, stderr bytes.Buffer
}

func (b *bufferedIO) ReadStdin(ctx context.Context) ([]byte, error) {
	b.reads++
	return append([]byte(nil), b.payload...), ctx.Err()
}
func (b *bufferedIO) WriteStdout(data []byte) error { _, err := b.stdout.Write(data); return err }
func (b *bufferedIO) WriteStderr(data string) error { _, err := b.stderr.WriteString(data); return err }

type emptyEnv struct{}

func (emptyEnv) LookupEnv(string) (string, bool) { return "", false }

// Decode returns only validated native facts. SDK diagnostics stay in buffered
// IO; failures and panics return fixed errors without input or raw causes.
func Decode(ctx context.Context, selector string, payload []byte) (Facts, error) {
	facts, _, err := decodeWithIO(ctx, selector, payload)
	return facts, err
}
func decodeWithIO(ctx context.Context, selector string, payload []byte) (facts Facts, input *bufferedIO, err error) {
	defer func() {
		if recover() != nil {
			facts, err = Facts{}, errors.New("invalid_frame")
		}
	}()
	invocation, ok := InvocationForEvent(selector)
	if !ok {
		return Facts{}, nil, errors.New("unsupported_selector")
	}
	if ctx.Err() != nil || len(payload) == 0 || len(payload) > MaxPayloadBytes {
		return Facts{}, nil, errors.New("invalid_frame")
	}
	input = &bufferedIO{payload: payload}
	app := pluginkitai.New(pluginkitai.Config{Name: "agent-notifications", Args: []string{"agent-notifications", invocation}, IO: input, Env: emptyEnv{}})
	valid := false
	registrar := app.Gemini()
	registrar.OnAfterAgent(func(event *gemini.AfterAgentEvent) *gemini.AfterAgentResponse {
		// Native identity is checked before dropping the SDK DTO. Dispatch's argv
		// event name alone cannot establish that this is an AfterAgent observation.
		if event.HookEventName == selector && validSession(event.SessionID) && validTimestamp(event.Timestamp) {
			facts = Facts{SessionID: event.SessionID, Timestamp: event.Timestamp, Event: AfterAgent, StopHookActive: event.StopHookActive}
			valid = true
		}
		return &gemini.AfterAgentResponse{}
	})
	registrar.OnNotification(func(event *gemini.NotificationEvent) *gemini.NotificationResponse {
		if event.HookEventName == selector && validSession(event.SessionID) && validTimestamp(event.Timestamp) {
			subtype := ""
			if event.NotificationType == gemini.NotificationTypeToolPermission {
				subtype = ToolPermission
			}
			// Never pass unknown subtype strings, message or details into business
			// data; unsupported observations carry only the validated native event.
			facts = Facts{SessionID: event.SessionID, Timestamp: event.Timestamp, Event: Notification, Subtype: subtype}
			valid = true
		}
		return &gemini.NotificationResponse{}
	})
	if app.RunContext(ctx) != 0 || !valid || ctx.Err() != nil {
		return Facts{}, input, errors.New("invalid_frame")
	}
	return facts, input, nil
}
func validSession(session string) bool {
	if session == "" || !utf8.ValidString(session) {
		return false
	}
	for _, r := range session {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validTimestamp(timestamp string) bool {
	if timestamp == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, timestamp)
	return err == nil
}
