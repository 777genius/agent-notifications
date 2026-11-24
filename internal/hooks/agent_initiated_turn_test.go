package hooks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

// copyTurnOriginFixture copies a testdata transcript into a fresh file so its
// modification time is recent; the Stop handler skips transcripts untouched
// for ten minutes.
func copyTurnOriginFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "turn_origin", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func agentInitiatedTurnConfig(notify *bool) *config.Config {
	return &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:                     config.DesktopConfig{Enabled: true},
			NotifyOnAgentInitiatedTurns: notify,
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete":   {Title: "Completed"},
			"review_complete": {Title: "Review"},
			"question":        {Title: "Question"},
			"api_error":       {Title: "API Error"},
		},
	}
}

func TestClaudeStopAgentInitiatedTurns(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fixture    string
		message    string // last_assistant_message in the Stop payload
		notify     *bool
		wantStatus analyzer.Status // StatusUnknown means no notification
	}{
		{name: "default notifies subagent turn", fixture: "task_notification.jsonl",
			wantStatus: analyzer.StatusTaskComplete},
		{name: "explicit true notifies peer turn", fixture: "peer.jsonl", notify: boolPtr(true),
			wantStatus: analyzer.StatusTaskComplete},
		{name: "human turn notifies", fixture: "human.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusTaskComplete},
		{name: "human turn that loaded a skill notifies", fixture: "human_skill.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusTaskComplete},
		{name: "subagent turn is skipped", fixture: "task_notification.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusUnknown},
		{name: "peer turn is skipped", fixture: "peer.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusUnknown},
		{name: "teammate message without origin is skipped", fixture: "peer_without_origin.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusUnknown},
		{name: "legacy transcript keeps notifying", fixture: "legacy.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusTaskComplete},
		{name: "question in subagent turn still notifies", fixture: "task_notification_question.jsonl", notify: boolPtr(false),
			wantStatus: analyzer.StatusQuestion},
		{name: "text-only subagent turn is skipped", fixture: "task_notification_text_only.jsonl", notify: boolPtr(false),
			message: "Merged the subagent's findings.", wantStatus: analyzer.StatusUnknown},
		{name: "text-only subagent turn notifies by default", fixture: "task_notification_text_only.jsonl",
			message: "Merged the subagent's findings.", wantStatus: analyzer.StatusTaskComplete},
		{name: "API error in subagent turn still notifies", fixture: "task_notification_text_only.jsonl", notify: boolPtr(false),
			message: "API error: 500 Internal Server Error", wantStatus: analyzer.StatusAPIError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setTestHome(t, t.TempDir())
			handler, mockNotif, _ := newTestHandler(t, agentInitiatedTurnConfig(tc.notify))
			hookData := buildHookDataJSON(HookData{
				SessionID:            "test-agent-initiated-" + filepath.Base(t.Name()),
				TranscriptPath:       copyTurnOriginFixture(t, tc.fixture),
				LastAssistantMessage: tc.message,
				CWD:                  t.TempDir(),
			})
			if err := handler.HandleHook("Stop", hookData); err != nil {
				t.Fatalf("HandleHook: %v", err)
			}
			if tc.wantStatus == analyzer.StatusUnknown {
				if mockNotif.wasCalled() {
					t.Fatalf("unexpected notification: %+v", mockNotif.lastCall())
				}
				return
			}
			if mockNotif.callCount() != 1 || mockNotif.lastCall().status != tc.wantStatus {
				t.Fatalf("notification = %+v, count = %d, want status %s",
					mockNotif.lastCall(), mockNotif.callCount(), tc.wantStatus)
			}
		})
	}
}

// In wait-all team mode the lead's last turn is usually started by a
// teammate message, yet the team-done notification it claims must go out.
func TestClaudeStopAgentInitiatedTurns_TeamWaitAllStillNotifies(t *testing.T) {
	sessionID := "test-agent-initiated-team-lead"
	teamName := "test-agent-initiated-team"
	claudeDir := setupTeamConfig(t, teamName, sessionID, []string{"alice"})
	setTestHome(t, claudeDir)

	cfg := agentInitiatedTurnConfig(boolPtr(false))
	cfg.Notifications.TeamMode = "wait-all"
	handler, mockNotif, _ := newTestHandler(t, cfg)

	teamMgr := setupTeamStateManager(t, claudeDir)
	if err := teamMgr.RecordTeammateIdle(teamName, "alice"); err != nil {
		t.Fatal(err)
	}

	hookData := buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: copyTurnOriginFixture(t, "peer.jsonl"),
		CWD:            "/test",
	})
	if err := handler.HandleHook("Stop", hookData); err != nil {
		t.Fatalf("HandleHook: %v", err)
	}
	if mockNotif.callCount() != 1 || mockNotif.lastCall().status != analyzer.StatusTaskComplete {
		t.Fatalf("team-done notification = %+v, count = %d", mockNotif.lastCall(), mockNotif.callCount())
	}
}

// A lead Stop that does not complete the team stays suppressed by wait-all,
// and the option does not change which hook claims completion: the lead's
// stop is still recorded, so the last TeammateIdle delivers the team-done
// notification.
func TestClaudeStopAgentInitiatedTurns_TeamWaitAllLeadStopRecorded(t *testing.T) {
	sessionID := "test-agent-initiated-team-lead-early"
	teamName := "test-agent-initiated-team-early"
	claudeDir := setupTeamConfig(t, teamName, sessionID, []string{"alice"})
	setTestHome(t, claudeDir)

	cfg := agentInitiatedTurnConfig(boolPtr(false))
	cfg.Notifications.TeamMode = "wait-all"
	handler, mockNotif, _ := newTestHandler(t, cfg)

	hookData := buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: copyTurnOriginFixture(t, "peer.jsonl"),
		CWD:            "/test",
	})
	if err := handler.HandleHook("Stop", hookData); err != nil {
		t.Fatalf("HandleHook Stop: %v", err)
	}
	if mockNotif.wasCalled() {
		t.Fatalf("lead Stop with an active teammate notified: %+v", mockNotif.lastCall())
	}

	idle := buildHookDataJSON(HookData{
		SessionID:     sessionID,
		CWD:           "/test",
		HookEventName: "TeammateIdle",
		TeamName:      teamName,
		TeammateName:  "alice",
	})
	if err := handler.HandleHook("TeammateIdle", idle); err != nil {
		t.Fatalf("HandleHook TeammateIdle: %v", err)
	}
	if mockNotif.callCount() != 1 || mockNotif.lastCall().status != analyzer.StatusTaskComplete {
		t.Fatalf("team-done notification = %+v, count = %d", mockNotif.lastCall(), mockNotif.callCount())
	}
}
