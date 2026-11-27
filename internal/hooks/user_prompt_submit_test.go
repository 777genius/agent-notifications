package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/platform"
)

type ghosttyCaptureCall struct {
	configOverride, sessionID, cwd string
}

type ghosttyCaptureRecorder struct {
	mu     sync.Mutex
	legacy []ghosttyCaptureCall
	prompt []ghosttyCaptureCall
}

func (r *ghosttyCaptureRecorder) legacyCalls() []ghosttyCaptureCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ghosttyCaptureCall(nil), r.legacy...)
}

func (r *ghosttyCaptureRecorder) promptCalls() []ghosttyCaptureCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ghosttyCaptureCall(nil), r.prompt...)
}

// stubGhosttyCapture swaps both Ghostty capture seams for recorders so handler
// tests never run osascript or write the real session state file.
func stubGhosttyCapture(t *testing.T) *ghosttyCaptureRecorder {
	t.Helper()
	recorder := &ghosttyCaptureRecorder{}
	originalLegacy, originalPrompt := captureGhosttyTerminalID, captureGhosttyTerminalIDOnPrompt
	t.Cleanup(func() {
		captureGhosttyTerminalID, captureGhosttyTerminalIDOnPrompt = originalLegacy, originalPrompt
	})
	captureGhosttyTerminalID = func(configOverride, sessionID, cwd string) {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		recorder.legacy = append(recorder.legacy, ghosttyCaptureCall{configOverride, sessionID, cwd})
	}
	captureGhosttyTerminalIDOnPrompt = func(configOverride, sessionID, cwd string) {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		recorder.prompt = append(recorder.prompt, ghosttyCaptureCall{configOverride, sessionID, cwd})
	}
	return recorder
}

func userPromptSubmitTestConfig(clickToFocus bool) *config.Config {
	return &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop: config.DesktopConfig{
				Enabled:          true,
				ClickToFocus:     clickToFocus,
				TerminalBundleID: "com.mitchellh.ghostty",
			},
			Webhook: config.WebhookConfig{Enabled: true, URL: "https://example.invalid/hook"},
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
			"plan_ready":    {Title: "Plan Ready"},
		},
	}
}

func userPromptSubmitPayload(sessionID, cwd string) string {
	payload, _ := json.Marshal(map[string]string{
		"session_id":      sessionID,
		"cwd":             cwd,
		"transcript_path": "/tmp/does-not-exist.jsonl",
		"hook_event_name": "UserPromptSubmit",
		"permission_mode": "default",
		"prompt":          "secret prompt text",
	})
	return string(payload)
}

func sessionArtifacts(t *testing.T, sessionID string) []string {
	t.Helper()
	var found []string
	for _, pattern := range []string{
		"claude-notification-" + sessionID + "*.lock",
		"claude-content-lock-" + sessionID + "*",
		"claude-session-state-" + sessionID + ".json",
	} {
		matches, err := filepath.Glob(filepath.Join(platform.TempDir(), pattern))
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, matches...)
	}
	return found
}

func removeSessionArtifacts(t *testing.T, sessionID string) {
	t.Helper()
	for _, path := range sessionArtifacts(t, sessionID) {
		_ = os.Remove(path)
	}
}

func TestUserPromptSubmitCapturesGhosttyTerminalWithPromptSemantics(t *testing.T) {
	recorder := stubGhosttyCapture(t)
	handler, notify, webhook := newTestHandler(t, userPromptSubmitTestConfig(true))
	sessionID := "test-ups-capture"
	removeSessionArtifacts(t, sessionID)
	t.Cleanup(func() { removeSessionArtifacts(t, sessionID) })

	if err := handler.HandleHook("UserPromptSubmit", strings.NewReader(userPromptSubmitPayload(sessionID, "/repo/project"))); err != nil {
		t.Fatalf("HandleHook(UserPromptSubmit) error = %v", err)
	}

	want := ghosttyCaptureCall{"com.mitchellh.ghostty", sessionID, "/repo/project"}
	if got := recorder.promptCalls(); len(got) != 1 || got[0] != want {
		t.Fatalf("prompt capture calls = %+v, want [%+v]", got, want)
	}
	if got := recorder.legacyCalls(); len(got) != 0 {
		t.Fatalf("legacy capture calls = %+v, want none", got)
	}
	if notify.callCount() != 0 {
		t.Fatalf("UserPromptSubmit sent %d notification(s)", notify.callCount())
	}
	if webhook.wasCalled() {
		t.Fatal("UserPromptSubmit sent a webhook")
	}
	if found := sessionArtifacts(t, sessionID); len(found) != 0 {
		t.Fatalf("UserPromptSubmit left dedup/state artifacts: %v", found)
	}
}

func TestUserPromptSubmitWithoutClickToFocusDoesNothing(t *testing.T) {
	recorder := stubGhosttyCapture(t)
	handler, notify, webhook := newTestHandler(t, userPromptSubmitTestConfig(false))
	sessionID := "test-ups-no-click"
	removeSessionArtifacts(t, sessionID)
	t.Cleanup(func() { removeSessionArtifacts(t, sessionID) })

	if err := handler.HandleHook("UserPromptSubmit", strings.NewReader(userPromptSubmitPayload(sessionID, "/repo/project"))); err != nil {
		t.Fatalf("HandleHook(UserPromptSubmit) error = %v", err)
	}

	if got := recorder.promptCalls(); len(got) != 0 {
		t.Fatalf("prompt capture calls = %+v, want none", got)
	}
	if notify.callCount() != 0 || webhook.wasCalled() {
		t.Fatalf("UserPromptSubmit delivered: notifications=%d webhook=%v", notify.callCount(), webhook.wasCalled())
	}
	if found := sessionArtifacts(t, sessionID); len(found) != 0 {
		t.Fatalf("UserPromptSubmit left dedup/state artifacts: %v", found)
	}
}

func TestUserPromptSubmitDoesNotSuppressFollowingStop(t *testing.T) {
	setTestHome(t, t.TempDir())
	stubGhosttyCapture(t)
	handler, notify, _ := newTestHandler(t, userPromptSubmitTestConfig(true))
	sessionID := "test-ups-then-stop"
	removeSessionArtifacts(t, sessionID)
	t.Cleanup(func() { removeSessionArtifacts(t, sessionID) })

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user","timestamp":"2026-09-24T12:00:00Z","message":{"role":"user","content":"test"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()

	if err := handler.HandleHook("UserPromptSubmit", strings.NewReader(userPromptSubmitPayload(sessionID, cwd))); err != nil {
		t.Fatalf("HandleHook(UserPromptSubmit) error = %v", err)
	}
	stop, err := json.Marshal(HookData{
		SessionID: sessionID, TranscriptPath: transcript, CWD: cwd,
		HookEventName: "Stop", LastAssistantMessage: "Done.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleHook("Stop", strings.NewReader(string(stop))); err != nil {
		t.Fatalf("HandleHook(Stop) error = %v", err)
	}

	if notify.callCount() != 1 || notify.lastCall().status != analyzer.StatusTaskComplete {
		t.Fatalf("Stop after UserPromptSubmit: count=%d last=%+v, want one task_complete", notify.callCount(), notify.lastCall())
	}
}

func TestLegacyGhosttyCaptureEventsStillUseLegacyPath(t *testing.T) {
	for _, tc := range []struct {
		event string
		data  HookData
	}{
		{event: "PreToolUse", data: HookData{ToolName: "ExitPlanMode", HookEventName: "PreToolUse"}},
		{event: "Notification", data: HookData{NotificationType: "permission_prompt", Message: "Claude needs your permission", HookEventName: "Notification"}},
	} {
		t.Run(tc.event, func(t *testing.T) {
			recorder := stubGhosttyCapture(t)
			handler, _, _ := newTestHandler(t, userPromptSubmitTestConfig(true))
			sessionID := "test-ups-legacy-" + strings.ToLower(tc.event)
			removeSessionArtifacts(t, sessionID)
			t.Cleanup(func() { removeSessionArtifacts(t, sessionID) })

			tc.data.SessionID, tc.data.CWD = sessionID, "/repo/project"
			if err := handler.HandleHook(tc.event, buildHookDataJSON(tc.data)); err != nil {
				t.Fatalf("HandleHook(%s) error = %v", tc.event, err)
			}

			want := ghosttyCaptureCall{"com.mitchellh.ghostty", sessionID, "/repo/project"}
			if got := recorder.legacyCalls(); len(got) != 1 || got[0] != want {
				t.Fatalf("legacy capture calls = %+v, want [%+v]", got, want)
			}
			if got := recorder.promptCalls(); len(got) != 0 {
				t.Fatalf("prompt capture calls = %+v, want none", got)
			}
		})
	}
}

func TestUserPromptSubmitIgnoredForQoder(t *testing.T) {
	recorder := stubGhosttyCapture(t)
	handler, notify, webhook := newTestHandler(t, userPromptSubmitTestConfig(true))
	handler.product, handler.source = ProductQoder, QoderSource{}

	_ = handler.HandleHook("UserPromptSubmit", strings.NewReader(userPromptSubmitPayload("test-ups-qoder", "/repo/project")))

	if len(recorder.promptCalls()) != 0 || len(recorder.legacyCalls()) != 0 {
		t.Fatalf("Qoder UserPromptSubmit reached Ghostty capture: prompt=%+v legacy=%+v", recorder.promptCalls(), recorder.legacyCalls())
	}
	if notify.callCount() != 0 || webhook.wasCalled() {
		t.Fatalf("Qoder UserPromptSubmit delivered: notifications=%d webhook=%v", notify.callCount(), webhook.wasCalled())
	}
}

func TestClaudeSourceDecodeUserPromptSubmit(t *testing.T) {
	ev, err := (ClaudeSource{}).Decode(context.Background(), "UserPromptSubmit", strings.NewReader(userPromptSubmitPayload("s-ups", "/repo/project")))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if _, ok := ev.Payload.(UserPromptSubmitPayload); !ok {
		t.Fatalf("payload = %#v, want UserPromptSubmitPayload", ev.Payload)
	}
	if ev.Kind() != EventUserPromptSubmit {
		t.Fatalf("Kind() = %q, want %q", ev.Kind(), EventUserPromptSubmit)
	}
	if ev.Product != ProductClaude || ev.Session.SessionID != "s-ups" || ev.Session.CWD != "/repo/project" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestValidateEventRejectsUserPromptSubmitForOtherProducts(t *testing.T) {
	for _, product := range []Product{ProductCodex, ProductQoder} {
		ev := Event{Product: product, Session: SessionContext{SessionID: "s"}, Payload: UserPromptSubmitPayload{}}
		if err := ValidateEvent(ev); err == nil {
			t.Fatalf("ValidateEvent accepted UserPromptSubmit for %s", product)
		}
	}
	if err := ValidateEvent(Event{Product: ProductClaude, Session: SessionContext{SessionID: "s"}, Payload: UserPromptSubmitPayload{}}); err != nil {
		t.Fatalf("ValidateEvent rejected Claude UserPromptSubmit: %v", err)
	}
}
