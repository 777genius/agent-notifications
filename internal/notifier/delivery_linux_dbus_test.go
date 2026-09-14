//go:build linux

package notifier

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testNotifications struct{ last uint32 }

func (s *testNotifications) Notify(string, uint32, string, string, string, []string, map[string]dbus.Variant, int32) (uint32, *dbus.Error) {
	s.last++
	return s.last, nil
}

func startTestSessionBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon not installed")
	}
	cmd := exec.Command("dbus-daemon", "--nofork", "--session", "--print-address")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimSpace(line)
	if addr == "" {
		t.Fatal("empty bus address")
	}
	return addr
}

func TestFreedesktopSessionBusSubmit(t *testing.T) {
	addr := startTestSessionBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	clock := &pr3Clock{now: 100}
	d := NewFreedesktopDelivery(clock)
	req := linuxNoneRequest(clock)
	got := d.CheckReadiness(context.Background(), req)
	if got.Status != "rejected" || got.Reason != "unsupported_notifier" {
		t.Fatal("missing Notifications name", got)
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	server := &testNotifications{}
	if err = conn.Export(server, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal(reply, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ready := d.CheckReadiness(ctx, req)
	if ready.Status != "ready" || ready.Reason != "permission_authorized" {
		t.Fatal(ready)
	}
	receipt := d.Deliver(ctx, req)
	if receipt.Status != "submitted" || receipt.Reason != "session_notification" || server.last != 1 {
		t.Fatal(receipt, server.last)
	}
}
