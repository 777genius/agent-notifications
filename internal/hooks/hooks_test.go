package hooks

import (
	"encoding/json"
	"errors"
	"github.com/777genius/agent-notifications/internal/testenv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/dedup"
	"github.com/777genius/agent-notifications/internal/state"
	"github.com/777genius/agent-notifications/internal/teamstate"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// setTestHome isolates all configuration, metadata and temporary paths.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	testenv.Set(t, dir)
}

// === Mock Notifier ===

type mockNotifier struct {
	mu         sync.Mutex
	calls      []notificationCall
	shouldFail bool
}

type notificationCall struct {
	status  analyzer.Status
	message string
	cwd     string
}

func (m *mockNotifier) SendDesktop(status analyzer.Status, message, sessionID, cwd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, notificationCall{
		status:  status,
		message: message,
		cwd:     cwd,
	})

	if m.shouldFail {
		return errors.New("mock error")
	}
	return nil
}

func (m *mockNotifier) Close() error {
	return nil
}

func (m *mockNotifier) wasCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls) > 0
}

func (m *mockNotifier) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockNotifier) lastCall() *notificationCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return &m.calls[len(m.calls)-1]
}

// === Mock Webhook ===

type mockWebhook struct {
	mu              sync.Mutex
	calls           []webhookCall
	shutdownCalled  bool
	shutdownTimeout time.Duration
}

type webhookCall struct {
	status    analyzer.Status
	message   string
	sessionID string
	cwd       string
}

func (m *mockWebhook) SendAsyncWithContext(sendCtx webhook.SendContext) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, webhookCall{
		status:    sendCtx.Status,
		message:   sendCtx.Message,
		sessionID: sendCtx.SessionID,
		cwd:       sendCtx.CWD,
	})
}

func (m *mockWebhook) Shutdown(timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shutdownCalled = true
	m.shutdownTimeout = timeout
	return nil
}

func (m *mockWebhook) Send(status analyzer.Status, message, sessionID string) error {
	m.SendAsyncWithContext(webhook.SendContext{
		Status:    status,
		Message:   message,
		SessionID: sessionID,
	})
	return nil
}

func (m *mockWebhook) wasCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls) > 0
}

func (m *mockWebhook) wasShutdownCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shutdownCalled
}

func (m *mockWebhook) getShutdownTimeout() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shutdownTimeout
}

// === Test Helpers ===

func buildHookDataJSON(data HookData) io.Reader {
	b, _ := json.Marshal(data)
	return strings.NewReader(string(b))
}

func createTempTranscript(t *testing.T, messages []jsonl.Message) string {
	t.Helper()

	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("failed to create transcript: %v", err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			t.Fatalf("failed to encode message: %v", err)
		}
	}

	return transcriptPath
}

func buildTranscriptWithTools(tools []string, textLength int) []jsonl.Message {
	var content []jsonl.Content

	// Add tools
	for _, tool := range tools {
		content = append(content, jsonl.Content{
			Type: "tool_use",
			Name: tool,
		})
	}

	// Add text
	text := strings.Repeat("a", textLength)
	content = append(content, jsonl.Content{
		Type: "text",
		Text: text,
	})

	return []jsonl.Message{
		{
			Type: "user",
			Message: jsonl.MessageContent{
				Role: "user",
				Content: []jsonl.Content{
					{Type: "text", Text: "Test request"},
				},
			},
			Timestamp: "2025-01-01T12:00:00Z",
		},
		{
			Type: "assistant",
			Message: jsonl.MessageContent{
				Role:    "assistant",
				Content: content,
			},
			Timestamp: "2025-01-01T12:00:01Z",
		},
	}
}

func newTestHandler(t *testing.T, cfg *config.Config) (*Handler, *mockNotifier, *mockWebhook) {
	t.Helper()

	// Clear CLAUDE_HOOK_JUDGE_MODE by default for all tests
	// This ensures tests don't accidentally get affected by judge mode
	// Tests that need judge mode set should call t.Setenv AFTER calling newTestHandler
	t.Setenv("CLAUDE_HOOK_JUDGE_MODE", "")

	// Cleanup state/lock files from previous test runs
	// This prevents duplicate detection issues on fast Go versions (1.25+)
	// where tests run faster than the 180-second duplicate window
	testSessionPatterns := []string{
		"claude-session-state-test-*.json",
		"claude-notification-test-*.lock",
		"claude-content-lock-test-*.lock",
	}
	tempDir := os.TempDir()
	for _, pattern := range testSessionPatterns {
		matches, _ := filepath.Glob(filepath.Join(tempDir, pattern))
		for _, f := range matches {
			_ = os.Remove(f)
		}
	}

	mockNotif := &mockNotifier{}
	mockWH := &mockWebhook{}

	handler := &Handler{
		cfg:          cfg,
		dedupMgr:     dedup.NewManager(),
		stateMgr:     state.NewManager(),
		teamStateMgr: teamstate.NewManager(""),
		notifierSvc:  mockNotif,
		webhookSvc:   mockWH,
		pluginRoot:   t.TempDir(),
	}

	return handler, mockNotif, mockWH
}

// === Integration Tests ===

func TestHandler_PreToolUse_ExitPlanMode(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {Title: "Plan Ready"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-session-1",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification to be sent")
	}

	call := mockNotif.lastCall()
	if call == nil {
		t.Fatal("no notification sent")
	}

	if call.status != analyzer.StatusPlanReady {
		t.Errorf("got status %v, want StatusPlanReady", call.status)
	}
}

func TestHandler_PreToolUse_AskUserQuestion(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"question": {Title: "Question"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-session-2",
		ToolName:  "AskUserQuestion",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification to be sent")
	}

	call := mockNotif.lastCall()
	if call.status != analyzer.StatusQuestion {
		t.Errorf("got status %v, want StatusQuestion", call.status)
	}
}

func TestHandler_Stop_ReviewComplete(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"review_complete": {Title: "Review Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript with Read tools + long text
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Read", "Read", "Grep"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-3",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification to be sent")
	}

	call := mockNotif.lastCall()
	if call.status != analyzer.StatusReviewComplete {
		t.Errorf("got status %v, want StatusReviewComplete", call.status)
	}
}

func TestHandler_Stop_TaskComplete(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript with active tools (Write/Edit)
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Read", "Edit", "Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-4",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification to be sent")
	}

	call := mockNotif.lastCall()
	if call.status != analyzer.StatusTaskComplete {
		t.Errorf("got status %v, want StatusTaskComplete", call.status)
	}
}

func TestHandler_Notification_SuppressedAfterExitPlanMode(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			SuppressQuestionAfterAnyNotificationSeconds: intPtr(60), // 60s suppression window
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {Title: "Plan Ready"},
			"question":   {Title: "Question"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// 1. Send PreToolUse ExitPlanMode (writes session state)
	hookData1 := buildHookDataJSON(HookData{
		SessionID: "test-session-5",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData1)
	if err != nil {
		t.Fatalf("PreToolUse error: %v", err)
	}

	initialCalls := mockNotif.callCount()

	// 2. Send Notification hook within 60s (should be suppressed - same session!)
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"ExitPlanMode"}, 300))

	hookData2 := buildHookDataJSON(HookData{
		SessionID:      "test-session-5", // Same session ID
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	time.Sleep(100 * time.Millisecond) // Small delay

	err = handler.HandleHook("Notification", hookData2)
	if err != nil {
		t.Fatalf("Notification error: %v", err)
	}

	// Should not send duplicate notification
	if mockNotif.callCount() > initialCalls {
		t.Error("Notification should be suppressed after recent ExitPlanMode")
	}
}

// === Deduplication Tests ===

func TestHandler_EarlyDuplicateCheck(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "same-session",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	// First call
	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	firstCallCount := mockNotif.callCount()

	// Immediate second call (< 2s) should be suppressed by early duplicate check
	time.Sleep(50 * time.Millisecond)

	hookData2 := buildHookDataJSON(HookData{
		SessionID:      "same-session",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err = handler.HandleHook("Stop", hookData2)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	// Should not send duplicate
	if mockNotif.callCount() > firstCallCount {
		t.Error("Duplicate hook should be suppressed by early check")
	}
}

// === Cooldown Tests ===

func TestHandler_QuestionCooldownAfterTaskComplete(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:                                  config.DesktopConfig{Enabled: true},
			SuppressQuestionAfterTaskCompleteSeconds: intPtr(3),
			SuppressQuestionAfterAnyNotificationSeconds: intPtr(0), // Disable "any notification" cooldown for this test
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
			"question":      {Title: "Question"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// 1. Send task_complete
	transcriptTask := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData1 := buildHookDataJSON(HookData{
		SessionID:      "test-cooldown-1",
		TranscriptPath: transcriptTask,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData1)
	if err != nil {
		t.Fatalf("task_complete error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Fatal("task_complete notification should be sent")
	}

	taskCallCount := mockNotif.callCount()

	// 2. Send question within cooldown (3s) - should be suppressed (same session!)
	// Wait to ensure state file is fully written and flushed
	time.Sleep(200 * time.Millisecond)

	hookData2 := buildHookDataJSON(HookData{
		SessionID: "test-cooldown-1", // Same session ID
		CWD:       "/test",
	})

	err = handler.HandleHook("Notification", hookData2)
	if err != nil {
		t.Fatalf("notification error: %v", err)
	}

	// Should be suppressed
	if mockNotif.callCount() > taskCallCount {
		t.Errorf("Question should be suppressed within cooldown window, got %d calls, expected %d",
			mockNotif.callCount(), taskCallCount)
	}

	// 3. Wait for cooldown to expire (3s total from task_complete)
	time.Sleep(3 * time.Second)

	// Use same session ID - cooldown should have expired now
	hookData3 := buildHookDataJSON(HookData{
		SessionID: "test-cooldown-1", // Same session - cooldown expired
		CWD:       "/test",
	})

	err = handler.HandleHook("Notification", hookData3)
	if err != nil {
		t.Fatalf("notification after cooldown error: %v", err)
	}

	// Should go through after cooldown expires
	if mockNotif.callCount() <= taskCallCount {
		t.Errorf("Question should be sent after cooldown expires, got %d calls, expected > %d",
			mockNotif.callCount(), taskCallCount)
	}
}

// === Error Handling Tests ===

func TestHandler_InvalidJSON(t *testing.T) {
	cfg := &config.Config{}
	handler, _, _ := newTestHandler(t, cfg)

	err := handler.HandleHook("Stop", strings.NewReader("invalid json"))

	if err == nil {
		t.Error("expected error for invalid JSON")
	}

	if !strings.Contains(err.Error(), "failed to parse hook data") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestHandler_ParsesJSONWithUTF8BOM(t *testing.T) {
	cfg := &config.Config{}
	handler, _, _ := newTestHandler(t, cfg)

	input := "\xef\xbb\xbf" + `{"session_id":"bom-session","transcript_path":"","cwd":""}`
	if err := handler.HandleHook("Stop", strings.NewReader(input)); err != nil {
		t.Fatalf("expected UTF-8 BOM-prefixed JSON to parse, got %v", err)
	}
}

func TestHandler_MissingTranscriptFile(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, _, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-9",
		TranscriptPath: "/nonexistent/path.jsonl",
		CWD:            "/test",
	})

	// Should handle gracefully (degrades, not fails)
	err := handler.HandleHook("Stop", hookData)

	if err != nil {
		t.Errorf("should handle missing file gracefully, got error: %v", err)
	}

	// May still send notification with default message
	// (depends on implementation - this is graceful degradation)
}

func TestHandler_EmptySessionID(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {Title: "Plan Ready"},
		},
	}

	handler, _, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "", // Empty
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	// Should handle gracefully (uses "unknown")
	err := handler.HandleHook("PreToolUse", hookData)

	if err != nil {
		t.Errorf("should handle empty session ID gracefully, got error: %v", err)
	}
}

// === Notification Disabled Tests ===

func TestHandler_NotificationsDisabled(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: false},
			Webhook: config.WebhookConfig{Enabled: false},
		},
	}

	handler, mockNotif, mockWH := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-session-10",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should exit early without sending
	if mockNotif.wasCalled() {
		t.Error("should not send notification when disabled")
	}

	if mockWH.wasCalled() {
		t.Error("should not send webhook when disabled")
	}
}

// === SubagentStop Tests ===

func TestHandler_SubagentStop_DisabledByDefault(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:              config.DesktopConfig{Enabled: true},
			NotifyOnSubagentStop: false, // Default: no notifications for subagents
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-11",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("SubagentStop", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT send notification when disabled
	if mockNotif.wasCalled() {
		t.Error("expected NO notification for SubagentStop (disabled by default)")
	}
}

func TestHandler_SubagentStop_EnabledInConfig(t *testing.T) {
	// To actually receive subagent notifications a user must both opt in
	// (notifyOnSubagentStop) AND disable the suppress-by-default safety net
	// (suppressForSubagents), since the latter overrides the opt-in.
	suppressForSubagents := false
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:              config.DesktopConfig{Enabled: true},
			NotifyOnSubagentStop: true,                  // Explicitly enabled
			SuppressForSubagents: &suppressForSubagents, // Safety net off
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Real-world payload: Claude Code passes the PARENT session transcript path
	// to the SubagentStop hook, not a .../subagents/... path.
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-12",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("SubagentStop", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should send notification when opted in and suppression is disabled
	if !mockNotif.wasCalled() {
		t.Error("expected notification for SubagentStop (opted in, suppression disabled)")
	}
}

// TestHandler_SubagentStop_SuppressForSubagentsOverridesOptIn verifies that the
// suppress-by-default safety net wins even when notifyOnSubagentStop is enabled,
// using the real-world payload (parent transcript path, NOT a /subagents/ path).
// This is the behavior the path-based check could not deliver, because Claude
// Code never passes the subagent's own transcript path to the SubagentStop hook.
func TestHandler_SubagentStop_SuppressForSubagentsOverridesOptIn(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:              config.DesktopConfig{Enabled: true},
			NotifyOnSubagentStop: true, // Opted in...
			// SuppressForSubagents: nil = default true (overrides the opt-in)
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-suppress-overrides",
		TranscriptPath: transcriptPath, // parent path, no /subagents/ segment
		CWD:            "/test",
	})

	err := handler.HandleHook("SubagentStop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT notify: suppressForSubagents (default true) overrides the opt-in.
	if mockNotif.wasCalled() {
		t.Error("expected NO notification: suppressForSubagents (default) overrides notifyOnSubagentStop")
	}
}

// === SuppressForSubagents Tests ===

func TestHandler_Stop_SuppressedForSubagentTranscript(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			// SuppressForSubagents: nil = default true
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript in a subagents directory
	tmpDir := t.TempDir()
	subagentDir := filepath.Join(tmpDir, "projects", "subagents", "agent-123")
	if err := os.MkdirAll(subagentDir, 0755); err != nil {
		t.Fatalf("failed to create subagent dir: %v", err)
	}

	transcriptPath := filepath.Join(subagentDir, "transcript.jsonl")
	messages := buildTranscriptWithTools([]string{"Write"}, 300)
	data, _ := json.Marshal(messages[0])
	data2, _ := json.Marshal(messages[1])
	if err := os.WriteFile(transcriptPath, append(append(data, '\n'), append(data2, '\n')...), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-suppress-subagent-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT send notification (subagent path detected, suppress by default)
	if mockNotif.wasCalled() {
		t.Error("expected NO notification for subagent transcript in Stop hook (suppressForSubagents default true)")
	}
}

func TestHandler_Stop_NotSuppressedWhenSuppressForSubagentsDisabled(t *testing.T) {
	suppressForSubagents := false
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:              config.DesktopConfig{Enabled: true},
			SuppressForSubagents: &suppressForSubagents,
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript in a subagents directory
	tmpDir := t.TempDir()
	subagentDir := filepath.Join(tmpDir, "projects", "subagents", "agent-456")
	if err := os.MkdirAll(subagentDir, 0755); err != nil {
		t.Fatalf("failed to create subagent dir: %v", err)
	}

	transcriptPath := filepath.Join(subagentDir, "transcript.jsonl")
	messages := buildTranscriptWithTools([]string{"Write"}, 300)
	data, _ := json.Marshal(messages[0])
	data2, _ := json.Marshal(messages[1])
	if err := os.WriteFile(transcriptPath, append(append(data, '\n'), append(data2, '\n')...), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-suppress-subagent-disabled-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should send notification (suppressForSubagents disabled)
	if !mockNotif.wasCalled() {
		t.Error("expected notification when suppressForSubagents is false")
	}
}

func TestHandler_Stop_NotSuppressedForNonSubagentTranscript(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			// SuppressForSubagents: nil = default true
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript in a normal (non-subagent) directory
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-suppress-normal-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should send notification (not a subagent path)
	if !mockNotif.wasCalled() {
		t.Error("expected notification for non-subagent transcript")
	}
}

func TestHandler_SubagentStop_SuppressedForSubagentTranscript(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:              config.DesktopConfig{Enabled: true},
			NotifyOnSubagentStop: true, // Enable SubagentStop notifications
			// SuppressForSubagents: nil = default true (suppress by path)
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript in a subagents directory
	tmpDir := t.TempDir()
	subagentDir := filepath.Join(tmpDir, "projects", "subagents", "agent-789")
	if err := os.MkdirAll(subagentDir, 0755); err != nil {
		t.Fatalf("failed to create subagent dir: %v", err)
	}

	transcriptPath := filepath.Join(subagentDir, "transcript.jsonl")
	messages := buildTranscriptWithTools([]string{"Write"}, 300)
	data, _ := json.Marshal(messages[0])
	data2, _ := json.Marshal(messages[1])
	if err := os.WriteFile(transcriptPath, append(append(data, '\n'), append(data2, '\n')...), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-suppress-subagent-stop-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("SubagentStop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT send notification even with notifyOnSubagentStop=true:
	// suppressForSubagents (default true) suppresses every SubagentStop event,
	// independent of the transcript path.
	if mockNotif.wasCalled() {
		t.Error("expected NO notification for SubagentStop (suppressForSubagents default true)")
	}
}

func TestIsSubagentTranscript(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "subagent path unix",
			path:     "/home/user/.claude/projects/foo/subagents/agent-123/transcript.jsonl",
			expected: true,
		},
		{
			name:     "subagent path macOS",
			path:     "/Users/dev/.claude/projects/bar/subagents/abc/transcript.jsonl",
			expected: true,
		},
		{
			name:     "normal transcript path",
			path:     "/home/user/.claude/projects/foo/transcript.jsonl",
			expected: false,
		},
		{
			name:     "empty path",
			path:     "",
			expected: false,
		},
		{
			name:     "subagents in filename only",
			path:     "/home/user/subagents-transcript.jsonl",
			expected: false,
		},
		{
			name:     "subagents as directory segment",
			path:     "/tmp/claude/subagents/nested/deep/transcript.jsonl",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSubagentTranscript(tt.path)
			if result != tt.expected {
				t.Errorf("isSubagentTranscript(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

// === Unknown Hook Event ===

func TestHandler_UnknownHookEvent(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}
	handler, _, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-session-12",
		CWD:       "/test",
	})

	err := handler.HandleHook("UnknownEvent", hookData)

	if err == nil {
		t.Fatal("expected error for unknown hook event")
	}

	if !strings.Contains(err.Error(), "unknown hook event") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// === Webhook Integration ===

func TestHandler_SendsWebhookWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			Webhook: config.WebhookConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, _, mockWH := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-session-13",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // Webhook is async

	if !mockWH.wasCalled() {
		t.Error("expected webhook to be called when enabled")
	}
}

// === NewHandler Constructor Tests ===

func TestNewHandler_Success(t *testing.T) {
	setTestHome(t, t.TempDir()) // isolate stable config path

	// Create temp plugin root with valid config
	tmpDir := t.TempDir()

	// Create config directory and file (expected path: pluginRoot/config/config.json)
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.json")
	configJSON := `{
		"notifications": {
			"desktop": {"enabled": true, "sound": true},
			"webhook": {"enabled": false}
		},
		"statuses": {
			"task_complete": {"title": "Task Complete"}
		}
	}`

	err = os.WriteFile(configPath, []byte(configJSON), 0644)
	if err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create handler
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", configPath)
	handler, err := NewHandler(tmpDir)

	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	if handler == nil {
		t.Fatal("handler is nil")
	}

	// Verify handler components
	if handler.cfg == nil {
		t.Error("handler.cfg is nil")
	}
	if handler.dedupMgr == nil {
		t.Error("handler.dedupMgr is nil")
	}
	if handler.stateMgr == nil {
		t.Error("handler.stateMgr is nil")
	}
	if handler.notifierSvc == nil {
		t.Error("handler.notifierSvc is nil")
	}
	if handler.webhookSvc == nil {
		t.Error("handler.webhookSvc is nil")
	}
	if handler.pluginRoot != tmpDir {
		t.Errorf("handler.pluginRoot = %s, want %s", handler.pluginRoot, tmpDir)
	}

	// Verify the test config was actually loaded (not just defaults)
	info, exists := handler.cfg.GetStatusInfo("task_complete")
	if !exists {
		t.Error("expected task_complete status to exist")
	} else if info.Title != "Task Complete" {
		t.Errorf("expected task_complete title 'Task Complete' from test config, got %q", info.Title)
	}
}

func TestNewHandler_WithDefaultConfig(t *testing.T) {
	setTestHome(t, t.TempDir()) // isolate stable config path

	// Create empty plugin root (no config file)
	tmpDir := t.TempDir()

	// NewHandler should use default config
	handler, err := NewHandler(tmpDir)

	if err != nil {
		t.Fatalf("NewHandler with defaults failed: %v", err)
	}

	if handler == nil {
		t.Fatal("handler is nil")
	}

	// Verify default config was loaded
	if !handler.cfg.IsDesktopEnabled() {
		t.Error("expected desktop notifications enabled by default")
	}
}

func TestNewHandler_InvalidConfig(t *testing.T) {
	setTestHome(t, t.TempDir()) // isolate stable config path

	tmpDir := t.TempDir()

	// Create config directory
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create invalid config (webhook enabled but no URL)
	configPath := filepath.Join(configDir, "config.json")
	configJSON := `{
		"notifications": {
			"webhook": {
				"enabled": true,
				"preset": "slack",
				"url": ""
			}
		}
	}`

	err = os.WriteFile(configPath, []byte(configJSON), 0644)
	if err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// NewHandler should fail validation
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", configPath)
	handler, err := NewHandler(tmpDir)

	if err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}

	if handler != nil {
		t.Error("expected handler to be nil on validation error")
	}

	if !strings.Contains(err.Error(), "ConfigInvalid") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNewHandler_MalformedJSON(t *testing.T) {
	setTestHome(t, t.TempDir()) // isolate stable config path

	tmpDir := t.TempDir()

	// Create config directory
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create malformed JSON config
	configPath := filepath.Join(configDir, "config.json")
	err = os.WriteFile(configPath, []byte("{ invalid json }"), 0644)
	if err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Invalid canonical configuration stops before effect services are created.
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", configPath)
	handler, err := NewHandler(tmpDir)
	if err == nil || handler != nil || !strings.Contains(err.Error(), "ConfigInvalid") {
		t.Fatalf("expected typed config error and no handler: %v", err)
	}
}

func TestNewHandler_NonexistentPluginRoot(t *testing.T) {
	setTestHome(t, t.TempDir()) // isolate stable config path

	// Use nonexistent directory
	nonexistentDir := "/nonexistent/plugin/root/path"

	// NewHandler should still work (config will use defaults)
	handler, err := NewHandler(nonexistentDir)

	if err != nil {
		t.Fatalf("NewHandler with nonexistent root failed: %v", err)
	}

	if handler == nil {
		t.Fatal("handler is nil")
	}

	// Should use default config
	if !handler.cfg.IsDesktopEnabled() {
		t.Error("expected desktop notifications enabled by default")
	}
}

func TestNewHandler_EmptyPluginRoot(t *testing.T) {
	setTestHome(t, t.TempDir()) // isolate stable config path

	// Empty string as plugin root
	handler, err := NewHandler("")

	if err != nil {
		t.Fatalf("NewHandler with empty root failed: %v", err)
	}

	if handler == nil {
		t.Fatal("handler is nil")
	}

	// Should use default config
	if !handler.cfg.IsDesktopEnabled() {
		t.Error("expected desktop notifications enabled by default")
	}
}

// === Cleanup Tests ===

func TestCleanupOldLocks_Success(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, _, _ := newTestHandler(t, cfg)

	// Call cleanupOldLocks - should not panic
	handler.cleanupOldLocks()

	// Verify handler is still functional after cleanup
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-after-cleanup",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("Handler should work after cleanup: %v", err)
	}
}

func TestHandleStopEvent_EmptyTranscriptPath(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, _, _ := newTestHandler(t, cfg)

	// Send Stop hook with empty TranscriptPath
	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-empty-transcript",
		TranscriptPath: "", // Empty
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)

	// Should handle gracefully (no error)
	if err != nil {
		t.Errorf("should handle empty transcript gracefully, got error: %v", err)
	}

	// May or may not send notification (depends on fallback behavior)
	// But should not crash
}

func TestHandleStopEvent_NonexistentTranscriptFile(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, _, _ := newTestHandler(t, cfg)

	// Send Stop hook with nonexistent transcript file
	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-nonexistent-transcript",
		TranscriptPath: "/nonexistent/path/transcript.jsonl",
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)

	// Should handle gracefully (no error, graceful degradation)
	if err != nil {
		t.Errorf("should handle nonexistent transcript gracefully, got error: %v", err)
	}
}

// === Background Judge Mode Tests (double-shot-latte compatibility) ===

// TestHandler_SkipsNotificationsInJudgeMode verifies that notifications are
// suppressed when CLAUDE_HOOK_JUDGE_MODE=true (set by double-shot-latte plugin
// when running background Claude instances for context evaluation)
func TestHandler_SkipsNotificationsInJudgeMode(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			Webhook: config.WebhookConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
			"plan_ready":    {Title: "Plan Ready"},
			"question":      {Title: "Question"},
		},
	}

	handler, mockNotif, mockWH := newTestHandler(t, cfg)

	// Set the judge mode environment variable AFTER creating handler
	// (newTestHandler clears it by default, so we override here)
	t.Setenv("CLAUDE_HOOK_JUDGE_MODE", "true")

	// Test PreToolUse hook
	hookData1 := buildHookDataJSON(HookData{
		SessionID: "test-judge-mode-1",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData1)
	if err != nil {
		t.Fatalf("PreToolUse error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected NO notification in judge mode (PreToolUse)")
	}

	// Test Stop hook
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData2 := buildHookDataJSON(HookData{
		SessionID:      "test-judge-mode-2",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err = handler.HandleHook("Stop", hookData2)
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected NO notification in judge mode (Stop)")
	}

	if mockWH.wasCalled() {
		t.Error("expected NO webhook in judge mode")
	}
}

// TestHandler_SendsNotificationsWhenJudgeModeNotSet verifies that notifications
// work normally when CLAUDE_HOOK_JUDGE_MODE is not set
func TestHandler_SendsNotificationsWhenJudgeModeNotSet(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {Title: "Plan Ready"},
		},
	}

	// newTestHandler already clears CLAUDE_HOOK_JUDGE_MODE by default
	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-no-judge-mode",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)
	if err != nil {
		t.Fatalf("PreToolUse error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification when judge mode is NOT set")
	}
}

// TestHandler_SendsNotificationsWhenJudgeModeFalse verifies that notifications
// work normally when CLAUDE_HOOK_JUDGE_MODE is set to something other than "true"
func TestHandler_SendsNotificationsWhenJudgeModeFalse(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {Title: "Plan Ready"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Set env var to "false" AFTER handler creation - should NOT suppress notifications
	t.Setenv("CLAUDE_HOOK_JUDGE_MODE", "false")

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-judge-mode-false",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)
	if err != nil {
		t.Fatalf("PreToolUse error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification when judge mode is 'false'")
	}
}

// TestHandler_IgnoresJudgeModeWhenRespectJudgeModeFalse verifies that notifications
// are sent even when CLAUDE_HOOK_JUDGE_MODE=true if respectJudgeMode is false
func TestHandler_IgnoresJudgeModeWhenRespectJudgeModeFalse(t *testing.T) {
	respectJudgeMode := false
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:          config.DesktopConfig{Enabled: true},
			RespectJudgeMode: &respectJudgeMode,
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {Title: "Plan Ready"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Set judge mode AFTER handler creation
	t.Setenv("CLAUDE_HOOK_JUDGE_MODE", "true")

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-ignore-judge-mode",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)
	if err != nil {
		t.Fatalf("PreToolUse error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification when respectJudgeMode is false, even with CLAUDE_HOOK_JUDGE_MODE=true")
	}
}

// TestHandleHookCallsWebhookShutdown verifies that HandleHook calls
// webhookSvc.Shutdown() in defer to ensure graceful shutdown of async requests
func TestHandleHookCallsWebhookShutdown(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: false},
			Webhook: config.WebhookConfig{Enabled: true},
		},
	}

	handler, _, mockWH := newTestHandler(t, cfg)

	// Create transcript file with Stop event
	transcript := []jsonl.Message{
		{
			Type: "assistant",
			Message: jsonl.MessageContent{
				Role: "assistant",
				Content: []jsonl.Content{
					{Type: "text", Text: "Task completed"},
				},
			},
			Timestamp: "2025-01-01T12:00:00Z",
		},
	}
	transcriptFile := createTempTranscript(t, transcript)

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-shutdown-session",
		TranscriptPath: transcriptFile,
		CWD:            "/test",
	})

	// Call HandleHook - this should call Shutdown() in defer
	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("HandleHook failed: %v", err)
	}

	// Verify that Shutdown was called
	if !mockWH.wasShutdownCalled() {
		t.Error("expected webhookSvc.Shutdown() to be called in defer, but it wasn't")
	}

	// Verify that Shutdown was called with correct timeout (5 seconds)
	expectedTimeout := 5 * time.Second
	actualTimeout := mockWH.getShutdownTimeout()
	if actualTimeout != expectedTimeout {
		t.Errorf("expected Shutdown timeout %v, got %v", expectedTimeout, actualTimeout)
	}
}

// === Per-Status Enabled Tests ===

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(v int) *int {
	return &v
}

// TestHandler_StatusDisabled_SkipsDesktopNotification verifies that when a status
// is disabled in config, desktop notifications are not sent for that status
func TestHandler_StatusDisabled_SkipsDesktopNotification(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				Enabled: boolPtr(false), // Disabled!
				Title:   "Task Complete",
			},
			"review_complete": {
				// Enabled: nil means enabled by default
				Title: "Review Complete",
			},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Create transcript with active tools (Write/Edit) - should trigger task_complete
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-status-disabled-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT send notification because task_complete is disabled
	if mockNotif.wasCalled() {
		t.Error("expected NO notification for disabled status (task_complete)")
	}
}

// TestHandler_StatusEnabled_SendsDesktopNotification verifies that when a status
// is explicitly enabled, desktop notifications are sent
func TestHandler_StatusEnabled_SendsDesktopNotification(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				Enabled: boolPtr(true), // Explicitly enabled
				Title:   "Task Complete",
			},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-status-enabled-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should send notification
	if !mockNotif.wasCalled() {
		t.Error("expected notification for enabled status (task_complete)")
	}
}

// TestHandler_StatusDisabled_SkipsWebhookNotification verifies that when a status
// is disabled, webhook notifications are also not sent for that status
func TestHandler_StatusDisabled_SkipsWebhookNotification(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			Webhook: config.WebhookConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				Enabled: boolPtr(false), // Disabled!
				Desktop: &config.StatusChannelConfig{Enabled: boolPtr(true)},
				Webhook: &config.StatusChannelConfig{Enabled: boolPtr(true)},
				Title:   "Task Complete",
			},
		},
	}

	handler, mockNotif, mockWH := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-status-disabled-webhook-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT send desktop notification
	if mockNotif.wasCalled() {
		t.Error("expected NO desktop notification for disabled status")
	}

	// Should NOT send webhook notification
	if mockWH.wasCalled() {
		t.Error("expected NO webhook notification for disabled status")
	}
}

func TestHandler_StatusDesktopOverride_SkipsDesktopButSendsWebhook(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			Webhook: config.WebhookConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				Desktop: &config.StatusChannelConfig{Enabled: boolPtr(false)},
				Webhook: &config.StatusChannelConfig{Enabled: boolPtr(true)},
				Title:   "Task Complete",
			},
		},
	}

	handler, mockNotif, mockWH := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-desktop-override-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected NO desktop notification when desktop override is disabled")
	}
	if !mockWH.wasCalled() {
		t.Error("expected webhook notification when webhook override is enabled")
	}
}

func TestHandler_StatusWebhookOverride_SkipsWebhookButSendsDesktop(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			Webhook: config.WebhookConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				Desktop: &config.StatusChannelConfig{Enabled: boolPtr(true)},
				Webhook: &config.StatusChannelConfig{Enabled: boolPtr(false)},
				Title:   "Task Complete",
			},
		},
	}

	handler, mockNotif, mockWH := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-webhook-override-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected desktop notification when desktop override is enabled")
	}
	if mockWH.wasCalled() {
		t.Error("expected NO webhook notification when webhook override is disabled")
	}
}

// TestHandler_StatusNilEnabled_SendsNotification verifies backward compatibility:
// when enabled field is nil (not specified), notifications should be sent
func TestHandler_StatusNilEnabled_SendsNotification(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				// Enabled: nil - not specified, should default to true
				Title: "Task Complete",
			},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-status-nil-enabled-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should send notification (nil = enabled by default)
	if !mockNotif.wasCalled() {
		t.Error("expected notification when enabled is nil (backward compatibility)")
	}
}

// TestHandler_PreToolUse_StatusDisabled verifies that PreToolUse hooks respect
// per-status enabled setting
func TestHandler_PreToolUse_StatusDisabled(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"plan_ready": {
				Enabled: boolPtr(false), // Disabled!
				Title:   "Plan Ready",
			},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID: "test-pretool-disabled-1",
		ToolName:  "ExitPlanMode",
		CWD:       "/test",
	})

	err := handler.HandleHook("PreToolUse", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT send notification because plan_ready is disabled
	if mockNotif.wasCalled() {
		t.Error("expected NO notification for disabled status (plan_ready)")
	}
}

// TestHandler_MixedStatusEnabled verifies that different statuses can have
// different enabled settings
func TestHandler_MixedStatusEnabled(t *testing.T) {
	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {
				Enabled: boolPtr(false), // Disabled
				Title:   "Task Complete",
			},
			"review_complete": {
				Enabled: boolPtr(true), // Enabled
				Title:   "Review Complete",
			},
			"question": {
				// nil - default enabled
				Title: "Question",
			},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Test 1: task_complete (disabled) - should NOT notify
	transcriptTask := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData1 := buildHookDataJSON(HookData{
		SessionID:      "test-mixed-1",
		TranscriptPath: transcriptTask,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData1)
	if err != nil {
		t.Fatalf("task_complete error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected NO notification for disabled task_complete")
	}

	// Test 2: review_complete (enabled) - should notify
	transcriptReview := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Read", "Grep"}, 300))

	hookData2 := buildHookDataJSON(HookData{
		SessionID:      "test-mixed-2",
		TranscriptPath: transcriptReview,
		CWD:            "/test",
	})

	err = handler.HandleHook("Stop", hookData2)
	if err != nil {
		t.Fatalf("review_complete error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification for enabled review_complete")
	}
}

// === Tests for suppress-filters ===

func TestHandler_SuppressFilter_MatchingFilter_SkipsNotification(t *testing.T) {
	folderStatus := "task_complete"
	folderName := "ClaudeProbe"
	emptyBranch := ""

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			Webhook: config.WebhookConfig{Enabled: true, URL: "http://localhost/test"},
			SuppressFilters: []config.SuppressFilter{
				{
					Name:      "Suppress ClaudeProbe completions",
					Status:    &folderStatus,
					GitBranch: &emptyBranch,
					Folder:    &folderName,
				},
			},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, mockWH := newTestHandler(t, cfg)

	// CWD ending with "ClaudeProbe" → folder = "ClaudeProbe"
	// Git branch will be "" because the temp dir is not a git repo
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-filter-match-1",
		TranscriptPath: transcriptPath,
		CWD:            filepath.Join(t.TempDir(), "ClaudeProbe"),
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both desktop and webhook should be suppressed
	if mockNotif.wasCalled() {
		t.Error("expected NO desktop notification when suppress-filter matches")
	}
	if mockWH.wasCalled() {
		t.Error("expected NO webhook notification when suppress-filter matches")
	}
}

func TestHandler_SuppressFilter_NonMatchingFilter_SendsNotification(t *testing.T) {
	folderStatus := "task_complete"
	emptyBranch := ""
	folderName := "ClaudeProbe"

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{Enabled: true},
			SuppressFilters: []config.SuppressFilter{
				{
					Status:    &folderStatus,
					GitBranch: &emptyBranch,
					Folder:    &folderName,
				},
			},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// CWD is "my-project" — does NOT match filter folder "ClaudeProbe"
	transcriptPath := createTempTranscript(t,
		buildTranscriptWithTools([]string{"Write"}, 300))

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-filter-nomatch-1",
		TranscriptPath: transcriptPath,
		CWD:            filepath.Join(t.TempDir(), "my-project"),
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still send because filter doesn't match
	if !mockNotif.wasCalled() {
		t.Error("expected notification when suppress-filter does not match")
	}
}

// === Team Mode Tests ===

// setupTeamConfig creates a team config in the temp dir and returns the HOME dir path.
// Teams are created at HOME/.claude/teams/ to match the real layout.
func setupTeamConfig(t *testing.T, teamName, leadSessionID string, memberNames []string) string {
	t.Helper()

	tmpHome := t.TempDir()
	teamsDir := filepath.Join(tmpHome, ".claude", "teams", teamName)
	if err := os.MkdirAll(teamsDir, 0755); err != nil {
		t.Fatal(err)
	}

	type member struct {
		AgentID   string `json:"agentId"`
		Name      string `json:"name"`
		AgentType string `json:"agentType"`
	}
	type teamCfg struct {
		Name          string   `json:"name"`
		LeadSessionID string   `json:"leadSessionId"`
		Members       []member `json:"members"`
	}

	members := []member{
		{AgentID: "team-lead@" + teamName, Name: "team-lead", AgentType: "team-lead"},
	}
	for _, n := range memberNames {
		members = append(members, member{
			AgentID:   n + "@" + teamName,
			Name:      n,
			AgentType: "general-purpose",
		})
	}

	cfg := teamCfg{
		Name:          teamName,
		LeadSessionID: leadSessionID,
		Members:       members,
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(teamsDir, "config.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	return tmpHome
}

func TestHandler_Stop_TeamMode_Smart_SuppressesLeadStop(t *testing.T) {
	sessionID := "test-team-lead-session"
	claudeDir := setupTeamConfig(t, "test-smart-team", sessionID, []string{"alice", "bob"})
	setTestHome(t, claudeDir)

	messages := buildTranscriptWithTools([]string{"Write"}, 50)
	transcriptPath := createTempTranscript(t, messages)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "wait-all",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected notification to be suppressed for team lead in smart mode")
	}
}

func TestHandler_Stop_TeamMode_Always_DoesNotSuppress(t *testing.T) {
	sessionID := "test-team-always-session"
	claudeDir := setupTeamConfig(t, "test-always-team", sessionID, []string{"alice"})
	setTestHome(t, claudeDir)

	messages := buildTranscriptWithTools([]string{"Write"}, 50)
	transcriptPath := createTempTranscript(t, messages)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "always",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification in 'always' team mode")
	}
}

func TestHandler_Stop_TeamMode_Never_Suppresses(t *testing.T) {
	sessionID := "test-team-never-session"
	claudeDir := setupTeamConfig(t, "test-never-team", sessionID, []string{"alice"})
	setTestHome(t, claudeDir)

	messages := buildTranscriptWithTools([]string{"Write"}, 50)
	transcriptPath := createTempTranscript(t, messages)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "never",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected notification to be suppressed in 'never' team mode")
	}
}

func TestHandler_Stop_TeamMode_Smart_NotifiesWhenAllIdle(t *testing.T) {
	sessionID := "test-team-allidle-session"
	teamName := "test-allidle-team"
	claudeDir := setupTeamConfig(t, teamName, sessionID, []string{"alice"})
	setTestHome(t, claudeDir)

	messages := buildTranscriptWithTools([]string{"Write"}, 50)
	transcriptPath := createTempTranscript(t, messages)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "wait-all",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Pre-record alice as idle (simulates TeammateIdle having fired already)
	teamMgr := setupTeamStateManager(t, claudeDir)
	teamMgr.RecordTeammateIdle(teamName, "alice") //nolint:errcheck

	hookData := buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification when lead stops and all teammates are idle")
	}
}

func TestHandler_TeammateIdle_SendsWhenAllReady(t *testing.T) {
	sessionID := "test-ti-session"
	teamName := "test-ti-team"
	claudeDir := setupTeamConfig(t, teamName, sessionID, []string{"alice", "bob"})
	setTestHome(t, claudeDir)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "wait-all",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Pre-record: lead stopped + alice already idle
	teamMgr := setupTeamStateManager(t, claudeDir)
	teamMgr.RecordLeadStopped(teamName)           //nolint:errcheck
	teamMgr.RecordTeammateIdle(teamName, "alice") //nolint:errcheck

	// Now bob goes idle → should trigger notification
	hookData := buildHookDataJSON(HookData{
		SessionID:    sessionID,
		TeamName:     teamName,
		TeammateName: "bob",
		CWD:          "/test",
	})

	err := handler.HandleHook("TeammateIdle", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification when last teammate goes idle and lead has stopped")
	}
}

func TestHandler_TeammateIdle_DoesNotSendWhenLeadNotStopped(t *testing.T) {
	sessionID := "test-ti-nosend-session"
	teamName := "test-ti-nosend-team"
	claudeDir := setupTeamConfig(t, teamName, sessionID, []string{"alice"})
	setTestHome(t, claudeDir)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "wait-all",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	// Lead has NOT stopped — alice goes idle
	hookData := buildHookDataJSON(HookData{
		SessionID:    sessionID,
		TeamName:     teamName,
		TeammateName: "alice",
		CWD:          "/test",
	})

	err := handler.HandleHook("TeammateIdle", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockNotif.wasCalled() {
		t.Error("expected no notification when lead has not stopped")
	}
}

func TestHandler_Stop_NonTeamLead_NormalBehavior(t *testing.T) {
	// A session that is NOT a team lead should get normal notification behavior
	claudeDir := setupTeamConfig(t, "other-team", "different-session-id", []string{"alice"})
	setTestHome(t, claudeDir)

	messages := buildTranscriptWithTools([]string{"Write"}, 50)
	transcriptPath := createTempTranscript(t, messages)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "wait-all",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}

	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID:      "test-nonlead-session-1",
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	})

	err := handler.HandleHook("Stop", hookData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockNotif.wasCalled() {
		t.Error("expected notification for non-team-lead session")
	}
}

// setupTeamStateManager creates a teamstate.Manager pointing to the given home dir's .claude.
func setupTeamStateManager(t *testing.T, homeDir string) *teamstate.Manager {
	t.Helper()
	return teamstate.NewManager(filepath.Join(homeDir, ".claude"))
}
