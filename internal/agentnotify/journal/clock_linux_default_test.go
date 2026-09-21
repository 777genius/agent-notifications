//go:build linux

package journal

import "testing"

func TestLinuxDefaultClockUsesTrustedBootIDPath(t *testing.T) {
	clock, ok := DefaultClock().(PlatformClock)
	if !ok || clock.BootIDPath != TrustedBootIDPath {
		t.Fatalf("default clock = %#v", DefaultClock())
	}
}
