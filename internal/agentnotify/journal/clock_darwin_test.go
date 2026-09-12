//go:build darwin

package journal

import "testing"

// This is an actual OS-port qualification: fake clocks in journal tests cannot
// prove that the shipped Darwin syscall constants and boot identity work.
func TestDarwinPlatformClockAvailableAndStableWithinBoot(t *testing.T) {
	clock := PlatformClock{}
	first, second := clock.Sample(), clock.Sample()
	if !first.Available || !second.Available || first.Boot == "" || second.Boot != first.Boot {
		t.Fatal("Darwin clock did not return a stable available boot identity")
	}
	if second.Seconds < first.Seconds {
		t.Fatal("Darwin monotonic sample regressed within one boot")
	}
}
