//go:build darwin

package journal

import "golang.org/x/sys/unix"

// PlatformClock uses kernel boot-session identity, not boot wall time. A kernel
// without that identity explicitly freezes logical time rather than guessing.
// Darwin execution qualification belongs to the macOS coordinator.
type PlatformClock struct{}

func (PlatformClock) Sample() Sample {
	boot, e := unix.Sysctl("kern.bootsessionuuid")
	if e != nil || !validText(boot, 256, true) {
		return Sample{}
	}
	var ts unix.Timespec
	if e = unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); e != nil || ts.Sec < 0 {
		return Sample{}
	}
	return Sample{Boot: boot, Seconds: uint64(ts.Sec), Available: true}
}
