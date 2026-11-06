package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
)

func TestClaudeNotificationPermissionPromptUsesHostMessage(t *testing.T) {
	for _, tc := range []struct {
		name, notificationType, prompt string
		wantStatus                     analyzer.Status
		wantBody                       string
	}{
		{"permission prompt", "permission_prompt", "Dangerous rm operation on statically-unresolvable target. Do you want to proceed?", analyzer.StatusPermissionRequest, "Dangerous rm operation on statically-unresolvable target. Do you want to proceed?"},
		{"non-permission notification", "idle_prompt", "Background event", analyzer.StatusQuestion, "Question"},
		{"empty permission prompt", "permission_prompt", "", analyzer.StatusPermissionRequest, "Permission Request"},
		{"long permission prompt", "permission_prompt", strings.Repeat("x", 240), analyzer.StatusPermissionRequest, strings.Repeat("x", 180) + "..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, notifier, _ := newTestHandler(t, codexTestConfig())
			data, err := json.Marshal(HookData{
				SessionID:        "test-claude-permission-" + strings.ReplaceAll(tc.name, " ", "-"),
				HookEventName:    "Notification",
				NotificationType: tc.notificationType,
				Message:          tc.prompt,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.HandleHook("Notification", strings.NewReader(string(data))); err != nil {
				t.Fatal(err)
			}
			if notifier.callCount() != 1 {
				t.Fatalf("expected one desktop notification, got %d", notifier.callCount())
			}
			got := notifier.lastCall()
			if got.status != tc.wantStatus || !strings.HasSuffix(got.message, tc.wantBody) {
				t.Fatalf("notification = %q (%s), want suffix %q (%s)", got.message, got.status, tc.wantBody, tc.wantStatus)
			}
		})
	}
}
