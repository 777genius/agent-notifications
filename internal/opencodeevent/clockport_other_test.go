//go:build !linux && !darwin && !windows

package opencodeevent

import "testing"

// Regression: adding a process-relative or wall-clock fallback on an unknown
// platform manufactures a shared boot coordinate instead of refusing it.
func TestClockSnapshotUnsupportedPlatform(t *testing.T) {
	if s, err := NewSystemSnapshotPort().SampleSnapshot(); err != ErrClockUnavailable || s != (ClockSnapshot{}) {
		t.Fatal(s, err)
	}
}
