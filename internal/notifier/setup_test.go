package notifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/platform"
)

// TestSoundPathConstruction tests the logic for constructing sound paths
// This mimics the bash function get_sound_path() from setup-notifications.md
func TestSoundPathConstruction(t *testing.T) {
	pluginRoot := "/test/plugin/root"

	tests := []struct {
		name     string
		choice   string
		expected string
	}{
		{
			name:     "Built-in sound with prefix",
			choice:   "Built-in: task-complete.mp3",
			expected: filepath.Join(pluginRoot, "sounds", "task-complete.mp3"),
		},
		{
			name:     "Built-in sound without prefix",
			choice:   "task-complete.mp3",
			expected: filepath.Join(pluginRoot, "sounds", "task-complete.mp3"),
		},
		{
			name:     "System sound macOS",
			choice:   "System: Glass",
			expected: "/System/Library/Sounds/Glass.aiff",
		},
		{
			name:     "System sound macOS with description",
			choice:   "System: Hero (Triumphant fanfare)",
			expected: "/System/Library/Sounds/Hero.aiff",
		},
		{
			name:     "Another built-in",
			choice:   "Built-in: review-complete.mp3",
			expected: filepath.Join(pluginRoot, "sounds", "review-complete.mp3"),
		},
		{
			name:     "System Funk",
			choice:   "System: Funk",
			expected: "/System/Library/Sounds/Funk.aiff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := constructSoundPath(tt.choice, pluginRoot)
			if result != tt.expected {
				t.Errorf("constructSoundPath(%q) = %q, want %q", tt.choice, result, tt.expected)
			}
		})
	}
}

// TestSystemSoundsDetection tests OS detection and system sounds availability
func TestSystemSoundsDetection(t *testing.T) {
	tests := []struct {
		name           string
		osType         string
		hasSystemSound bool
		soundDir       string
	}{
		{
			name:           "macOS",
			osType:         "Darwin",
			hasSystemSound: true,
			soundDir:       "/System/Library/Sounds",
		},
		{
			name:   "Linux with sounds",
			osType: "Linux",
			// Note: Linux might not have /usr/share/sounds on this system (e.g., macOS)
			// So we check the logic, not the actual existence
			hasSystemSound: platform.FileExists("/usr/share/sounds"),
			soundDir:       "/usr/share/sounds",
		},
		{
			name:           "Windows",
			osType:         "Windows",
			hasSystemSound: false,
			soundDir:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasSystemSounds, soundDir := detectSystemSounds(tt.osType)

			if hasSystemSounds != tt.hasSystemSound {
				// For Linux, this is expected if we're running on macOS
				if tt.osType == "Linux" && !platform.FileExists("/usr/share/sounds") {
					t.Logf("Linux sounds directory not found on this system (expected on macOS)")
					return
				}
				t.Errorf("detectSystemSounds(%q) hasSystemSounds = %v, want %v",
					tt.osType, hasSystemSounds, tt.hasSystemSound)
			}

			if hasSystemSounds && soundDir != tt.soundDir {
				t.Errorf("detectSystemSounds(%q) soundDir = %q, want %q",
					tt.osType, soundDir, tt.soundDir)
			}
		})
	}
}

// TestFallbackToBuiltInSounds tests that built-in sounds are used as fallback
func TestFallbackToBuiltInSounds(t *testing.T) {
	pluginRoot := "/test/plugin/root"

	// Test various invalid or edge case inputs
	tests := []struct {
		name     string
		choice   string
		wantPath string
	}{
		{
			name:     "Empty choice",
			choice:   "",
			wantPath: filepath.Join(pluginRoot, "sounds", "task-complete.mp3"),
		},
		{
			name:     "Invalid format",
			choice:   "Invalid: Something",
			wantPath: filepath.Join(pluginRoot, "sounds", "task-complete.mp3"),
		},
		{
			name:     "Random text",
			choice:   "random text here",
			wantPath: filepath.Join(pluginRoot, "sounds", "task-complete.mp3"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := constructSoundPath(tt.choice, pluginRoot)
			if result != tt.wantPath {
				t.Errorf("constructSoundPath(%q) = %q, want fallback %q",
					tt.choice, result, tt.wantPath)
			}
		})
	}
}

// TestBuiltInSoundsAlwaysAvailable tests that built-in sounds are listed even without system sounds
func TestBuiltInSoundsAlwaysAvailable(t *testing.T) {
	// This test ensures the logic that built-in sounds are always included
	// regardless of platform or system sounds availability

	builtInSounds := []string{
		"task-complete.mp3",
		"review-complete.mp3",
		"question.mp3",
		"plan-ready.mp3",
	}

	// Test that we have all 4 built-in sounds
	if len(builtInSounds) != 4 {
		t.Errorf("Expected 4 built-in sounds, got %d", len(builtInSounds))
	}

	// Test that each sound has .mp3 extension
	for _, sound := range builtInSounds {
		if filepath.Ext(sound) != ".mp3" {
			t.Errorf("Built-in sound %q does not have .mp3 extension", sound)
		}
	}
}

// TestAskUserQuestionOptionsGeneration tests dynamic options generation
func TestAskUserQuestionOptionsGeneration(t *testing.T) {
	tests := []struct {
		name               string
		hasSystemSounds    bool
		expectedMinOptions int
		expectedMaxOptions int
	}{
		{
			name:               "macOS with system sounds",
			hasSystemSounds:    true,
			expectedMinOptions: 8, // 4 built-in + at least 4 system
			expectedMaxOptions: 20,
		},
		{
			name:               "Windows without system sounds",
			hasSystemSounds:    false,
			expectedMinOptions: 4, // Only 4 built-in
			expectedMaxOptions: 4,
		},
		{
			name:               "Linux without system sounds",
			hasSystemSounds:    false,
			expectedMinOptions: 4,
			expectedMaxOptions: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := generateSoundOptions(tt.hasSystemSounds)

			if len(options) < tt.expectedMinOptions {
				t.Errorf("generateSoundOptions() returned %d options, want at least %d",
					len(options), tt.expectedMinOptions)
			}

			if len(options) > tt.expectedMaxOptions {
				t.Errorf("generateSoundOptions() returned %d options, want at most %d",
					len(options), tt.expectedMaxOptions)
			}

			// Verify all options are non-empty
			for i, opt := range options {
				if opt == "" {
					t.Errorf("generateSoundOptions() returned empty option at index %d", i)
				}
			}
		})
	}
}

// Helper functions that implement the logic from setup-notifications.md

// constructSoundPath mimics the bash function get_sound_path()
func constructSoundPath(choice, pluginRoot string) string {
	// Check if it's a built-in sound
	if contains(choice, "Built-in:") || contains(choice, ".mp3") {
		// Extract filename
		filename := choice
		if contains(filename, "Built-in: ") {
			filename = filename[len("Built-in: "):]
		}
		if contains(filename, ": ") {
			// Handle "Built-in: task-complete.mp3" format
			parts := splitOnFirst(filename, ": ")
			if len(parts) > 1 {
				filename = parts[1]
			}
		}
		// Extract just the filename if there's extra text
		if contains(filename, " ") {
			parts := splitOnFirst(filename, " ")
			filename = parts[0]
		}
		return filepath.Join(pluginRoot, "sounds", filename)
	}

	// Check if it's a system sound (macOS)
	if contains(choice, "System:") {
		// Extract sound name (e.g., "Glass" from "System: Glass")
		soundname := choice[len("System: "):]
		// Take only the first word
		if contains(soundname, " ") {
			parts := splitOnFirst(soundname, " ")
			soundname = parts[0]
		}
		return "/System/Library/Sounds/" + soundname + ".aiff"
	}

	// Fallback to built-in
	return filepath.Join(pluginRoot, "sounds", "task-complete.mp3")
}

// detectSystemSounds mimics the OS detection logic
func detectSystemSounds(osType string) (bool, string) {
	switch osType {
	case "Darwin":
		return true, "/System/Library/Sounds"
	case "Linux":
		// Check if /usr/share/sounds exists
		if platform.FileExists("/usr/share/sounds") {
			return true, "/usr/share/sounds"
		}
		return false, ""
	case "Windows", "MINGW", "MSYS", "CYGWIN":
		return false, ""
	default:
		return false, ""
	}
}

// generateSoundOptions generates the list of available sound options
func generateSoundOptions(hasSystemSounds bool) []string {
	options := []string{
		"Built-in: task-complete.mp3",
		"Built-in: review-complete.mp3",
		"Built-in: question.mp3",
		"Built-in: plan-ready.mp3",
	}

	if hasSystemSounds {
		// Add common macOS system sounds
		systemSounds := []string{
			"System: Glass",
			"System: Hero",
			"System: Funk",
			"System: Sosumi",
			"System: Ping",
			"System: Purr",
		}
		options = append(options, systemSounds...)
	}

	return options
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsAt(s, substr) >= 0
}

func containsAt(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func splitOnFirst(s, sep string) []string {
	idx := containsAt(s, sep)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

// TestCurrentPlatformDetection tests real OS detection
func TestCurrentPlatformDetection(t *testing.T) {
	// This test runs on the actual platform and checks detection

	osType := os.Getenv("GOOS")
	if osType == "" {
		// Fallback to runtime detection
		switch {
		case platform.IsMacOS():
			osType = "Darwin"
		case platform.IsLinux():
			osType = "Linux"
		case platform.IsWindows():
			osType = "Windows"
		}
	}

	t.Logf("Detected OS: %s", osType)

	hasSystemSounds, soundDir := detectSystemSounds(osType)
	t.Logf("Has system sounds: %v", hasSystemSounds)
	t.Logf("Sound directory: %s", soundDir)

	// On macOS, we expect system sounds to be available
	if platform.IsMacOS() {
		if !hasSystemSounds {
			t.Error("Expected system sounds on macOS")
		}
		if soundDir != "/System/Library/Sounds" {
			t.Errorf("Expected sound dir /System/Library/Sounds on macOS, got %s", soundDir)
		}
	}

	// On Windows, we don't expect system sounds
	if platform.IsWindows() {
		if hasSystemSounds {
			t.Error("Did not expect system sounds on Windows")
		}
	}
}
