//go:build windows

package journal

import "testing"

func TestWindowsPlatformClockAvailableAndStableWithinBoot(t *testing.T) {
	a, b := (PlatformClock{}).Sample(), (PlatformClock{}).Sample()
	if !a.Available || a.Boot == "" || b.Boot != a.Boot || b.Seconds < a.Seconds {
		t.Fatal(a, b)
	}
	boot, sec, nsec, ok := WindowsBootSample()
	if !ok || boot != a.Boot || sec < 0 || nsec < 0 {
		t.Fatal(boot, sec, nsec, ok)
	}
}
