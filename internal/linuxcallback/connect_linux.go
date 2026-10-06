//go:build linux

package linuxcallback

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// connect uses an explicit same-user Unix session bus. No session autolaunch,
// TCP address, address-list fallback or detached authentication waiter exists.
func connect(ctx context.Context) (*dbus.Conn, error) {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if len(address) > 1024 || !strings.HasPrefix(address, "unix:") || strings.Contains(address, ";") {
		return nil, ErrUnavailable
	}
	fields := map[string]string{}
	for _, item := range strings.Split(strings.TrimPrefix(address, "unix:"), ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok || (key != "path" && key != "abstract" && key != "guid") {
			return nil, ErrUnavailable
		}
		if _, exists := fields[key]; exists {
			return nil, ErrUnavailable
		}
		decoded, e := dbus.UnescapeBusAddressValue(value)
		if e != nil || strings.ContainsAny(decoded, "\x00\r\n") {
			return nil, ErrUnavailable
		}
		fields[key] = decoded
	}
	path := fields["path"]
	if path != "" {
		if !canonical(path) || fields["abstract"] != "" {
			return nil, ErrUnavailable
		}
	} else {
		if fields["abstract"] == "" {
			return nil, ErrUnavailable
		}
		path = "@" + fields["abstract"]
	}
	raw, e := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if e != nil {
		return nil, e
	}
	// Error paths close owned transports best-effort, preserving the rejection.
	peer, ok := raw.(*net.UnixConn)
	if !ok {
		_ = raw.Close()
		return nil, ErrUnavailable
	}
	syscall, e := peer.SyscallConn()
	if e != nil {
		_ = raw.Close()
		return nil, e
	}
	owned := false
	if e = syscall.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		owned = err == nil && int(credential.Uid) == os.Getuid()
	}); e != nil || !owned {
		_ = raw.Close()
		return nil, ErrUnavailable
	}
	// This transport needs no file descriptors. NewConn is the supported generic
	// transport constructor; WithContext closes Auth/Hello on lifetime cancellation.
	conn, e := dbus.NewConn(raw, dbus.WithContext(ctx))
	if e != nil {
		_ = raw.Close()
		return nil, e
	}
	if e = conn.Auth(nil); e != nil {
		_ = conn.Close()
		return nil, e
	}
	if e = conn.Hello(); e != nil {
		_ = conn.Close()
		return nil, e
	}
	return conn, nil
}
