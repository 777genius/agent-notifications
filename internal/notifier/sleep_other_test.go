//go:build !darwin

package notifier

import "testing"

// TestDesktopDisplayIsAsleep_UnsupportedPlatform pins the documented contract
// for platforms without a detector: display sleep state is unknown, so
// notifications and their sounds are always delivered. macOS is the only
// implemented platform; see docs/DO_NOT_DISTURB.md.
func TestDesktopDisplayIsAsleep_UnsupportedPlatform(t *testing.T) {
	if desktopDisplayIsAsleep() {
		t.Error("desktopDisplayIsAsleep() = true, want false on a platform with no detector")
	}
	if IsDisplayAsleep() {
		t.Error("IsDisplayAsleep() = true, want false on a platform with no detector")
	}
}
