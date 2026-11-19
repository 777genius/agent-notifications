package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/sessionname"
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
			sessionID := "test-claude-permission-" + strings.ReplaceAll(tc.name, " ", "-")
			expectedPrefix := "[" + sessionname.GenerateSessionLabel(sessionID) + " .] "
			data, err := json.Marshal(HookData{
				SessionID:        sessionID,
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
			if got.status != tc.wantStatus || !strings.HasPrefix(got.message, expectedPrefix) ||
				strings.TrimPrefix(got.message, expectedPrefix) != tc.wantBody {
				t.Fatalf("notification = %q (%s), want prefix %q and body %q (%s)",
					got.message, got.status, expectedPrefix, tc.wantBody, tc.wantStatus)
			}
		})
	}
}

// Claude fires PreToolUse for AskUserQuestion/ExitPlanMode and then a
// permission_prompt Notification for the same dialog (#662). Only the first
// may notify; unrelated permission prompts still do.
func TestClaudeInteractiveToolPermissionPromptPairing(t *testing.T) {
	cfg := codexTestConfig()
	cfg.Statuses["plan_ready"] = config.StatusInfo{Title: "Plan Ready"}

	send := func(t *testing.T, h *Handler, hookEvent string, data HookData) {
		t.Helper()
		data.HookEventName = hookEvent
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.HandleHook(hookEvent, strings.NewReader(string(raw))); err != nil {
			t.Fatal(err)
		}
	}
	permission := HookData{NotificationType: "permission_prompt", Message: "Claude needs your permission"}

	for _, tc := range []struct {
		name, tool string
		wantStatus analyzer.Status
	}{
		{"question", "AskUserQuestion", analyzer.StatusQuestion},
		{"plan", "ExitPlanMode", analyzer.StatusPlanReady},
	} {
		t.Run("paired "+tc.name+" is suppressed", func(t *testing.T) {
			h, notifier, _ := newTestHandler(t, cfg)
			sessionID := "test-claude-paired-" + tc.name
			send(t, h, "PreToolUse", HookData{SessionID: sessionID, ToolName: tc.tool, CWD: "/test"})
			p := permission
			p.SessionID, p.CWD = sessionID, "/test"
			send(t, h, "Notification", p)
			if notifier.callCount() != 1 || notifier.lastCall().status != tc.wantStatus {
				t.Fatalf("calls=%d last=%v, want one %s notification", notifier.callCount(), notifier.lastCall(), tc.wantStatus)
			}
		})
	}

	t.Run("specific permission prompt after interactive tool still notifies", func(t *testing.T) {
		h, notifier, _ := newTestHandler(t, cfg)
		sessionID := "test-claude-paired-specific"
		send(t, h, "PreToolUse", HookData{SessionID: sessionID, ToolName: "AskUserQuestion", CWD: "/test"})
		send(t, h, "Notification", HookData{SessionID: sessionID, CWD: "/test", NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"})
		if notifier.callCount() != 2 || notifier.lastCall().status != analyzer.StatusPermissionRequest {
			t.Fatalf("calls=%d last=%v, want a permission notification", notifier.callCount(), notifier.lastCall())
		}
	})

	t.Run("pairing is single-use", func(t *testing.T) {
		h, notifier, _ := newTestHandler(t, cfg)
		sessionID := "test-claude-paired-once"
		send(t, h, "PreToolUse", HookData{SessionID: sessionID, ToolName: "AskUserQuestion", CWD: "/test"})
		p := permission
		p.SessionID, p.CWD = sessionID, "/test"
		send(t, h, "Notification", p)
		if err := h.dedupMgr.ReleaseLock(sessionID, "Notification"); err != nil {
			t.Fatal(err)
		}
		send(t, h, "Notification", p)
		if notifier.callCount() != 2 || notifier.lastCall().status != analyzer.StatusPermissionRequest {
			t.Fatalf("calls=%d last=%v, want question then one permission notification", notifier.callCount(), notifier.lastCall())
		}
	})

	t.Run("stale interactive tool does not suppress", func(t *testing.T) {
		h, notifier, _ := newTestHandler(t, cfg)
		sessionID := "test-claude-paired-stale"
		send(t, h, "PreToolUse", HookData{SessionID: sessionID, ToolName: "AskUserQuestion", CWD: "/test"})
		st, err := h.stateMgr.Load(sessionID)
		if err != nil || st == nil {
			t.Fatalf("load state: %v %v", st, err)
		}
		window := int64(cfg.GetSuppressQuestionAfterAnyNotificationSeconds())
		st.LastTimestamp -= window
		st.LastNotificationTime -= window
		if err := h.stateMgr.Save(st); err != nil {
			t.Fatal(err)
		}
		p := permission
		p.SessionID, p.CWD = sessionID, "/test"
		send(t, h, "Notification", p)
		if notifier.callCount() != 2 || notifier.lastCall().status != analyzer.StatusPermissionRequest {
			t.Fatalf("calls=%d last=%v, want a permission notification", notifier.callCount(), notifier.lastCall())
		}
	})

	t.Run("undelivered interactive tool does not suppress", func(t *testing.T) {
		h, notifier, _ := newTestHandler(t, cfg)
		sessionID := "test-claude-paired-undelivered"
		if err := h.stateMgr.UpdateInteractiveTool(sessionID, "AskUserQuestion", "/test"); err != nil {
			t.Fatal(err)
		}
		p := permission
		p.SessionID, p.CWD = sessionID, "/test"
		send(t, h, "Notification", p)
		if notifier.callCount() != 1 || notifier.lastCall().status != analyzer.StatusPermissionRequest {
			t.Fatalf("calls=%d, want one permission notification", notifier.callCount())
		}
	})
}

func TestIsGenericClaudePermissionPrompt(t *testing.T) {
	for msg, want := range map[string]bool{
		"":                                  true,
		"Claude needs your permission":      true,
		"  claude needs your permission.  ": true,
		"Claude needs your permission to use Bash":                                          false,
		"Dangerous rm operation on statically-unresolvable target. Do you want to proceed?": false,
	} {
		if got := isGenericClaudePermissionPrompt(msg); got != want {
			t.Errorf("isGenericClaudePermissionPrompt(%q) = %v, want %v", msg, got, want)
		}
	}
}
