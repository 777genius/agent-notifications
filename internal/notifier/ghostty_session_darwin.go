//go:build darwin

package notifier

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/state"
)

const ghosttyFrontmostTerminalInfoTimeout = 1500 * time.Millisecond

type ghosttyFrontmostTerminalInfo struct {
	ID               string
	WorkingDirectory string
	Name             string
}

type ghosttyWorktreeContext struct {
	RepoRoot     string
	WorktreeName string
}

var ghosttyFrontmostTerminalInfoRunner = readGhosttyFrontmostTerminalInfo

// MaybeCaptureGhosttyTerminalID stores the current Ghostty terminal ID for the
// Claude session when we can confidently prove the frontmost Ghostty terminal is
// the active Claude session that emitted this hook.
func MaybeCaptureGhosttyTerminalID(configOverride, sessionID, cwd string) {
	if sessionID == "" || cwd == "" {
		return
	}
	if !isGhosttyBundleID(GetTerminalBundleID(configOverride)) {
		return
	}

	info, err := ghosttyFrontmostTerminalInfoRunner()
	if err != nil {
		logging.Debug("Ghostty terminal capture skipped: %v", err)
		return
	}
	if !ghosttyFrontmostTerminalMatchesSession(info, cwd) {
		logging.Debug("Ghostty terminal capture skipped: frontmost terminal did not match session cwd=%q name=%q dir=%q", cwd, info.Name, info.WorkingDirectory)
		return
	}

	if err := state.NewManager().UpdateGhosttyTerminalID(sessionID, info.ID); err != nil {
		logging.Warn("Failed to persist Ghostty terminal ID for session %s: %v", sessionID, err)
		return
	}
	logging.Debug("Captured Ghostty terminal ID %s for session %s", info.ID, sessionID)
}

func loadStoredGhosttyTerminalID(sessionID string) string {
	if sessionID == "" {
		return ""
	}

	sessionState, err := state.NewManager().Load(sessionID)
	if err != nil || sessionState == nil {
		return ""
	}

	return strings.TrimSpace(sessionState.GhosttyTerminalID)
}

func ghosttyFrontmostTerminalMatchesSession(info ghosttyFrontmostTerminalInfo, cwd string) bool {
	if strings.TrimSpace(info.ID) == "" {
		return false
	}

	normalizedTermDir := normalizeGhosttyWorkingDir(info.WorkingDirectory)
	if normalizedTermDir == "" {
		return false
	}

	lowerName := strings.ToLower(strings.TrimSpace(info.Name))
	if !strings.Contains(lowerName, "claude") {
		return false
	}

	for _, candidate := range ghosttyFocusCandidates(cwd) {
		if normalizedTermDir == candidate {
			return true
		}
	}

	worktreeCtx, ok := deriveGhosttyWorktreeContext(cwd)
	if !ok {
		return false
	}
	if normalizedTermDir != worktreeCtx.RepoRoot {
		return false
	}

	return ghosttyTerminalNameMatchesWorktree(lowerName, worktreeCtx.WorktreeName)
}

func deriveGhosttyWorktreeContext(cwd string) (ghosttyWorktreeContext, bool) {
	normalized := strings.ReplaceAll(normalizeGhosttyWorkingDir(cwd), "\\", "/")
	const marker = "/.claude/worktrees/"

	idx := strings.Index(normalized, marker)
	if idx <= 0 {
		return ghosttyWorktreeContext{}, false
	}

	repoRoot := normalizeGhosttyWorkingDir(normalized[:idx])
	worktreeName := strings.Trim(strings.TrimPrefix(normalized[idx+len(marker):], "/"), "/")
	if repoRoot == "" || worktreeName == "" {
		return ghosttyWorktreeContext{}, false
	}

	return ghosttyWorktreeContext{
		RepoRoot:     repoRoot,
		WorktreeName: worktreeName,
	}, true
}

func ghosttyTerminalNameMatchesWorktree(lowerName, worktreeName string) bool {
	return ghosttyTerminalNameContainsWorktree(lowerName, worktreeName) ||
		ghosttyTerminalNameContainsWorktree(lowerName, path.Base(worktreeName))
}

func ghosttyTerminalNameContainsWorktree(name, worktreeName string) bool {
	label := strings.ToLower(strings.TrimSpace(worktreeName))
	if label == "" {
		return false
	}

	patterns := []string{
		"-w " + label,
		"-w=" + label,
		"--worktree " + label,
		"--worktree=" + label,
	}
	for _, pattern := range patterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	return false
}

func readGhosttyFrontmostTerminalInfo() (ghosttyFrontmostTerminalInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghosttyFrontmostTerminalInfoTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", ghosttyFrontmostTerminalInfoAppleScript).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return ghosttyFrontmostTerminalInfo{}, fmt.Errorf("Ghostty frontmost terminal lookup timed out after %s", ghosttyFrontmostTerminalInfoTimeout)
	}
	if err != nil {
		outputText := strings.TrimSpace(string(output))
		if outputText == "" {
			return ghosttyFrontmostTerminalInfo{}, fmt.Errorf("Ghostty frontmost terminal lookup failed: %w", err)
		}
		return ghosttyFrontmostTerminalInfo{}, fmt.Errorf("Ghostty frontmost terminal lookup failed: %w: %s", err, outputText)
	}

	outputText := strings.TrimRight(string(output), "\r\n")
	parts := strings.SplitN(outputText, "\x1f", 3)
	if len(parts) != 3 {
		return ghosttyFrontmostTerminalInfo{}, fmt.Errorf("unexpected Ghostty frontmost terminal response: %q", outputText)
	}

	return ghosttyFrontmostTerminalInfo{
		ID:               strings.TrimSpace(parts[0]),
		WorkingDirectory: strings.TrimSpace(parts[1]),
		Name:             parts[2],
	}, nil
}

const ghosttyFrontmostTerminalInfoAppleScript = `
on normalizePath(thePath)
	if thePath is "/" then
		return "/"
	end if
	if thePath ends with "/" then
		return text 1 thru -2 of thePath
	end if
	return thePath
end normalizePath

on run
	tell application "Ghostty"
		if not frontmost then
			error "Ghostty not frontmost" number 1003
		end if
		set term to focused terminal of selected tab of front window
		set delim to ASCII character 31
		return ((id of term as string) & delim & (my normalizePath(working directory of term)) & delim & (name of term as string))
	end tell
end run
`
