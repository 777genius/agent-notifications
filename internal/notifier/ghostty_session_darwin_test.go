//go:build darwin

package notifier

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/state"
)

func TestGhosttyFrontmostTerminalMatchesSession_ExactCWD(t *testing.T) {
	info := ghosttyFrontmostTerminalInfo{
		ID:               "11",
		WorkingDirectory: "/repo/project",
		Name:             "claude",
	}

	if !ghosttyFrontmostTerminalMatchesSession(info, "/repo/project") {
		t.Fatal("expected exact cwd match to be accepted")
	}
}

func TestGhosttyFrontmostTerminalMatchesSession_ExactCWDRequiresClaudeTitle(t *testing.T) {
	info := ghosttyFrontmostTerminalInfo{
		ID:               "11",
		WorkingDirectory: "/repo/project",
		Name:             "zsh",
	}

	if ghosttyFrontmostTerminalMatchesSession(info, "/repo/project") {
		t.Fatal("non-Claude frontmost terminal should not be captured")
	}
}

func TestGhosttyFrontmostTerminalMatchesSession_WorktreeUsesRepoRootAndCommandTitle(t *testing.T) {
	info := ghosttyFrontmostTerminalInfo{
		ID:               "12",
		WorkingDirectory: "/repo",
		Name:             "claude -w feat/foo",
	}

	if !ghosttyFrontmostTerminalMatchesSession(info, "/repo/.claude/worktrees/feat/foo") {
		t.Fatal("expected worktree session to match repo-root cwd plus claude -w title")
	}
}

func TestGhosttyFrontmostTerminalMatchesSession_WorktreeRejectsMismatchedTitle(t *testing.T) {
	info := ghosttyFrontmostTerminalInfo{
		ID:               "13",
		WorkingDirectory: "/repo",
		Name:             "claude -w feat/bar",
	}

	if ghosttyFrontmostTerminalMatchesSession(info, "/repo/.claude/worktrees/feat/foo") {
		t.Fatal("mismatched worktree title should not be accepted")
	}
}

func TestMaybeCaptureGhosttyTerminalID_PersistsMatchedTerminal(t *testing.T) {
	originalRunner := ghosttyFrontmostTerminalInfoRunner
	t.Cleanup(func() {
		ghosttyFrontmostTerminalInfoRunner = originalRunner
	})

	sessionID := "ghostty-capture-test"
	t.Cleanup(func() {
		_ = state.NewManager().Delete(sessionID)
	})

	ghosttyFrontmostTerminalInfoRunner = func() (ghosttyFrontmostTerminalInfo, error) {
		return ghosttyFrontmostTerminalInfo{
			ID:               "99",
			WorkingDirectory: "/repo",
			Name:             "claude -w feat/foo",
		}, nil
	}

	MaybeCaptureGhosttyTerminalID("com.mitchellh.ghostty", sessionID, "/repo/.claude/worktrees/feat/foo")

	if got := loadStoredGhosttyTerminalID(sessionID); got != "99" {
		t.Fatalf("stored Ghostty terminal ID = %q, want 99", got)
	}
}
