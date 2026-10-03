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

type testNotifications struct {
	last atomic.Uint32
	body atomic.Value
}

func (s *testNotifications) Notify(_ string, _ uint32, _ string, _ string, body string, _ []string, _ map[string]dbus.Variant, _ int32) (uint32, *dbus.Error) {
	s.body.Store(body)
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
	// Regression: D-Bus has no native subtitle slot; dropping the subtitle
	// would remove session identification from a concrete-question banner.
	req.Content.Subtitle = "Installer work"
	req.Content.Body = "Use the new installer?"
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
	if server.body.Load() != "Installer work\nUse the new installer?" {
		t.Fatalf("lost subtitle: %v", server.body.Load())
	}
}
