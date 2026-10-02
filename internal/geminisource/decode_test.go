package geminisource

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Red condition: SDK argv dispatch alone accepts a mismatched native event;
// dropping the typed DTO first loses the evidence required to reject it.
func TestAfterAgentNativeIdentityAndPrivacy(t *testing.T) {
	raw := []byte(`{"session_id":"native-session","timestamp":"2026-10-01T05:00:00.001Z","hook_event_name":"AfterAgent","prompt":"PRIVATE_PROMPT","prompt_response":"PRIVATE_RESPONSE","cwd":"PRIVATE_CWD","transcript_path":"PRIVATE_TRANSCRIPT","message":"PRIVATE_MESSAGE","details":{"private":"PRIVATE_DETAILS"}}`)
	facts, input, err := decodeWithIO(context.Background(), AfterAgent, raw)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_") {
		t.Fatalf("private data escaped: %s", data)
	}
	if facts != (Facts{SessionID: "native-session", Timestamp: "2026-10-01T05:00:00.001Z", Event: AfterAgent}) || input.reads != 1 {
		t.Fatalf("wrong facts or reads: %+v / %d", facts, input.reads)
	}
	mismatch := strings.Replace(string(raw), `"hook_event_name":"AfterAgent"`, `"hook_event_name":"Notification"`, 1)
	facts, _, err = decodeWithIO(context.Background(), AfterAgent, []byte(mismatch))
	if err == nil || facts != (Facts{}) || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatalf("native mismatch escaped: %+v / %v", facts, err)
	}
}

// Red condition: invalid scalar/trailing/oversized native input or absent
// identity reaches business delivery, or SDK failure exposes raw input.
func TestAfterAgentRejectsInvalidNativeFrames(t *testing.T) {
	for _, raw := range []string{
		`{"hook_event_name":"AfterAgent","session_id":"s","stop_hook_active":"PRIVATE_BAD_BOOL"}`,
		`{"hook_event_name":"AfterAgent","session_id":"s","timestamp":123}`,
		`{"hook_event_name":"AfterAgent","session_id":"s","timestamp":"PRIVATE_BAD_TIME"}`,
		`{"hook_event_name":"AfterAgent","session_id":"s"} {}`,
		`{"hook_event_name":"AfterAgent"}`,
		`{"hook_event_name":"AfterAgent","session_id":"s\nprivate"}`,
		strings.Repeat(" ", MaxPayloadBytes+1),
	} {
		facts, err := Decode(context.Background(), AfterAgent, []byte(raw))
		if err == nil || facts != (Facts{}) || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("accepted private/invalid frame: %+v / %v", facts, err)
		}
	}
}

// Red condition: the source converts a recursive stop observation to a normal
// completion, or invents a timestamp for an observation lacking one.
func TestAfterAgentPreservesNativeStopAndMissingTimestamp(t *testing.T) {
	facts, err := Decode(context.Background(), AfterAgent, []byte(`{"session_id":"s","hook_event_name":"AfterAgent","stop_hook_active":true}`))
	if err != nil || !facts.StopHookActive || facts.Timestamp != "" {
		t.Fatalf("native facts changed: %+v / %v", facts, err)
	}
}

// Red condition: native Notification bypasses trusted selector validation or
// carries arbitrary message/details/subtype text outside the typed source.
func TestNotificationTypedDispatchIdentityAndPrivacy(t *testing.T) {
	raw := `{"session_id":"native-session","timestamp":"2026-10-01T05:00:00.001Z","hook_event_name":"Notification","notification_type":"ToolPermission","message":"PRIVATE_MESSAGE","details":{"tool":"PRIVATE_TOOL","nested":{"prompt":"PRIVATE_PROMPT"}},"cwd":"PRIVATE_CWD","transcript_path":"PRIVATE_PATH","prompt":"PRIVATE_PROMPT"}`
	facts, input, err := decodeWithIO(context.Background(), Notification, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if facts != (Facts{SessionID: "native-session", Timestamp: "2026-10-01T05:00:00.001Z", Event: Notification, Subtype: ToolPermission}) || input.reads != 1 {
		t.Fatalf("wrong typed Notification facts: %+v", facts)
	}
	data, _ := json.Marshal(facts)
	if strings.Contains(string(data), "PRIVATE_") {
		t.Fatalf("native data escaped: %s", data)
	}
	for _, selector := range []string{AfterAgent, Notification} {
		mismatch := raw
		if selector == Notification {
			mismatch = strings.Replace(raw, `"hook_event_name":"Notification"`, `"hook_event_name":"AfterAgent"`, 1)
		}
		facts, err := Decode(context.Background(), selector, []byte(mismatch))
		if err == nil || facts != (Facts{}) || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("selector/native mismatch escaped: %+v / %v", facts, err)
		}
	}
	unknown := strings.Replace(raw, `"notification_type":"ToolPermission"`, `"notification_type":"PRIVATE_UNKNOWN_SUBTYPE"`, 1)
	facts, err = Decode(context.Background(), Notification, []byte(unknown))
	if err != nil || facts.Subtype != "" || facts.Event != Notification {
		t.Fatalf("unknown subtype forwarded/classified: %+v / %v", facts, err)
	}
}

// Red condition: non-string notification_type, non-object details or missing
// native HookEventName dispatches business delivery with a forged subtype.
func TestNotificationRejectsInvalidNativeScalars(t *testing.T) {
	for _, raw := range []string{
		`{"hook_event_name":"Notification","session_id":"s","notification_type":123}`,
		`{"hook_event_name":"Notification","session_id":"s","notification_type":"ToolPermission","details":"PRIVATE_INVALID_DETAILS"}`,
		`{"session_id":"s","notification_type":"ToolPermission"}`,
	} {
		facts, err := Decode(context.Background(), Notification, []byte(raw))
		if err == nil || facts != (Facts{}) || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("invalid notification escaped: %+v / %v", facts, err)
		}
	}
}
