//go:build linux

package journal

import (
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// TrustedBootIDPath is the kernel procfs boot identity. Production clocks inject
// this path explicitly; the zero PlatformClock never implies a filesystem root.
const TrustedBootIDPath = "/proc/sys/kernel/random/boot_id"

// PlatformClock requires an explicitly injected boot ID path on Linux (normally
// TrustedBootIDPath). No user HOME/config path is consulted. CLOCK_BOOTTIME
// includes elapsed suspend time and never uses wall time.
type PlatformClock struct{ BootIDPath string }

// DefaultClock is the production Linux journal clock. Tests that need an
// unavailable clock still construct the zero PlatformClock.
func DefaultClock() Clock { return PlatformClock{BootIDPath: TrustedBootIDPath} }

func (c PlatformClock) Sample() Sample {
	boot, sec, _, ok := LinuxBootSample(c.BootIDPath)
	if !ok {
		return Sample{}
	}
	return Sample{Boot: boot, Seconds: uint64(sec), Available: true}
}

// LinuxBootSample reads an injected boot-id file and CLOCK_BOOTTIME. An empty
// path is unavailable and does not open any implicit filesystem root.
func LinuxBootSample(path string) (boot string, sec, nsec int64, ok bool) {
	if path == "" {
		return "", 0, 0, false
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return "", 0, 0, false
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return "", 0, 0, false
	}
	b, e := io.ReadAll(io.LimitReader(f, 258))
	if e != nil || len(b) > 257 {
		return "", 0, 0, false
	}
	boot = strings.TrimSuffix(string(b), "\n")
	if !validText(boot, 256, true) {
		return "", 0, 0, false
	}
	var ts unix.Timespec
	if e = unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); e != nil || ts.Sec < 0 {
		return "", 0, 0, false
	}
	return boot, ts.Sec, ts.Nsec, true
}
