package opencodeevent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/sessionname"
	"github.com/777genius/agent-notifications/internal/strictjson"
	uap "github.com/777genius/plugin-kit-ai/sdk/opencode"
)

// Separate the optional product display envelope before strict neutral decoding.
// A bad optional value cannot invalidate a valid neutral fact. Top-level duplicate
// keys, nonobjects and trailing data remain invalid frame errors.
func splitDisplay(raw []byte) (neutral, display []byte, err error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil, errors.New("invalid_frame")
	}
	seen := map[string]bool{}
	neutral = []byte{'{'}
	for d.More() {
		keyStart := d.InputOffset()
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return nil, nil, errors.New("invalid_frame")
		}
		// Keep invalid UTF-8/surrogates in top-level keys from being silently
		// repaired by encoding/json while allowing invalid optional values.
		keyRaw := bytes.TrimSpace(raw[keyStart:d.InputOffset()])
		keyRaw = bytes.TrimSpace(bytes.TrimPrefix(keyRaw, []byte{','}))
		if strictjson.Validate(keyRaw, strictjson.Budget{Bytes: uap.MaxBytes, Depth: 1, Entries: 1}) != nil {
			return nil, nil, errors.New("invalid_frame")
		}
		seen[key] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, nil, err
		}
		if key == "display" {
			display = value
			continue
		}
		if len(neutral) > 1 {
			neutral = append(neutral, ',')
		}
		encoded, _ := json.Marshal(key)
		neutral = append(neutral, encoded...)
		neutral = append(neutral, ':')
		neutral = append(neutral, value...)
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return nil, nil, errors.New("invalid_frame")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, nil, errors.New("invalid_frame")
	}
	return append(neutral, '}'), display, nil
}

type displayContext struct {
	SessionID    string `json:"sessionID"`
	RequestID    string `json:"requestID,omitempty"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	Question     string `json:"question,omitempty"`
}

func desktopContent(event uap.ObservedEvent, raw []byte, generic notification.Content, labels bool) notification.Content {
	if len(raw) == 0 || strictjson.Validate(raw, strictjson.Budget{Bytes: uap.MaxBytes, Depth: 2, Entries: 4}) != nil {
		return generic
	}
	var display displayContext
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&display) != nil || display.SessionID != event.SessionID ||
		(event.Kind == uap.QuestionAsked && display.RequestID != event.RequestID) ||
		(event.Kind != uap.QuestionAsked && display.RequestID != "") {
		return generic
	}
	title, question := "", ""
	if len(display.SessionTitle) <= 1024 {
		title = sessionname.CleanNativeTitle(display.SessionTitle)
	}
	if event.Kind == uap.QuestionAsked && len(display.Question) <= 2048 {
		question = sessionname.CleanNativeTitle(display.Question)
	}
	if (!labels || title == "") && question == "" {
		return generic
	}
	statusTitle := generic.Title
	if event.Kind == uap.TurnIdleVerified {
		statusTitle = "✅ Completed"
	}
	if event.Kind == uap.QuestionAsked {
		statusTitle = "❓ Question"
	}
	body := generic.Body
	if question != "" {
		body = question
	}
	content := notifier.HookDesktopContent(mapStatus(event.Kind), notifier.HookPresentation{
		SessionName: title, Question: question, Body: body,
	}, statusTitle, labels)
	content.Category = generic.Category
	return content
}
