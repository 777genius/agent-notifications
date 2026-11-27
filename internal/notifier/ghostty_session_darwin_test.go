//go:build darwin

package notifier

import (
	"errors"
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

func TestGhosttyTerminalMatchesSessionCWD_PromptModeAcceptsTopicTitle(t *testing.T) {
	info := ghosttyFrontmostTerminalInfo{
		ID:               "21",
		WorkingDirectory: "/repo/project",
		Name:             "✳ Plan a trip",
	}

	if !ghosttyTerminalMatchesSessionCWD(info, "/repo/project", false) {
		t.Fatal("prompt-time capture should accept a matching cwd without 'claude' in the title")
	}
	if ghosttyTerminalMatchesSessionCWD(info, "/repo/project", true) {
		t.Fatal("legacy capture must still require 'claude' in the title")
	}
	if ghosttyFrontmostTerminalMatchesSession(info, "/repo/project") {
		t.Fatal("ghosttyFrontmostTerminalMatchesSession must keep requiring 'claude' in the title")
	}
}

func TestGhosttyTerminalMatchesSessionCWD_PromptModeStillRequiresIdentity(t *testing.T) {
	tests := []struct {
		name string
		info ghosttyFrontmostTerminalInfo
		cwd  string
	}{
		{name: "empty id", info: ghosttyFrontmostTerminalInfo{ID: "  ", WorkingDirectory: "/repo/project", Name: "✳ Plan a trip"}, cwd: "/repo/project"},
		{name: "empty working directory", info: ghosttyFrontmostTerminalInfo{ID: "22", WorkingDirectory: "", Name: "✳ Plan a trip"}, cwd: "/repo/project"},
		{name: "cwd mismatch", info: ghosttyFrontmostTerminalInfo{ID: "23", WorkingDirectory: "/repo/other", Name: "✳ Plan a trip"}, cwd: "/repo/project"},
		{name: "parent directory is not a match", info: ghosttyFrontmostTerminalInfo{ID: "24", WorkingDirectory: "/repo", Name: "✳ Plan a trip"}, cwd: "/repo/project"},
		{name: "worktree under a different repo", info: ghosttyFrontmostTerminalInfo{ID: "25", WorkingDirectory: "/other", Name: "✳ Plan a trip"}, cwd: "/repo/.claude/worktrees/feat/foo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if ghosttyTerminalMatchesSessionCWD(tc.info, tc.cwd, false) {
				t.Fatalf("prompt-time capture accepted %+v for cwd %q", tc.info, tc.cwd)
			}
		})
	}
}

func TestGhosttyTerminalMatchesSessionCWD_PromptModeWorktreeMatchesRepoRoot(t *testing.T) {
	info := ghosttyFrontmostTerminalInfo{
		ID:               "26",
		WorkingDirectory: "/repo",
		Name:             "✳ Refactor the parser",
	}

	if !ghosttyTerminalMatchesSessionCWD(info, "/repo/.claude/worktrees/feat/foo", false) {
		t.Fatal("prompt-time capture should accept a worktree session whose frontmost terminal is at the repo root")
	}
	if ghosttyTerminalMatchesSessionCWD(info, "/repo/.claude/worktrees/feat/foo", true) {
		t.Fatal("legacy worktree capture must still require a 'claude -w <name>' title")
	}
}

func TestMaybeCaptureGhosttyTerminalIDOnPrompt_PersistsTopicTitledTerminal(t *testing.T) {
	originalRunner := ghosttyFrontmostTerminalInfoRunner
	t.Cleanup(func() {
		ghosttyFrontmostTerminalInfoRunner = originalRunner
	})

	sessionID := "ghostty-prompt-capture-test"
	_ = state.NewManager().Delete(sessionID)
	t.Cleanup(func() {
		_ = state.NewManager().Delete(sessionID)
	})

	ghosttyFrontmostTerminalInfoRunner = func() (ghosttyFrontmostTerminalInfo, error) {
		return ghosttyFrontmostTerminalInfo{
			ID:               "77",
			WorkingDirectory: "/repo/project",
			Name:             "✳ Plan a trip",
		}, nil
	}

	MaybeCaptureGhosttyTerminalIDOnPrompt("com.mitchellh.ghostty", sessionID, "/repo/project")

	if got := loadStoredGhosttyTerminalID(sessionID); got != "77" {
		t.Fatalf("stored Ghostty terminal ID = %q, want 77", got)
	}
}

func TestMaybeCaptureGhosttyTerminalID_LegacyRejectsTopicTitledTerminal(t *testing.T) {
	originalRunner := ghosttyFrontmostTerminalInfoRunner
	t.Cleanup(func() {
		ghosttyFrontmostTerminalInfoRunner = originalRunner
	})

	sessionID := "ghostty-legacy-topic-title-test"
	_ = state.NewManager().Delete(sessionID)
	t.Cleanup(func() {
		_ = state.NewManager().Delete(sessionID)
	})

	ghosttyFrontmostTerminalInfoRunner = func() (ghosttyFrontmostTerminalInfo, error) {
		return ghosttyFrontmostTerminalInfo{
			ID:               "78",
			WorkingDirectory: "/repo/project",
			Name:             "✳ Plan a trip",
		}, nil
	}

	MaybeCaptureGhosttyTerminalID("com.mitchellh.ghostty", sessionID, "/repo/project")

	if got := loadStoredGhosttyTerminalID(sessionID); got != "" {
		t.Fatalf("legacy capture stored Ghostty terminal ID %q for a non-claude title", got)
	}
}

func TestMaybeCaptureGhosttyTerminalIDOnPrompt_SkipsWhenGhosttyNotFrontmost(t *testing.T) {
	originalRunner := ghosttyFrontmostTerminalInfoRunner
	t.Cleanup(func() {
		ghosttyFrontmostTerminalInfoRunner = originalRunner
	})

	sessionID := "ghostty-prompt-not-frontmost-test"
	_ = state.NewManager().Delete(sessionID)
	t.Cleanup(func() {
		_ = state.NewManager().Delete(sessionID)
	})

	ghosttyFrontmostTerminalInfoRunner = func() (ghosttyFrontmostTerminalInfo, error) {
		return ghosttyFrontmostTerminalInfo{}, errors.New("Ghostty not frontmost")
	}

	MaybeCaptureGhosttyTerminalIDOnPrompt("com.mitchellh.ghostty", sessionID, "/repo/project")

	if got := loadStoredGhosttyTerminalID(sessionID); got != "" {
		t.Fatalf("stored Ghostty terminal ID = %q, want none", got)
	}
}

func TestMaybeCaptureGhosttyTerminalIDOnPrompt_SkipsNonGhosttyTerminal(t *testing.T) {
	originalRunner := ghosttyFrontmostTerminalInfoRunner
	t.Cleanup(func() {
		ghosttyFrontmostTerminalInfoRunner = originalRunner
	})

	called := false
	ghosttyFrontmostTerminalInfoRunner = func() (ghosttyFrontmostTerminalInfo, error) {
		called = true
		return ghosttyFrontmostTerminalInfo{ID: "79", WorkingDirectory: "/repo/project", Name: "zsh"}, nil
	}

	MaybeCaptureGhosttyTerminalIDOnPrompt("com.apple.Terminal", "ghostty-prompt-non-ghostty-test", "/repo/project")

	if called {
		t.Fatal("non-Ghostty terminal must not query Ghostty")
	}
}
