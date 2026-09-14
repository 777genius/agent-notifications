//go:build linux

package journal

import (
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// PlatformClock requires an explicitly injected boot ID path on Linux (normally
// the trusted procfs boot_id file). No user HOME/config path is consulted.
// CLOCK_BOOTTIME includes elapsed suspend time and never uses wall time.
type PlatformClock struct{ BootIDPath string }

func (c PlatformClock) Sample() Sample {
	if c.BootIDPath == "" {
		return Sample{}
	}
	fd, e := unix.Open(c.BootIDPath, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return Sample{}
	}
	f := os.NewFile(uintptr(fd), c.BootIDPath)
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return Sample{}
	}
	b, e := io.ReadAll(io.LimitReader(f, 258))
	if e != nil || len(b) > 257 {
		return Sample{}
	}
	boot := strings.TrimSuffix(string(b), "\n")
	if !validText(boot, 256, true) {
		return Sample{}
	}
	var ts unix.Timespec
	if e = unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); e != nil || ts.Sec < 0 {
		return Sample{}
	}
	return Sample{Boot: boot, Seconds: uint64(ts.Sec), Available: true}
}
