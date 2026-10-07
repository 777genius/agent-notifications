//go:build linux

package linuxcallback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// Initial supported stack is the two exact source-bound builds qualified in
// the selected-client native capsule. Name/interface versions alone never opt
// in another distro build; this does not install these runtimes for the user.
var stackPins = map[string]string{
	frontend: "7fe62c1a938985b8ca4ec335a768ad36f1d17abe027098624fa4f5989c0b4594",
	backend:  "b95c473ae8fe4e3b51e7ca4bf27d4f4719786d552524443e246d1b40468d40af",
}

func birth(pid uint32) (string, error) {
	p := "/proc/" + strconv.FormatUint(uint64(pid), 10)
	b, e := os.ReadFile(p + "/stat")
	if e != nil || len(b) > 16384 {
		return "", ErrUnavailable
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", ErrUnavailable
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) <= 19 {
		return "", ErrUnavailable
	}
	info, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return "", ErrUnavailable
	}
	return fields[19], nil
}
func qualifiedStack(ctx context.Context, c *dbus.Conn, o Owners) error {
	for name, unique := range map[string]string{frontend: o.Frontend, backend: o.GTK} {
		var pid uint32
		if e := c.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, unique).Store(&pid); e != nil {
			return e
		}
		first, e := birth(pid)
		if e != nil {
			return e
		}
		fd, e := unix.PidfdOpen(int(pid), 0)
		if e != nil {
			return ErrUnavailable
		}
		check := func() error {
			defer func() { _ = unix.Close(fd) }() // Held identity observation is complete before cleanup.
			procPath := "/proc/" + strconv.FormatUint(uint64(pid), 10) + "/exe"
			selected, e := os.Readlink(procPath)
			if e != nil || !canonical(selected) {
				return ErrUnavailable
			}
			if e = vendorAncestors(selected); e != nil {
				return e
			}
			info, e := os.Lstat(selected)
			if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 32<<20 {
				return ErrUnavailable
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || st.Uid != 0 {
				return ErrUnavailable
			}
			f, e := os.Open(procPath)
			if e != nil {
				return e
			}
			defer func() { _ = f.Close() }() // Read-only executable observation.
			opened, e := f.Stat()
			if e != nil || !os.SameFile(info, opened) {
				return ErrUnavailable
			}
			h := sha256.New()
			buffer := make([]byte, 64<<10)
			var total int64
			for {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				n, re := f.Read(buffer)
				total += int64(n)
				if total > 32<<20 {
					return ErrUnavailable
				}
				if n > 0 {
					_, _ = h.Write(buffer[:n])
				}
				if re == io.EOF {
					break
				}
				if re != nil {
					return re
				}
			}
			if hex.EncodeToString(h.Sum(nil)) != stackPins[name] {
				return ErrUnavailable
			}
			second, e := birth(pid)
			if e != nil || first != second {
				return ErrUnavailable
			}
			var current string
			if e = c.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, name).Store(&current); e != nil || current != unique {
				return ErrUnavailable
			}
			events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, e := unix.Poll(events, 0); e != nil || n != 0 {
				return ErrUnavailable
			}
			return nil
		}
		// The digest establishes executable identity; transport names are not file names.
		if e = check(); e != nil {
			return e
		}
	}
	return nil
}
