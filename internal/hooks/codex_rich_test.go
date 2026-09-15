package hooks

import (
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/codexsource"
)

func codexQuestionData(session, turn string, toolInput string) *codexsource.PreToolUseData {
	return &codexsource.PreToolUseData{
		SessionID:      session,
		TurnID:         turn,
		CWD:            "/proj",
		HookEventName:  "PreToolUse",
		Model:          "gpt-5.6-sol",
		PermissionMode: "default",
		ToolName:       "request_user_input",
		ToolInput:      []byte(toolInput),
		ToolUseID:      "call-1",
	}
}

func TestCodexFlowQuestionToolNotifies(t *testing.T) {
	toolInput := `{"questions":[{"id":"q1","header":"Approach","question":"Should I use **variant A** or variant B?","is_secret":false,"options":[{"label":"A","description":"secret-option-detail"}]},{"id":"q2","header":"Scope","question":"Include tests?"}],"is_blocking":true}`
	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		PreToolUse: codexQuestionData(uniqueCodexSession(t), "turn-1", toolInput),
	})

	if err := handler.HandleHook("PreToolUse", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}

	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("expected notification")
	}
	if call.status != analyzer.StatusQuestion {
		t.Errorf("status = %v, want question", call.status)
	}
	if !strings.Contains(call.message, "Should I use variant A or variant B?") {
		t.Errorf("message %q must contain the cleaned question text", call.message)
	}
	if !strings.Contains(call.message, "(+1 more)") {
		t.Errorf("message %q must count the extra question", call.message)
	}
	// Allowlist projection: option details and ids never reach the body.
	for _, leaked := range []string{"secret-option-detail", "q1", "is_blocking"} {
		if strings.Contains(call.message, leaked) {
			t.Errorf("message %q leaks tool input field %q", call.message, leaked)
		}
	}
}

func TestCodexFlowNonQuestionToolSkipped(t *testing.T) {
	data := codexQuestionData(uniqueCodexSession(t), "turn-1", `{}`)
	data.ToolName = "shell"
	handler, mockNotif, mockWH := newCodexTestHandler(t, codexsource.Decoded{PreToolUse: data})

	if err := handler.HandleHook("PreToolUse", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}
	if mockNotif.wasCalled() || mockWH.wasCalled() {
		t.Fatal("non-question tool must not notify")
	}
}

func TestCodexFlowQuestionRepeatsAcrossTurns(t *testing.T) {
	session := uniqueCodexSession(t)
	toolInput := `{"questions":[{"question":"Proceed with refactor?"}]}`

	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		PreToolUse: codexQuestionData(session, "turn-1", toolInput),
	})
	if err := handler.HandleHook("PreToolUse", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("first HandleHook() error = %v", err)
	}

	second := codexQuestionData(session, "turn-2", toolInput)
	second.ToolUseID = "call-2"
	handler.source = CodexSource{DecodeFn: stubCodexDecode(codexsource.Decoded{PreToolUse: second})}
	if err := handler.HandleHook("PreToolUse", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("second HandleHook() error = %v", err)
	}

	if got := mockNotif.callCount(); got != 2 {
		t.Fatalf("identical question across turns delivered %d notifications, want 2", got)
	}
}

func TestCodexFlowSubagentStopOptIn(t *testing.T) {
	makeDecoded := func(session string) codexsource.Decoded {
		return codexsource.Decoded{
			SubagentStop: &codexsource.SubagentStopData{
				Stop:    *codexStopData(session, "turn-1", "Subagent finished the migration.", false),
				AgentID: "a1",
			},
		}
	}

	noSuppress := false

	// Default config (suppressForSubagents defaults to true): no delivery.
	handler, mockNotif, _ := newCodexTestHandler(t, makeDecoded(uniqueCodexSession(t)))
	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}
	if mockNotif.wasCalled() {
		t.Fatal("SubagentStop must stay silent by default")
	}

	// Suppression off but opt-in flag still unset: no delivery.
	handler, mockNotif, _ = newCodexTestHandler(t, makeDecoded(uniqueCodexSession(t)))
	handler.cfg.Notifications.SuppressForSubagents = &noSuppress
	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}
	if mockNotif.wasCalled() {
		t.Fatal("SubagentStop must stay silent without the opt-in flag")
	}

	// Opted in (suppression off + flag on): delivery with the subagent's message.
	handler, mockNotif, _ = newCodexTestHandler(t, makeDecoded(uniqueCodexSession(t)))
	handler.cfg.Notifications.SuppressForSubagents = &noSuppress
	handler.cfg.Notifications.NotifyOnSubagentStop = true
	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}
	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("expected notification with notifyOnSubagentStop enabled")
	}
	if call.status != analyzer.StatusTaskComplete {
		t.Errorf("status = %v, want task_complete", call.status)
	}
	if !strings.Contains(call.message, "Subagent finished the migration.") {
		t.Errorf("message %q must contain the subagent message", call.message)
	}

	// Global suppression wins over the opt-in.
	handler, mockNotif, _ = newCodexTestHandler(t, makeDecoded(uniqueCodexSession(t)))
	handler.cfg.Notifications.NotifyOnSubagentStop = true
	suppress := true
	handler.cfg.Notifications.SuppressForSubagents = &suppress
	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}
	if mockNotif.wasCalled() {
		t.Fatal("suppressForSubagents must win over notifyOnSubagentStop")
	}
}

// TestCodexFlowParallelSubagentsDoNotCollapse guards doc 00 §6.1: parallel
// subagents finishing with an identical final message must both notify.
func TestCodexFlowParallelSubagentsDoNotCollapse(t *testing.T) {
	session := uniqueCodexSession(t)
	noSuppress := false
	makeDecoded := func(agentID string) codexsource.Decoded {
		return codexsource.Decoded{
			SubagentStop: &codexsource.SubagentStopData{
				Stop:    *codexStopData(session, "turn-1", "Done.", false),
				AgentID: agentID,
			},
		}
	}

	handler, mockNotif, _ := newCodexTestHandler(t, makeDecoded("agent-a"))
	handler.cfg.Notifications.SuppressForSubagents = &noSuppress
	handler.cfg.Notifications.NotifyOnSubagentStop = true
	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("first HandleHook() error = %v", err)
	}

	handler.source = CodexSource{DecodeFn: stubCodexDecode(makeDecoded("agent-b"))}
	if err := handler.HandleHook("SubagentStop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("second HandleHook() error = %v", err)
	}

	if got := mockNotif.callCount(); got != 2 {
		t.Fatalf("parallel subagents with identical message delivered %d notifications, want 2", got)
	}
}

func TestCodexFlowStopErrorHeuristic(t *testing.T) {
	handler, mockNotif, _ := newCodexTestHandler(t, codexsource.Decoded{
		Stop: codexStopData(uniqueCodexSession(t), "turn-1", "Rate limit reached, please retry later.", false),
	})
	handler.cfg.Statuses["api_error_overloaded"] = handler.cfg.Statuses["task_complete"]

	if err := handler.HandleHook("Stop", strings.NewReader(`{}`)); err != nil {
		t.Fatalf("HandleHook() error = %v", err)
	}
	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("expected notification")
	}
	if call.status != analyzer.StatusAPIErrorOverloaded {
		t.Errorf("status = %v, want api_error_overloaded", call.status)
	}
}
