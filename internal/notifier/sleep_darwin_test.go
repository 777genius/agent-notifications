//go:build darwin

package notifier

import "testing"

func TestEvaluateDisplaySleep(t *testing.T) {
	tests := []struct {
		name   string
		states []bool
		want   bool
	}{
		{"no displays found", nil, false},
		{"single display awake", []bool{false}, false},
		{"single display asleep", []bool{true}, true},
		{"all asleep", []bool{true, true}, true},
		{"one awake among several asleep (clamshell + external monitor)", []bool{true, false}, false},
		{"all awake", []bool{false, false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluateDisplaySleep(tt.states); got != tt.want {
				t.Errorf("evaluateDisplaySleep(%v) = %v, want %v", tt.states, got, tt.want)
			}
		})
	}
}

func TestDesktopDisplayIsAsleep_Darwin(t *testing.T) {
	restore := activeDisplaySleepStates
	defer func() { activeDisplaySleepStates = restore }()

	t.Run("mutes when every display reports asleep", func(t *testing.T) {
		activeDisplaySleepStates = func() ([]bool, bool) { return []bool{true, true}, true }
		if !desktopDisplayIsAsleep() {
			t.Error("desktopDisplayIsAsleep() = false, want true when every display is asleep")
		}
	})

	t.Run("does not mute when any display is awake", func(t *testing.T) {
		activeDisplaySleepStates = func() ([]bool, bool) { return []bool{true, false}, true }
		if desktopDisplayIsAsleep() {
			t.Error("desktopDisplayIsAsleep() = true, want false when a display is awake")
		}
	})

	t.Run("fails open when the query errors", func(t *testing.T) {
		activeDisplaySleepStates = func() ([]bool, bool) { return nil, false }
		if desktopDisplayIsAsleep() {
			t.Error("desktopDisplayIsAsleep() = true, want false when the display query fails")
		}
	})
}
