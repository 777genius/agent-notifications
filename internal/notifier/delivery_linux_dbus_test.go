//go:build linux

package notifier

import (
	"context"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testNotifications struct{ last atomic.Uint32 }

func (s *testNotifications) Notify(string, uint32, string, string, string, []string, map[string]dbus.Variant, int32) (uint32, *dbus.Error) {
	return s.last.Add(1), nil
}

func startTestSessionBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--fork", "--print-address=1", "--print-pid=1")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("cannot start dbus-daemon: %v", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 {
		t.Fatalf("dbus-daemon output %q", out)
	}
	t.Cleanup(func() { _ = exec.Command("kill", lines[1]).Run() })
	return lines[0]
}

func TestFreedesktopSessionBusSubmit(t *testing.T) {
	address := startTestSessionBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	server := &testNotifications{}
	if _, err = conn.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if err = conn.Export(server, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	clock := &pr3Clock{now: 100}
	d := NewFreedesktopDelivery(clock)
	req := linuxNoneRequest(clock)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready := d.CheckReadiness(ctx, req)
	if ready.Status != "ready" {
		t.Fatal(ready)
	}
	receipt := d.Deliver(ctx, req)
	if receipt.Status != "submitted" || receipt.Reason != "session_notification" || server.last.Load() != 1 {
		t.Fatal(receipt, server.last.Load())
	}
}
