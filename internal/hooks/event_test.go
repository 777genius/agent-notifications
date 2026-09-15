package hooks

import (
	"strings"
	"testing"
)

func TestEventKindDerivesFromPayload(t *testing.T) {
	cases := []struct {
		payload EventPayload
		want    EventKind
	}{
		{payload: StopPayload{}, want: EventStop},
		{payload: SubagentStopPayload{}, want: EventSubagentStop},
		{payload: PermissionRequestPayload{}, want: EventPermissionRequest},
		{payload: PreToolUsePayload{}, want: EventPreToolUse},
		{payload: NotificationPayload{}, want: EventNotification},
		{payload: TeammateIdlePayload{}, want: EventTeammateIdle},
		{payload: nil, want: EventUnknown},
	}
	for _, tc := range cases {
		ev := Event{Payload: tc.payload}
		if got := ev.Kind(); got != tc.want {
			t.Errorf("Kind() with %T = %q, want %q", tc.payload, got, tc.want)
		}
	}
}

func TestValidateEvent(t *testing.T) {
	valid := Event{
		Product: ProductClaude,
		Session: SessionContext{SessionID: "s"},
		Payload: StopPayload{},
	}
	if err := ValidateEvent(valid); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}

	cases := []struct {
		name    string
		event   Event
		wantErr string
	}{
		{
			name:    "unknown product",
			event:   Event{Product: "cursor", Session: SessionContext{SessionID: "s"}, Payload: StopPayload{}},
			wantErr: "unknown event product",
		},
		{
			name:    "empty product",
			event:   Event{Session: SessionContext{SessionID: "s"}, Payload: StopPayload{}},
			wantErr: "unknown event product",
		},
		{
			name:    "nil payload",
			event:   Event{Product: ProductClaude, Session: SessionContext{SessionID: "s"}},
			wantErr: "no payload",
		},
		{
			name:    "missing session",
			event:   Event{Product: ProductCodex, Payload: StopPayload{}},
			wantErr: "session id",
		},
		{
			name: "codex subagent stop without agent",
			event: Event{
				Product: ProductCodex,
				Session: SessionContext{SessionID: "s"},
				Payload: SubagentStopPayload{},
			},
			wantErr: "agent identity",
		},
	}
	for _, tc := range cases {
		err := ValidateEvent(tc.event)
		if err == nil {
			t.Errorf("%s: expected error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.wantErr)
		}
	}

	claudeSubagent := Event{
		Product: ProductClaude,
		Session: SessionContext{SessionID: "s"},
		Payload: SubagentStopPayload{},
	}
	if err := ValidateEvent(claudeSubagent); err != nil {
		t.Fatalf("legacy claude subagent stop without agent rejected: %v", err)
	}
}

func TestCodexKeysAreTurnScopedAndFilenameSafe(t *testing.T) {
	base := Event{
		Product: ProductCodex,
		Session: SessionContext{SessionID: "sess/../1", TurnID: "turn:1"},
		Payload: StopPayload{},
	}
	k1 := codexKeys(base)

	otherTurn := base
	otherTurn.Session.TurnID = "turn:2"
	k2 := codexKeys(otherTurn)

	if k1.stateKey != k2.stateKey {
		t.Fatalf("state key must be session-scoped: %q vs %q", k1.stateKey, k2.stateKey)
	}
	if k1.lockKey == k2.lockKey {
		t.Fatal("lock key must be turn-scoped")
	}
	for _, key := range []string{k1.stateKey, k1.lockKey} {
		if strings.ContainsAny(key, "/\\:.") {
			t.Fatalf("key %q is not filename-safe", key)
		}
	}

	// Length-prefixed hashing is injective: shifting a boundary changes the key.
	a := hashedIdentity("ab", "c")
	b := hashedIdentity("a", "bc")
	if a == b {
		t.Fatal("length-prefixed identity collided across field boundaries")
	}

	// Tool identity separates permission request locks.
	perm := base
	perm.Payload = PermissionRequestPayload{ToolName: "shell"}
	perm2 := base
	perm2.Payload = PermissionRequestPayload{ToolName: "web"}
	if codexKeys(perm).lockKey == codexKeys(perm2).lockKey {
		t.Fatal("permission request lock key must include tool identity")
	}
}
