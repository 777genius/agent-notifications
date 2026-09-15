package hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexsource"
)

func stubCodexDecode(decoded codexsource.Decoded) CodexDecodeFunc {
	return func(_ context.Context, _ string, _ []byte) (codexsource.Decoded, error) {
		return decoded, nil
	}
}

func TestCodexSourceMapsStop(t *testing.T) {
	src := CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{
		Stop: &codexsource.StopData{
			SessionID:            "sess",
			TurnID:               "turn",
			TranscriptPath:       "/rollout.jsonl",
			CWD:                  "/proj",
			HookEventName:        "Stop",
			Model:                "gpt-5.6-sol",
			PermissionMode:       "bypassPermissions",
			StopHookActive:       true,
			LastAssistantMessage: "OK",
		},
	})}

	ev, err := src.Decode(context.Background(), "Stop", strings.NewReader(`{"x":1}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ev.Product != ProductCodex {
		t.Fatalf("Product = %q", ev.Product)
	}
	if ev.Session.SessionID != "sess" || ev.Session.TurnID != "turn" || ev.Session.Model != "gpt-5.6-sol" {
		t.Fatalf("Session = %+v", ev.Session)
	}
	p, ok := ev.Payload.(StopPayload)
	if !ok {
		t.Fatalf("Payload = %#v", ev.Payload)
	}
	// Wire names must not leak: last_assistant_message → AssistantMessage,
	// stop_hook_active → Continuation.
	if p.AssistantMessage != "OK" || !p.Continuation {
		t.Fatalf("StopPayload = %+v", p)
	}
}

func TestCodexSourceMapsSubagentStop(t *testing.T) {
	src := CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{
		SubagentStop: &codexsource.SubagentStopData{
			Stop: codexsource.StopData{
				SessionID:            "sess",
				TurnID:               "turn",
				HookEventName:        "SubagentStop",
				LastAssistantMessage: "done",
			},
			AgentID:             "a1",
			AgentType:           "worker",
			AgentTranscriptPath: "/agent.jsonl",
		},
	})}

	ev, err := src.Decode(context.Background(), "SubagentStop", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	p, ok := ev.Payload.(SubagentStopPayload)
	if !ok {
		t.Fatalf("Payload = %#v", ev.Payload)
	}
	if p.Agent == nil || p.Agent.ID != "a1" || p.Agent.Type != "worker" || p.Agent.TranscriptPath != "/agent.jsonl" {
		t.Fatalf("Agent = %+v", p.Agent)
	}
	if p.Stop.AssistantMessage != "done" {
		t.Fatalf("Stop = %+v", p.Stop)
	}
}

func TestCodexSourceMapsPermissionRequest(t *testing.T) {
	src := CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{
		PermissionRequest: &codexsource.PermissionRequestData{
			SessionID:     "sess",
			TurnID:        "turn",
			HookEventName: "PermissionRequest",
			ToolName:      "shell",
			ToolInput:     []byte(`{"command":["ls"]}`),
		},
	})}

	ev, err := src.Decode(context.Background(), "PermissionRequest", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	p, ok := ev.Payload.(PermissionRequestPayload)
	if !ok {
		t.Fatalf("Payload = %#v", ev.Payload)
	}
	if p.ToolName != "shell" || string(p.ToolInput) != `{"command":["ls"]}` {
		t.Fatalf("PermissionRequestPayload = %+v", p)
	}
	if p.Agent != nil {
		t.Fatalf("Agent = %+v, want nil when absent on the wire", p.Agent)
	}
}

func TestCodexSourceRejectsMissingSession(t *testing.T) {
	src := CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{
		Stop: &codexsource.StopData{TurnID: "turn"},
	})}
	if _, err := src.Decode(context.Background(), "Stop", strings.NewReader(`{}`)); err == nil {
		t.Fatal("expected validation error for missing session id")
	}
}

func TestCodexSourceRejectsSubagentWithoutAgent(t *testing.T) {
	src := CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{
		SubagentStop: &codexsource.SubagentStopData{
			Stop: codexsource.StopData{SessionID: "s", TurnID: "t"},
		},
	})}
	_, err := src.Decode(context.Background(), "SubagentStop", strings.NewReader(`{}`))
	if err == nil || !strings.Contains(err.Error(), "agent identity") {
		t.Fatalf("error = %v, want agent identity validation", err)
	}
}

func TestCodexSourceOversizedPayload(t *testing.T) {
	src := CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{})}
	huge := strings.NewReader(strings.Repeat("a", codexsource.MaxPayloadBytes+1))
	_, err := src.Decode(context.Background(), "Stop", huge)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want size guard", err)
	}
}

func TestCodexSourceUnwired(t *testing.T) {
	if _, err := (CodexSource{}).Decode(context.Background(), "Stop", strings.NewReader(`{}`)); err == nil {
		t.Fatal("expected error for unwired source")
	}
}
