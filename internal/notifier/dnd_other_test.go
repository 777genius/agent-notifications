//go:build !linux

package notifier

import "testing"

// TestDesktopIsDoNotDisturb_UnsupportedPlatform pins the documented contract for
// platforms without a detector: DND state is unknown, so notifications and their
// sounds are always delivered. macOS and Windows are tracked in
// docs/DO_NOT_DISTURB.md.
func TestDesktopIsDoNotDisturb_UnsupportedPlatform(t *testing.T) {
	if desktopIsDoNotDisturb() {
		t.Error("desktopIsDoNotDisturb() = true, want false on a platform with no detector")
	}
	if IsDoNotDisturb() {
		t.Error("IsDoNotDisturb() = true, want false on a platform with no detector")
	}
}
