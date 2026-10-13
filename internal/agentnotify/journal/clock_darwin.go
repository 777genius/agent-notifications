//go:build darwin

package journal

import "golang.org/x/sys/unix"

// PlatformClock uses kernel boot-session identity, not boot wall time. A kernel
// without that identity explicitly freezes logical time rather than guessing.
// Darwin execution qualification belongs to the macOS coordinator.
type PlatformClock struct{}

// DefaultClock is the production Darwin journal clock.
func DefaultClock() Clock { return PlatformClock{} }

func (PlatformClock) Sample() Sample {
	boot, sec, _, ok := DarwinBootSample()
	if !ok {
		return Sample{}
	}
	return Sample{Boot: boot, Seconds: uint64(sec), Available: true}
}

// DarwinBootSample preserves the existing continuous CLOCK_MONOTONIC_RAW epoch
// and boot-session identity, exposing its integer sec/nsec without new FFI.
func DarwinBootSample() (boot string, sec, nsec int64, ok bool) {
	boot, e := unix.Sysctl("kern.bootsessionuuid")
	if e != nil || !validText(boot, 256, true) {
		return "", 0, 0, false
	}
	var ts unix.Timespec
	if e = unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); e != nil || ts.Sec < 0 {
		return "", 0, 0, false
	}
	return boot, ts.Sec, ts.Nsec, true
}
