package teamstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/logging"
)

// teamConfig represents the relevant fields from ~/.claude/teams/{name}/config.json
type teamConfig struct {
	Name          string       `json:"name"`
	LeadSessionID string       `json:"leadSessionId"`
	Members       []teamMember `json:"members"`
}

// teamMember represents a member entry in team config
type teamMember struct {
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	AgentType string `json:"agentType"`
}

// TeamInfo holds detected team information for the current session
type TeamInfo struct {
	TeamName   string
	Members    []string // non-lead member names
	ConfigPath string
}

// State tracks team notification state (persisted to /tmp)
type State struct {
	TeamName    string           `json:"team_name"`
	LeadStopped bool             `json:"lead_stopped"`
	LeadStopAt  int64            `json:"lead_stop_at,omitempty"`
	IdleMembers map[string]int64 `json:"idle_members"` // member name → unix timestamp
	NotifiedAt  int64            `json:"notified_at,omitempty"`
}

// Manager handles team detection and state tracking.
// All state mutations use file-level locking (flock) for cross-process safety,
// since Stop and TeammateIdle hooks run as separate OS processes.
type Manager struct {
	claudeDir string // defaults to ~/.claude
}

// NewManager creates a new team state manager.
// claudeDir can be empty to use the default (~/.claude).
func NewManager(claudeDir string) *Manager {
	if claudeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			claudeDir = filepath.Join(os.TempDir(), ".claude")
		} else {
			claudeDir = filepath.Join(home, ".claude")
		}
	}
	return &Manager{claudeDir: claudeDir}
}

// DetectTeamLead checks if the given session ID is a team lead.
// Returns team info if found, nil otherwise.
func (m *Manager) DetectTeamLead(sessionID string) *TeamInfo {
	teamsDir := filepath.Join(m.claudeDir, "teams")
	entries, err := os.ReadDir(teamsDir)
	if err != nil {
		logging.Debug("teamstate: cannot read teams dir %s: %v", teamsDir, err)
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		configPath := filepath.Join(teamsDir, entry.Name(), "config.json")
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}

		var cfg teamConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}

		if cfg.LeadSessionID == sessionID {
			// Found our team — collect non-lead member names
			var members []string
			for _, member := range cfg.Members {
				if member.AgentType != "team-lead" {
					members = append(members, member.Name)
				}
			}
			if len(members) == 0 {
				// Team with no non-lead members — not a real team scenario
				logging.Debug("teamstate: team %q has no non-lead members, skipping", cfg.Name)
				continue
			}
			logging.Debug("teamstate: session %s is lead of team %q with %d members: %s",
				sessionID, cfg.Name, len(members), strings.Join(members, ", "))
			return &TeamInfo{
				TeamName:   cfg.Name,
				Members:    members,
				ConfigPath: configPath,
			}
		}
	}

	return nil
}

// DetectTeamByName finds team info by team name.
// Returns team info if found, nil otherwise.
func (m *Manager) DetectTeamByName(teamName string) *TeamInfo {
	configPath := filepath.Join(m.claudeDir, "teams", teamName, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		logging.Debug("teamstate: cannot read team config %s: %v", configPath, err)
		return nil
	}

	var cfg teamConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		logging.Debug("teamstate: cannot parse team config %s: %v", configPath, err)
		return nil
	}

	var members []string
	for _, member := range cfg.Members {
		if member.AgentType != "team-lead" {
			members = append(members, member.Name)
		}
	}

	return &TeamInfo{
		TeamName:   cfg.Name,
		Members:    members,
		ConfigPath: configPath,
	}
}

// statePath returns the path to the state file for a team
func statePath(teamName string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("claude-team-notify-%s.json", teamName))
}

// lockPath returns the path to the flock file for a team
func lockPath(teamName string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("claude-team-notify-%s.lock", teamName))
}

// loadStateUnlocked reads team state from disk without locking.
// Caller must hold the file lock.
func loadStateUnlocked(teamName string) (*State, error) {
	path := statePath(teamName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{
				TeamName:    teamName,
				IdleMembers: make(map[string]int64),
			}, nil
		}
		return nil, fmt.Errorf("read team state: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupted state — return fresh
		logging.Warn("teamstate: corrupted state file %s, resetting: %v", path, err)
		return &State{
			TeamName:    teamName,
			IdleMembers: make(map[string]int64),
		}, nil
	}
	if s.IdleMembers == nil {
		s.IdleMembers = make(map[string]int64)
	}

	return &s, nil
}

// saveStateUnlocked persists team state to disk atomically without locking.
// Caller must hold the file lock.
func saveStateUnlocked(s *State) error {
	path := statePath(s.TeamName)
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal team state: %w", err)
	}

	// Atomic write via temp file + rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write team state tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename team state: %w", err)
	}

	return nil
}

// LoadState loads team state from disk (for read-only access / tests).
func (m *Manager) LoadState(teamName string) (*State, error) {
	var s *State
	err := withFileLock(teamName, func() error {
		var loadErr error
		s, loadErr = loadStateUnlocked(teamName)
		return loadErr
	})
	return s, err
}

// SaveState persists team state to disk (for tests).
func (m *Manager) SaveState(s *State) error {
	return withFileLock(s.TeamName, func() error {
		return saveStateUnlocked(s)
	})
}

// RecordLeadStopped marks the team lead as stopped and persists state.
// Uses file locking to prevent races with concurrent TeammateIdle hooks.
func (m *Manager) RecordLeadStopped(teamName string) error {
	return withFileLock(teamName, func() error {
		s, err := loadStateUnlocked(teamName)
		if err != nil {
			return err
		}
		s.LeadStopped = true
		s.LeadStopAt = time.Now().Unix()
		return saveStateUnlocked(s)
	})
}

// RecordTeammateIdle marks a teammate as idle and persists state.
// Uses file locking to prevent races with concurrent Stop hooks.
func (m *Manager) RecordTeammateIdle(teamName, teammateName string) error {
	return withFileLock(teamName, func() error {
		s, err := loadStateUnlocked(teamName)
		if err != nil {
			return err
		}
		s.IdleMembers[teammateName] = time.Now().Unix()
		return saveStateUnlocked(s)
	})
}

// CheckAllIdle checks if the lead has stopped AND all expected members are idle.
// Returns true if a notification should be sent.
// Uses file locking for consistent reads.
func (m *Manager) CheckAllIdle(teamName string, expectedMembers []string) (bool, error) {
	var result bool
	err := withFileLock(teamName, func() error {
		s, err := loadStateUnlocked(teamName)
		if err != nil {
			return err
		}

		if !s.LeadStopped {
			logging.Debug("teamstate: lead not stopped yet for team %q", teamName)
			return nil
		}

		for _, member := range expectedMembers {
			if _, idle := s.IdleMembers[member]; !idle {
				logging.Debug("teamstate: member %q not idle yet in team %q", member, teamName)
				return nil
			}
		}

		// Prevent duplicate notifications: check if we already notified
		if s.NotifiedAt > 0 && s.NotifiedAt >= s.LeadStopAt {
			logging.Debug("teamstate: already notified for team %q (notified_at=%d >= lead_stop_at=%d)",
				teamName, s.NotifiedAt, s.LeadStopAt)
			return nil
		}

		logging.Debug("teamstate: all conditions met for team %q — lead stopped + all %d members idle",
			teamName, len(expectedMembers))
		result = true
		return nil
	})
	return result, err
}

// MarkNotified records that a notification was sent and resets state for next cycle.
// Uses file locking for atomic read-modify-write.
func (m *Manager) MarkNotified(teamName string) error {
	return withFileLock(teamName, func() error {
		s, err := loadStateUnlocked(teamName)
		if err != nil {
			return err
		}
		s.NotifiedAt = time.Now().Unix()
		// Reset state for next cycle: lead will stop again, teammates will go idle again
		s.LeadStopped = false
		s.IdleMembers = make(map[string]int64)
		return saveStateUnlocked(s)
	})
}

// Cleanup removes state and lock files older than maxAge seconds.
func (m *Manager) Cleanup(maxAgeSec int64) {
	cutoff := time.Now().Add(-time.Duration(maxAgeSec) * time.Second)
	for _, pattern := range []string{
		filepath.Join(os.TempDir(), "claude-team-notify-*.json"),
		filepath.Join(os.TempDir(), "claude-team-notify-*.lock"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(path)
			}
		}
	}
}
