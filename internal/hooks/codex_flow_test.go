package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/codexsource"
	"github.com/777genius/agent-notifications/internal/config"
)

func codexTestConfig() *config.Config {
	return &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete":      {Title: "Completed"},
			"question":           {Title: "Question"},
			"permission_request": {Title: "Permission Request"},
		},
	}
}

func newCodexTestHandler(t *testing.T, decoded codexsource.Decoded) (*Handler, *mockNotifier, *mockWebhook) {
	t.Helper()

	// Codex identities hash into claude-*-codex-* filenames that the shared
	// newTestHandler cleanup patterns do not match; sweep them so repeated
	// runs on the same machine never inherit dedup/cooldown state.
	tempDir := os.TempDir()
	for _, pattern := range []string{
		"claude-session-state-codex-*.json",
		"claude-notification-codex-*.lock",
		"claude-content-lock-codex-*.lock",
	} {
		matches, _ := filepath.Glob(filepath.Join(tempDir, pattern))
		for _, f := range matches {
			_ = os.Remove(f)
		}
	}

	handler, mockNotif, mockWH := newTestHandler(t, codexTestConfig())
	handler.product = ProductCodex
	handler.source = CodexSource{DecodeFn: stubCodexDecode(decoded)}
	return handler, mockNotif, mockWH
}

// uniqueCodexSession returns a session id unique per test invocation so
// hashed state files can never collide across runs within the 180-second
// duplicate window.
func uniqueCodexSession(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

func codexStopData(session, turn, message string, continuation bool) *codexsource.StopData {
	return &codexsource.StopData{
		SessionID:            session,
		TurnID:               turn,
		TranscriptPath:       "/rollout.jsonl",
		CWD:                  "/proj",
		HookEventName:        "Stop",
		Model:                "gpt-5.6-sol",
		PermissionMode:       "bypassPermissions",
		StopHookActive:       continuation,
		LastAssistantMessage: message,
	}
}

func TestCodexFlowStopNotifies(t *testing.T) {
	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		Stop: codexStopData(uniqueCodexSession(t), "turn-1", "All tests pass.", false),
	})

	if err := handler.HandleHook("Stop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}

	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("expected notification")
	}
	if call.status != analyzer.StatusTaskComplete {
		t.Errorf("status = %v, want task_complete", call.status)
	}
	if !strings.Contains(call.message, "All tests pass.") {
		t.Errorf("message %q does not contain the assistant message", call.message)
	}
}

func TestCodexFlowStopQuestion(t *testing.T) {
	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		Stop: codexStopData(uniqueCodexSession(t), "turn-1", "Should I continue?", false),
	})

	if err := handler.HandleHook("Stop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}

	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("expected notification")
	}
	if call.status != analyzer.StatusQuestion {
		t.Errorf("status = %v, want question", call.status)
	}
}

func TestCodexFlowContinuationSuppressed(t *testing.T) {
	handler, mockNotif, mockWH := newCodexTestHandler(t, codexsource.Decoded{
		Stop: codexStopData(uniqueCodexSession(t), "turn-1", "OK", true),
	})

	if err := handler.HandleHook("Stop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}

	if mockNotif.wasCalled() || mockWH.wasCalled() {
		t.Fatal("continuation turn must not notify")
	}
}

func TestCodexFlowSubagentStopSkipped(t *testing.T) {
	handler, mockNotif, mockWH := newCodexTestHandler(t, codexsource.Decoded{
		SubagentStop: &codexsource.SubagentStopData{
			Stop:    *codexStopData(uniqueCodexSession(t), "turn-1", "done", false),
			AgentID: "a1",
		},
	})

	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}

	if mockNotif.wasCalled() || mockWH.wasCalled() {
		t.Fatal("codex SubagentStop delivery is out of scope and must not notify")
	}
}

func TestCodexFlowPermissionRequest(t *testing.T) {
	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		PermissionRequest: &codexsource.PermissionRequestData{
			SessionID:     uniqueCodexSession(t),
			TurnID:        "turn-1",
			CWD:           "/proj",
			HookEventName: "PermissionRequest",
			ToolName:      "shell",
			ToolInput:     []byte(`{"command":["rm","-rf","secret"],"api_key":"sk-XYZ"}`),
		},
	})

	if err := handler.HandleHook("PermissionRequest", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}

	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("expected notification")
	}
	if call.status != analyzer.StatusPermissionRequest {
		t.Errorf("status = %v, want permission_request", call.status)
	}
	if !strings.Contains(call.message, "shell") {
		t.Errorf("message %q must include the tool identity", call.message)
	}
	// ToolInput is never projected into the body.
	for _, secret := range []string{"sk-XYZ", "rm -rf", "api_key"} {
		if strings.Contains(call.message, secret) {
			t.Errorf("message %q leaks tool input %q", call.message, secret)
		}
	}
}

// TestCodexFlowPermissionRequestRepeatsAcrossTurns guards the contract that
// event dedup is turn-scoped: the same tool asking for approval again in a
// LATER turn must notify again even though the body text is identical.
func TestCodexFlowPermissionRequestRepeatsAcrossTurns(t *testing.T) {
	session := uniqueCodexSession(t)
	permData := func(turn string) *codexsource.PermissionRequestData {
		return &codexsource.PermissionRequestData{
			SessionID:     session,
			TurnID:        turn,
			CWD:           "/proj",
			HookEventName: "PermissionRequest",
			ToolName:      "shell",
			ToolInput:     []byte(`{}`),
		}
	}

	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{PermissionRequest: permData("turn-1")})
	if err := handler.HandleHook("PermissionRequest", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("first HandleHook() error = %v", err)
	}

	handler.source = CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{PermissionRequest: permData("turn-2")})}
	if err := handler.HandleHook("PermissionRequest", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("second HandleHook() error = %v", err)
	}

	if got := mockNotif.callCount(); got != 2 {
		t.Fatalf("permission request across two turns delivered %d notifications, want 2", got)
	}
}

func TestCodexFlowTurnScopedDedup(t *testing.T) {
	session := uniqueCodexSession(t)

	// Same turn twice: the second run must be deduplicated.
	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		Stop: codexStopData(session, "turn-A", "First.", false),
	})
	if err := handler.HandleHook("Stop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("first HandleHook() error = %v", err)
	}
	if err := handler.HandleHook("Stop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("second HandleHook() error = %v", err)
	}
	if got := mockNotif.callCount(); got != 1 {
		t.Fatalf("same-turn duplicate delivered %d notifications, want 1", got)
	}
}

func TestCodexFlowUnsupportedEventFails(t *testing.T) {
	handler, _, _ := newCodexTestHandler(t, codexsource.Decoded{})
	handler.source = CodexSource{DecodeFn: func(_ context.Context, publicEvent string, _ []byte) (codexsource.Decoded, error) {
		return codexsource.Decoded{}, fmt.Errorf("unsupported codex event %q", publicEvent)
	}}

	err := handler.HandleHook("PreToolUse", strings.NewReader(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported codex event") {
		t.Fatalf("error = %v, want unsupported event", err)
	}
}

// Holding the real lock models another process paused in its critical section.
// Delivery must happen before release, rather than after expiry or a retry.
func TestCodexDistinctEventsDeliverWhileContentLockHeld(t *testing.T) {
	for _, kind := range []string{"PermissionRequest", "PreToolUse", "SubagentStop", "Stop"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			t.Setenv("TMP", root)
			t.Setenv("TEMP", root)
			session := uniqueCodexSession(t)
			var decoded codexsource.Decoded
			switch kind {
			case "PermissionRequest":
				decoded.PermissionRequest = &codexsource.PermissionRequestData{SessionID: session, TurnID: "t", ToolName: "shell", HookEventName: kind}
			case "PreToolUse":
				decoded.PreToolUse = codexQuestionData(session, "t", `{"questions":[{"question":"Which option?"}]}`)
			case "SubagentStop":
				decoded.SubagentStop = &codexsource.SubagentStopData{Stop: *codexStopData(session, "t", "Done.", false), AgentID: "b"}
			case "Stop":
				decoded.Stop = codexStopData(session, "t", "Done.", false)
			}
			h, notifier, _ := newCodexTestHandler(t, decoded)
			no := false
			h.cfg.Notifications.SuppressForSubagents = &no
			h.cfg.Notifications.NotifyOnSubagentStop = true
			ev, err := h.source.Decode(context.Background(), kind, strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			key := codexKeys(ev).stateKey
			ok, err := h.dedupMgr.AcquireContentLock(key)
			if err != nil || !ok {
				t.Fatalf("hold lock: %v %v", ok, err)
			}
			defer func() {
				if err := h.dedupMgr.ReleaseContentLock(key); err != nil {
					t.Error(err)
				}
			}()
			if err := h.HandleHook(kind, strings.NewReader(`{}`)); err != nil {
				t.Fatal(err)
			}
			want := 1
			if kind == "Stop" {
				want = 0
			}
			if got := notifier.callCount(); got != want {
				t.Fatalf("deliveries while lock held = %d, want %d", got, want)
			}
			if ok, err := h.dedupMgr.AcquireContentLock(key); err != nil || ok {
				t.Fatalf("handler removed another event's lock: %v %v", ok, err)
			}
			if kind == "SubagentStop" {
				if err := h.dedupMgr.ReleaseContentLock(key); err != nil {
					t.Fatal(err)
				}
				decoded.SubagentStop.AgentID = "a"
				h.source = CodexSource{DecodeFn: stubCodexDecode(decoded)}
				if err := h.HandleHook(kind, strings.NewReader(`{}`)); err != nil {
					t.Fatal(err)
				}
				if notifier.callCount() != 2 {
					t.Fatal("both overlapping subagents must deliver")
				}
			}
		})
	}
}

func TestClaudePermissionRetainsHeldContentLockBehavior(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	h, notifier, _ := newTestHandler(t, codexTestConfig())
	session := "test-held-claude-permission"
	ok, err := h.dedupMgr.AcquireContentLock(session)
	if err != nil || !ok {
		t.Fatalf("hold lock: %v %v", ok, err)
	}
	defer func() {
		if err := h.dedupMgr.ReleaseContentLock(session); err != nil {
			t.Error(err)
		}
	}()
	payload := `{"session_id":"test-held-claude-permission","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Permission required"}`
	if err := h.HandleHook("Notification", strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if notifier.callCount() != 0 {
		t.Fatal("Claude permission must still respect the session content lock")
	}
	if ok, err := h.dedupMgr.AcquireContentLock(session); err != nil || ok {
		t.Fatalf("Claude removed the held lock: %v %v", ok, err)
	}
	if err := h.dedupMgr.ReleaseContentLock(session); err != nil {
		t.Fatal(err)
	}
	// Claude intentionally retains its event lock for two seconds even when
	// content locking suppresses delivery. Use a fresh session for the control.
	payload = strings.ReplaceAll(payload, session, "test-free-claude-permission")
	if err := h.HandleHook("Notification", strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if notifier.callCount() != 1 {
		t.Fatal("a fresh Claude permission must deliver without a held content lock")
	}
}
