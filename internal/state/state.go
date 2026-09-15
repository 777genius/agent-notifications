package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/platform"
)

// SessionState represents per-session state
type SessionState struct {
	SessionID               string `json:"session_id"`
	LastInteractiveTool     string `json:"last_interactive_tool"`
	LastTimestamp           int64  `json:"last_ts"`
	LastTaskCompleteTime    int64  `json:"last_task_complete_ts,omitempty"`
	LastNotificationTime    int64  `json:"last_notification_ts,omitempty"`
	LastNotificationStatus  string `json:"last_notification_status,omitempty"`
	LastNotificationMessage string `json:"last_notification_message,omitempty"`
	GhosttyTerminalID       string `json:"ghostty_terminal_id,omitempty"`
	CWD                     string `json:"cwd"`
}

// Manager manages session state
type Manager struct {
	tempDir string
}

// NewManager creates a new state manager
func NewManager() *Manager {
	return &Manager{
		tempDir: platform.TempDir(),
	}
}

// getStatePath returns the path to the state file for a session
func (m *Manager) getStatePath(sessionID string) string {
	return filepath.Join(m.tempDir, fmt.Sprintf("claude-session-state-%s.json", sessionID))
}

// Load loads session state from disk
// Returns nil if state file doesn't exist
func (m *Manager) Load(sessionID string) (*SessionState, error) {
	path := m.getStatePath(sessionID)
	if !platform.FileExists(path) {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return &state, nil
}

// Save saves session state to disk
func (m *Manager) Save(state *SessionState) error {
	path := m.getStatePath(state.SessionID)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize state: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// Delete deletes session state
func (m *Manager) Delete(sessionID string) error {
	path := m.getStatePath(sessionID)
	if !platform.FileExists(path) {
		return nil
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete state file: %w", err)
	}

	return nil
}

// UpdateInteractiveTool updates the last interactive tool and timestamp
func (m *Manager) UpdateInteractiveTool(sessionID, toolName, cwd string) error {
	state, err := m.Load(sessionID)
	if err != nil {
		return err
	}

	if state == nil {
		state = &SessionState{
			SessionID: sessionID,
		}
	}

	state.LastInteractiveTool = toolName
	state.LastTimestamp = platform.CurrentTimestamp()
	state.CWD = cwd

	return m.Save(state)
}

// UpdateGhosttyTerminalID stores the exact Ghostty terminal ID associated with a
// Claude session so future notification clicks can target the correct tab.
func (m *Manager) UpdateGhosttyTerminalID(sessionID, terminalID string) error {
	state, err := m.Load(sessionID)
	if err != nil {
		return err
	}

	if state == nil {
		state = &SessionState{
			SessionID: sessionID,
		}
	}

	state.GhosttyTerminalID = strings.TrimSpace(terminalID)

	return m.Save(state)
}

// UpdateTaskComplete updates the last task complete timestamp
func (m *Manager) UpdateTaskComplete(sessionID string) error {
	state, err := m.Load(sessionID)
	if err != nil {
		return err
	}

	if state == nil {
		state = &SessionState{
			SessionID: sessionID,
		}
	}

	state.LastTaskCompleteTime = platform.CurrentTimestamp()

	return m.Save(state)
}

// ShouldSuppressQuestion checks if a question notification should be suppressed
// due to being within the cooldown window after a task completion
func (m *Manager) ShouldSuppressQuestion(sessionID string, cooldownSeconds int) (bool, error) {
	if cooldownSeconds <= 0 {
		return false, nil
	}

	state, err := m.Load(sessionID)
	if err != nil {
		return false, err
	}

	if state == nil || state.LastTaskCompleteTime == 0 {
		return false, nil
	}

	// Check if we're within the cooldown window
	now := platform.CurrentTimestamp()
	elapsed := now - state.LastTaskCompleteTime

	return elapsed < int64(cooldownSeconds), nil
}

// UpdateState updates state based on the detected status
func (m *Manager) UpdateState(sessionID string, status analyzer.Status, toolName, cwd string) error {
	switch status {
	case analyzer.StatusTaskComplete:
		return m.UpdateTaskComplete(sessionID)
	case analyzer.StatusPlanReady, analyzer.StatusQuestion:
		if toolName != "" {
			return m.UpdateInteractiveTool(sessionID, toolName, cwd)
		}
	}
	return nil
}

// Cleanup cleans up old state files (older than maxAge seconds)
func (m *Manager) Cleanup(maxAge int64) error {
	return platform.CleanupOldFiles(m.tempDir, "claude-session-state-*.json", maxAge)
}

// UpdateLastNotification updates the last notification timestamp, status, and message
func (m *Manager) UpdateLastNotification(sessionID string, status analyzer.Status, message string) error {
	state, err := m.Load(sessionID)
	if err != nil {
		return err
	}

	if state == nil {
		state = &SessionState{
			SessionID: sessionID,
		}
	}

	state.LastNotificationTime = platform.CurrentTimestamp()
	state.LastNotificationStatus = string(status)
	state.LastNotificationMessage = message

	return m.Save(state)
}

// ShouldSuppressQuestionAfterAnyNotification checks if a question notification should be suppressed
// due to being within the cooldown window after ANY notification
func (m *Manager) ShouldSuppressQuestionAfterAnyNotification(sessionID string, cooldownSeconds int) (bool, error) {
	if cooldownSeconds <= 0 {
		return false, nil
	}

	state, err := m.Load(sessionID)
	if err != nil {
		return false, err
	}

	if state == nil || state.LastNotificationTime == 0 {
		return false, nil
	}

	// Check if we're within the cooldown window
	now := platform.CurrentTimestamp()
	elapsed := now - state.LastNotificationTime
	shouldSuppress := elapsed < int64(cooldownSeconds)

	// Import logging to add debug output
	// Note: This creates a circular dependency, so we'll skip logging here
	// and rely on the caller to log the result

	return shouldSuppress, nil
}

// normalizeMessage normalizes a message for comparison by:
// - Trimming whitespace
// - Removing trailing dots
// - Converting to lowercase
func normalizeMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	msg = strings.TrimRight(msg, ".")
	return strings.ToLower(msg)
}

// IsDuplicateMessage checks if the given message is a duplicate of a recent notification
// within the specified time window (in seconds)
func (m *Manager) IsDuplicateMessage(sessionID string, message string, windowSeconds int) (bool, error) {
	if windowSeconds <= 0 {
		return false, nil
	}

	state, err := m.Load(sessionID)
	if err != nil {
		return false, err
	}

	if state == nil || state.LastNotificationTime == 0 || state.LastNotificationMessage == "" {
		return false, nil
	}

	// Check if we're within the time window
	now := platform.CurrentTimestamp()
	elapsed := now - state.LastNotificationTime
	if elapsed > int64(windowSeconds) {
		return false, nil
	}

	// Compare normalized messages
	return normalizeMessage(message) == normalizeMessage(state.LastNotificationMessage), nil
}
