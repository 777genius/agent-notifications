package codexsource

import (
	"context"
	"strings"
	"testing"
)

const realStopPayload = `{"session_id":"01a05cd8-b495-7f80-a36b-cc0aa98efc05","turn_id":"01a05cd8-b51b-7343-8b75-b2d4ad9e276e","transcript_path":"/tmp/rollout.jsonl","cwd":"/tmp/proj","hook_event_name":"Stop","model":"gpt-5.6-sol","permission_mode":"bypassPermissions","stop_hook_active":false,"last_assistant_message":"OK"}`

func TestDecodeStopThroughSDK(t *testing.T) {
	decoded, err := Decode(context.Background(), "Stop", []byte(realStopPayload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.Stop == nil {
		t.Fatal("Stop DTO missing")
	}
	if decoded.SubagentStop != nil || decoded.PermissionRequest != nil {
		t.Fatal("unexpected extra DTOs")
	}
	d := decoded.Stop
	if d.SessionID != "01a05cd8-b495-7f80-a36b-cc0aa98efc05" ||
		d.TurnID != "01a05cd8-b51b-7343-8b75-b2d4ad9e276e" ||
		d.Model != "gpt-5.6-sol" ||
		d.PermissionMode != "bypassPermissions" ||
		d.LastAssistantMessage != "OK" ||
		d.StopHookActive {
		t.Fatalf("StopData = %+v", *d)
	}
}

func TestDecodePermissionRequestThroughSDK(t *testing.T) {
	payload := `{"session_id":"s","turn_id":"t","hook_event_name":"PermissionRequest","tool_name":"shell","tool_input":{"command":["ls"]},"agent_id":"a1","agent_type":"worker"}`
	decoded, err := Decode(context.Background(), "PermissionRequest", []byte(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.PermissionRequest == nil {
		t.Fatal("PermissionRequest DTO missing")
	}
	d := decoded.PermissionRequest
	if d.ToolName != "shell" || d.AgentID != "a1" || d.AgentType != "worker" {
		t.Fatalf("PermissionRequestData = %+v", *d)
	}
	if string(d.ToolInput) != `{"command":["ls"]}` {
		t.Fatalf("ToolInput = %s", d.ToolInput)
	}
}

func TestDecodePreToolUseThroughSDK(t *testing.T) {
	payload := `{"session_id":"s","turn_id":"t","hook_event_name":"PreToolUse","tool_name":"request_user_input","tool_input":{"questions":[{"id":"q1","question":"Which one?"}]},"tool_use_id":"call-1"}`
	decoded, err := Decode(context.Background(), "PreToolUse", []byte(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.PreToolUse == nil {
		t.Fatal("PreToolUse DTO missing")
	}
	d := decoded.PreToolUse
	if d.ToolName != "request_user_input" || d.ToolUseID != "call-1" {
		t.Fatalf("PreToolUseData = %+v", *d)
	}
	if string(d.ToolInput) != `{"questions":[{"id":"q1","question":"Which one?"}]}` {
		t.Fatalf("ToolInput = %s", d.ToolInput)
	}
}

func TestDecodeSubagentStopThroughSDK(t *testing.T) {
	payload := `{"session_id":"s","turn_id":"t","hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"worker","agent_transcript_path":"/at","last_assistant_message":"done"}`
	decoded, err := Decode(context.Background(), "SubagentStop", []byte(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.SubagentStop == nil {
		t.Fatal("SubagentStop DTO missing")
	}
	if decoded.SubagentStop.AgentID != "a1" || decoded.SubagentStop.Stop.LastAssistantMessage != "done" {
		t.Fatalf("SubagentStopData = %+v", *decoded.SubagentStop)
	}
}

// TestDecodeConsumesPayloadOnceWithNoOutput proves the SDK read the saved
// payload exactly once and wrote nothing that could leak to the host process.
func TestDecodeConsumesPayloadOnceWithNoOutput(t *testing.T) {
	decoded, io, err := decodeWithIO(context.Background(), "Stop", []byte(realStopPayload))
	if err != nil {
		t.Fatalf("decodeWithIO() error = %v", err)
	}
	if decoded.Stop == nil {
		t.Fatal("Stop DTO missing")
	}
	if io.reads != 1 {
		t.Fatalf("payload consumed %d times, want exactly 1", io.reads)
	}
	if io.stdout.Len() != 0 || io.stderr.Len() != 0 {
		t.Fatalf("SDK wrote output: stdout=%q stderr=%q", io.stdout.String(), io.stderr.String())
	}
}

func TestDecodeUnsupportedEvent(t *testing.T) {
	_, err := Decode(context.Background(), "SessionStart", []byte(realStopPayload))
	if err == nil || !strings.Contains(err.Error(), "unsupported codex event") {
		t.Fatalf("error = %v, want unsupported event", err)
	}
}

func TestDecodeMalformedPayload(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":     nil,
		"malformed": []byte(`{oops`),
	} {
		if _, err := Decode(context.Background(), "Stop", payload); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestInvocationForEvent(t *testing.T) {
	cases := map[string]string{
		"Stop":              "CodexStop",
		"SubagentStop":      "CodexSubagentStop",
		"PreToolUse":        "CodexPreToolUse",
		"PermissionRequest": "CodexPermissionRequest",
	}
	for event, want := range cases {
		got, ok := InvocationForEvent(event)
		if !ok || got != want {
			t.Errorf("InvocationForEvent(%q) = %q,%v want %q", event, got, ok, want)
		}
	}
	if _, ok := InvocationForEvent("Notification"); ok {
		t.Error("Notification must not map to a codex invocation")
	}
}

func TestValidateProductOverride(t *testing.T) {
	if p, err := ValidateProductOverride("codex"); err != nil || p != "codex" {
		t.Fatalf("codex override = %q, %v", p, err)
	}
	if p, err := ValidateProductOverride("claude"); err != nil || p != "claude" {
		t.Fatalf("claude override = %q, %v", p, err)
	}
	if _, err := ValidateProductOverride("cursor"); err == nil {
		t.Fatal("unknown override must be an error")
	}
}
