//go:build !windows

package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/google/uuid"
)

// Exercise the actual Claude TeammateIdle entrypoint without Claude or an
// external provider. The Git subprocess stalls; no second lookup may happen.
func TestTeammateIdleStalledGitIsBoundedAndLookedUpOnce(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	teamName := "teammate-git-" + uuid.NewString()
	leadID := "lead-" + uuid.NewString()
	home := setupTeamConfig(t, teamName, leadID, []string{"alice", "bob"})
	setTestHome(t, home)

	cfg := &config.Config{
		Notifications: config.NotificationsConfig{
			Desktop:  config.DesktopConfig{Enabled: true},
			TeamMode: "wait-all",
		},
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Completed"},
		},
	}
	handler, notifications, _ := newTestHandler(t, cfg)
	handler.BindHookDeadline(time.Now().Add(12 * time.Second))

	team := setupTeamStateManager(t, home)
	if err := team.RecordLeadStopped(teamName); err != nil {
		t.Fatal(err)
	}
	if err := team.RecordTeammateIdle(teamName, "alice"); err != nil {
		t.Fatal(err)
	}

	calls := filepath.Join(t.TempDir(), "git-calls")
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf x >> \"$TEST_GIT_CALLS\"\nexec sleep 5\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_GIT_CALLS", calls)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	err := handler.HandleHook("TeammateIdle", buildHookDataJSON(HookData{
		SessionID:    "bob-" + uuid.NewString(),
		TeamName:     teamName,
		TeammateName: "bob",
		CWD:          t.TempDir(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 4*time.Second {
		t.Fatalf("TeammateIdle blocked on Git for %v", elapsed)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "x" {
		t.Fatalf("Git command invoked %d times, want exactly once", len(b))
	}
	if notifications.callCount() != 1 {
		t.Fatalf("expected one teammate completion notification, got %d", notifications.callCount())
	}
}
