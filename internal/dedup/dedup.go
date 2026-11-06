package dedup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
)

// Manager handles deduplication using two-phase locking
type Manager struct {
	tempDir string
	fileAge func(string) int64
}

// NewManager creates a new deduplication manager
func NewManager() *Manager {
	return NewManagerWithFileAge(nil)
}

// NewManagerWithFileAge supplies the age source for lock TTL decisions.
// A nil source retains the native filesystem clock used by NewManager.
func NewManagerWithFileAge(fileAge func(string) int64) *Manager {
	return &Manager{tempDir: platform.TempDir(), fileAge: fileAge}
}

func (m *Manager) lockAge(path string) int64 {
	if m.fileAge != nil {
		return m.fileAge(path)
	}
	return platform.FileAge(path)
}

// getLockPath returns the path to the lock file for a session and hook event
// If hookEvent is empty, uses a global lock for the session (backward compatibility)
func (m *Manager) getLockPath(sessionID string, hookEvent ...string) string {
	if len(hookEvent) > 0 && hookEvent[0] != "" {
		return filepath.Join(m.tempDir, fmt.Sprintf("claude-notification-%s-%s.lock", sessionID, hookEvent[0]))
	}
	return filepath.Join(m.tempDir, fmt.Sprintf("claude-notification-%s.lock", sessionID))
}

// CheckEarlyDuplicate performs Phase 1 check for duplicates
// Returns true if this is a duplicate and should be skipped
// hookEvent parameter is optional - if provided, checks hook-specific lock file
func (m *Manager) CheckEarlyDuplicate(sessionID string, hookEvent ...string) bool {
	lockPath := m.getLockPath(sessionID, hookEvent...)

	if !platform.FileExists(lockPath) {
		return false
	}

	// Check lock age
	age := m.lockAge(lockPath)

	// If mtime is unavailable (Windows issue) or lock is fresh (<2s), treat as duplicate
	if age == -1 {
		logging.Warn("Lock file mtime unavailable: %s (treating as duplicate, possible causes: permission error, race condition, or filesystem issue)", lockPath)
		return true
	}
	if age >= 0 && age < 2 {
		return true
	}

	return false
}

// AcquireLock performs the existing short, two-second event lock.
func (m *Manager) AcquireLock(sessionID string, hookEvent ...string) (bool, error) {
	return m.AcquireLockWithTTL(sessionID, 2, hookEvent...)
}

// AcquireLockWithTTL retains a longer event-scoped lock only when its caller
// has a bounded delivery lifecycle (Claude's delayed desktop-only Stop).
// The marker is a pending attempt, never a claim of successful delivery.
func (m *Manager) AcquireLockWithTTL(sessionID string, ttlSeconds int64, hookEvent ...string) (bool, error) {
	if ttlSeconds < 2 || ttlSeconds > 60 {
		return false, fmt.Errorf("invalid lock TTL")
	}
	lockPath := m.getLockPath(sessionID, hookEvent...)

	// Try to create lock atomically
	created, err := platform.AtomicCreateFile(lockPath)
	if err != nil {
		return false, fmt.Errorf("failed to create lock file: %w", err)
	}

	if created {
		// Lock acquired successfully
		return true, nil
	}

	// Lock exists - check if it's stale
	age := m.lockAge(lockPath)

	// An unavailable timestamp is not proof of expiry for a retained
	// pending Stop. Preserve legacy two-second lock behavior elsewhere.
	if ttlSeconds > 2 && age < 0 {
		return false, nil
	}
	if age >= 0 && age < ttlSeconds {
		return false, nil
	}

	// Lock is stale - try to replace it
	_ = os.Remove(lockPath) // Ignore error - someone else might have deleted it

	// Try again
	created, err = platform.AtomicCreateFile(lockPath)
	if err != nil {
		return false, fmt.Errorf("failed to create lock file after cleanup: %w", err)
	}

	return created, nil
}

// ReleaseLock releases a lock (optional, locks are cleaned up automatically)
// hookEvent parameter is optional - if provided, releases hook-specific lock file
func (m *Manager) ReleaseLock(sessionID string, hookEvent ...string) error {
	lockPath := m.getLockPath(sessionID, hookEvent...)
	if platform.FileExists(lockPath) {
		return os.Remove(lockPath)
	}
	return nil
}

// Cleanup cleans up old lock files (older than maxAge seconds)
func (m *Manager) Cleanup(maxAge int64) error {
	return platform.CleanupOldFiles(m.tempDir, "claude-notification-*.lock", maxAge)
}

// CleanupForSession cleans up lock file for a specific session
func (m *Manager) CleanupForSession(sessionID string) error {
	lockPath := m.getLockPath(sessionID)
	if platform.FileExists(lockPath) {
		return os.Remove(lockPath)
	}
	return nil
}

// AcquireContentLock acquires a lock for content-based deduplication
// Uses a separate lock file with longer TTL (5s) to prevent race conditions
// between different hook types (Stop, Notification) with same content
func (m *Manager) AcquireContentLock(sessionID string) (bool, error) {
	lockPath := filepath.Join(m.tempDir, fmt.Sprintf("claude-notification-%s-content.lock", sessionID))

	// Try to create lock atomically
	created, err := platform.AtomicCreateFile(lockPath)
	if err != nil {
		return false, fmt.Errorf("failed to create content lock file: %w", err)
	}

	if created {
		return true, nil
	}

	// Lock exists - check if it's stale (5s TTL for content lock)
	age := m.lockAge(lockPath)
	if age >= 0 && age < 5 {
		// Lock is fresh - wait a bit and try again
		// This gives the first process time to complete
		return false, nil
	}

	// Lock is stale - try to replace it
	_ = os.Remove(lockPath)
	created, err = platform.AtomicCreateFile(lockPath)
	if err != nil {
		return false, fmt.Errorf("failed to create content lock file after cleanup: %w", err)
	}

	return created, nil
}

// ReleaseContentLock releases the content-based deduplication lock
func (m *Manager) ReleaseContentLock(sessionID string) error {
	lockPath := filepath.Join(m.tempDir, fmt.Sprintf("claude-notification-%s-content.lock", sessionID))
	if platform.FileExists(lockPath) {
		return os.Remove(lockPath)
	}
	return nil
}
