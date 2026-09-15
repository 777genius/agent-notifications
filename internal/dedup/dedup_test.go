package dedup

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckEarlyDuplicate(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}

	// First check should be false (no lock exists)
	isDup := mgr.CheckEarlyDuplicate("test-session")
	assert.False(t, isDup)

	// Create a fresh lock
	lockPath := mgr.getLockPath("test-session")
	err := os.WriteFile(lockPath, []byte(""), 0644)
	require.NoError(t, err)
	defer os.Remove(lockPath)

	// Add small delay to ensure file system flush (important for race detector)
	time.Sleep(10 * time.Millisecond)

	// Immediately check again - should be duplicate
	isDup = mgr.CheckEarlyDuplicate("test-session")
	assert.True(t, isDup)

	// Wait 3 seconds and check again - should not be duplicate (stale)
	time.Sleep(3 * time.Second)
	isDup = mgr.CheckEarlyDuplicate("test-session")
	assert.False(t, isDup)
}

func TestAcquireLock(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}

	// First acquisition should succeed
	acquired, err := mgr.AcquireLock("test-session")
	require.NoError(t, err)
	assert.True(t, acquired)

	// Cleanup
	lockPath := mgr.getLockPath("test-session")
	defer os.Remove(lockPath)

	// Second acquisition immediately should fail (fresh lock)
	acquired, err = mgr.AcquireLock("test-session")
	require.NoError(t, err)
	assert.False(t, acquired)

	// Change lock mtime to be old
	oldTime := time.Now().Add(-3 * time.Second)
	err = os.Chtimes(lockPath, oldTime, oldTime)
	require.NoError(t, err)

	// Should succeed now (stale lock replaced)
	acquired, err = mgr.AcquireLock("test-session")
	require.NoError(t, err)
	assert.True(t, acquired)
}

func TestAcquireLockConcurrent(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}
	sessionID := "concurrent-test"

	// Cleanup
	lockPath := mgr.getLockPath(sessionID)
	defer os.Remove(lockPath)

	// Run 10 goroutines trying to acquire lock
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acquired, err := mgr.AcquireLock(sessionID)
			require.NoError(t, err)
			if acquired {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one should succeed
	assert.Equal(t, 1, successCount)
}

func TestReleaseLock(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}

	// Acquire lock
	acquired, err := mgr.AcquireLock("test-session")
	require.NoError(t, err)
	assert.True(t, acquired)

	lockPath := mgr.getLockPath("test-session")
	assert.FileExists(t, lockPath)

	// Release lock
	err = mgr.ReleaseLock("test-session")
	require.NoError(t, err)

	// Lock file should be gone
	_, err = os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err))

	// Releasing non-existent lock should not error
	err = mgr.ReleaseLock("test-session")
	require.NoError(t, err)
}

func TestCleanup(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}
	tempDir := mgr.tempDir

	// Create old lock
	oldLockPath := filepath.Join(tempDir, "claude-notification-Stop-old.lock")
	err := os.WriteFile(oldLockPath, []byte(""), 0644)
	require.NoError(t, err)

	oldTime := time.Now().Add(-2 * time.Minute)
	err = os.Chtimes(oldLockPath, oldTime, oldTime)
	require.NoError(t, err)

	// Create recent lock
	recentLockPath := filepath.Join(tempDir, "claude-notification-Stop-recent.lock")
	err = os.WriteFile(recentLockPath, []byte(""), 0644)
	require.NoError(t, err)
	defer os.Remove(recentLockPath)

	// Cleanup old locks (>60s)
	err = mgr.Cleanup(60)
	require.NoError(t, err)

	// Old lock should be deleted
	_, err = os.Stat(oldLockPath)
	assert.True(t, os.IsNotExist(err))

	// Recent lock should remain
	_, err = os.Stat(recentLockPath)
	assert.NoError(t, err)
}

func TestCleanupForSession(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}

	sessionID := "test-session-123"

	// Create lock for this session
	_, err := mgr.AcquireLock(sessionID)
	require.NoError(t, err)

	// Create lock for different session
	_, err = mgr.AcquireLock("other-session")
	require.NoError(t, err)
	defer func() { _ = mgr.ReleaseLock("other-session") }()

	// Verify both locks exist
	testLock := mgr.getLockPath(sessionID)
	otherLock := mgr.getLockPath("other-session")
	assert.FileExists(t, testLock)
	assert.FileExists(t, otherLock)

	// Cleanup for specific session
	err = mgr.CleanupForSession(sessionID)
	require.NoError(t, err)

	// Lock for test-session-123 should be gone
	_, err = os.Stat(testLock)
	assert.True(t, os.IsNotExist(err))

	// Other session lock should remain
	_, err = os.Stat(otherLock)
	assert.NoError(t, err)
}

func TestGetLockPath_WithHookEvent(t *testing.T) {
	mgr := &Manager{tempDir: t.TempDir()}
	sessionID := "test-session-456"

	// Test without hookEvent
	pathWithout := mgr.getLockPath(sessionID)
	assert.Contains(t, pathWithout, "claude-notification-test-session-456.lock")
	assert.NotContains(t, pathWithout, "-Stop")

	// Test with hookEvent
	pathWith := mgr.getLockPath(sessionID, "Stop")
	assert.Contains(t, pathWith, "claude-notification-test-session-456-Stop.lock")
	assert.Contains(t, pathWith, "-Stop")

	// Verify paths are different
	assert.NotEqual(t, pathWithout, pathWith)
}

func TestCleanupForSession_RemoveError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Unix-style permissions not supported")
	}

	// Create a custom temp directory that we can control permissions on
	testTempDir := filepath.Join(t.TempDir(), "locks")
	err := os.MkdirAll(testTempDir, 0755)
	require.NoError(t, err)

	// Create manager with custom temp dir
	mgr := &Manager{tempDir: testTempDir}
	sessionID := "test-protected"

	// Create a lock file
	lockPath := mgr.getLockPath(sessionID)
	err = os.WriteFile(lockPath, []byte(""), 0644)
	require.NoError(t, err)

	// Make the directory read-only to prevent deletion
	err = os.Chmod(testTempDir, 0555) // Read + execute only
	require.NoError(t, err)

	// Cleanup should fail due to permissions
	err = mgr.CleanupForSession(sessionID)
	assert.Error(t, err, "CleanupForSession should fail on permission denied")

	// Restore permissions for cleanup
	_ = os.Chmod(testTempDir, 0755)
}
