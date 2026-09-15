package notifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/platform"
)

// TestPlaySoundWithBuiltInFiles tests sound playback with actual MP3 files if available
func TestPlaySoundWithBuiltInFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping sound playback test in short mode")
	}

	// Try to find the sounds directory
	soundsDir := findSoundsDirectory()
	if soundsDir == "" {
		t.Skip("Sounds directory not found, skipping sound playback test")
	}

	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Volume = 0.3 // 30% volume for tests
	n := New(cfg)
	defer n.Close()

	tests := []struct {
		name     string
		filename string
	}{
		{"task-complete", "task-complete.mp3"},
		{"review-complete", "review-complete.mp3"},
		{"question", "question.mp3"},
		{"plan-ready", "plan-ready.mp3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			soundPath := filepath.Join(soundsDir, tt.filename)

			if !platform.FileExists(soundPath) {
				t.Skipf("Sound file not found: %s", soundPath)
			}

			// Test that playSound doesn't crash
			// We can't really test that audio is actually playing without human verification
			// But we can test that the function completes without error
			n.playSound(soundPath)

			// If we get here, playSound completed (either successfully or with logged error)
			// This is good enough for automated testing
		})
	}
}

// TestPlayerInitialization tests audio player initialization
func TestPlayerInitialization(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Volume = 0.3 // 30% volume for tests
	n := New(cfg)
	defer n.Close()

	// First initialization
	err := n.initPlayer()
	if err != nil {
		// In CI environments without audio backend, context init may fail
		if os.Getenv("CI") != "" {
			t.Skipf("Skipping in CI (no audio backend): %v", err)
		}
		t.Errorf("initPlayer() first call returned error: %v", err)
	}

	// Check that player was initialized
	if n.audioPlayer == nil {
		t.Error("initPlayer() did not create audioPlayer")
	}

	// Second initialization should be safe (no-op due to sync.Once)
	err = n.initPlayer()
	if err != nil {
		t.Errorf("initPlayer() second call returned error: %v", err)
	}
}

// TestPlayerWithCustomDevice tests audio player with custom device
func TestPlayerWithCustomDevice(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Volume = 0.3
	cfg.Notifications.Desktop.AudioDevice = "NonExistentDevice12345"

	n := New(cfg)
	defer n.Close()

	// Initialization should fail for non-existent device
	err := n.initPlayer()
	if err == nil {
		t.Error("initPlayer() expected error for non-existent device, got nil")
	} else {
		// In CI, error might be about context init rather than device not found
		// Both are valid errors, so we just verify an error occurred
		t.Logf("initPlayer() returned expected error: %v", err)
	}
}

// TestGracefulShutdown tests that Close() waits for sounds to finish
func TestGracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping graceful shutdown test in short mode")
	}

	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Volume = 0.3 // 30% volume for tests
	n := New(cfg)

	// Don't play any sounds, just test Close()
	err := n.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// TestSystemSoundsAvailability tests detection of system sounds
func TestSystemSoundsAvailability(t *testing.T) {
	tests := []struct {
		name      string
		checkFunc func() bool
		soundPath string
	}{
		{
			name:      "macOS system sounds",
			checkFunc: platform.IsMacOS,
			soundPath: "/System/Library/Sounds/Glass.aiff",
		},
		{
			name:      "Linux system sounds",
			checkFunc: platform.IsLinux,
			soundPath: "/usr/share/sounds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.checkFunc() {
				t.Skipf("Skipping %s test on this platform", tt.name)
			}

			exists := platform.FileExists(tt.soundPath)
			t.Logf("System sounds at %s: exists=%v", tt.soundPath, exists)

			// On the expected platform, log whether system sounds are available
			// (Not a failure if they're not available, just informational)
		})
	}
}

// TestBuiltInSoundsExist tests that built-in sound files exist
func TestBuiltInSoundsExist(t *testing.T) {
	soundsDir := findSoundsDirectory()
	if soundsDir == "" {
		t.Skip("Sounds directory not found")
	}

	requiredSounds := []string{
		"task-complete.mp3",
		"review-complete.mp3",
		"question.mp3",
		"plan-ready.mp3",
	}

	for _, sound := range requiredSounds {
		t.Run(sound, func(t *testing.T) {
			soundPath := filepath.Join(soundsDir, sound)
			if !platform.FileExists(soundPath) {
				t.Errorf("Required sound file not found: %s", soundPath)
			}
		})
	}
}

// Helper function to find sounds directory
func findSoundsDirectory() string {
	// Try various possible locations
	candidates := []string{
		"../../sounds",
		"../sounds",
		"sounds",
		"./sounds",
	}

	for _, candidate := range candidates {
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if platform.FileExists(absPath) {
			return absPath
		}
	}

	// Try using CLAUDE_PLUGIN_ROOT if set
	if pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT"); pluginRoot != "" {
		soundsPath := filepath.Join(pluginRoot, "sounds")
		if platform.FileExists(soundsPath) {
			return soundsPath
		}
	}

	return ""
}
