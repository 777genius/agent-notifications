//go:build linux

package notifier

import (
	"context"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/godbus/dbus/v5"
)

type testNotifications struct {
	last            atomic.Uint32
	body            atomic.Value
	title           atomic.Value
	actions         atomic.Value
	silent          atomic.Bool
	capabilities    []string
	capabilityError *dbus.Error
	capabilityCalls atomic.Uint32
}

func (s *testNotifications) GetCapabilities() ([]string, *dbus.Error) {
	s.capabilityCalls.Add(1)
	if s.capabilityError != nil {
		return nil, s.capabilityError
	}
	if s.capabilities == nil {
		return []string{}, nil
	}
	return s.capabilities, nil
}

func (s *testNotifications) Notify(_ string, _ uint32, _ string, title string, body string, actions []string, hints map[string]dbus.Variant, _ int32) (uint32, *dbus.Error) {
	s.body.Store(body)
	s.title.Store(title)
	s.actions.Store(actions)
	s.silent.Store(hints["suppress-sound"].Value() == true)
	return s.last.Add(1), nil
}

// Regression: XML-capable servers interpret question/session text as markup
// (including links), while escaping plaintext servers would display entities.
// Assert the actual transport body for the same literal context under both.
func TestFreedesktopLiteralContextMatchesServerMarkupCapability(t *testing.T) {
	const title = `❓ Use <a> & keep tags?`
	const subtitle = `Installer <b>work</b> & SDK`
	const question = `Use <a href="https://example.invalid/">new</a> installer > old & keep <b>tags</b>?`
	for _, tc := range []struct {
		name         string
		capabilities []string
		expected     string
		unavailable  bool
	}{
		{"plaintext", []string{"body"}, subtitle + "\n" + question, false},
		{"markup", []string{"body", "body-markup"}, `Installer &lt;b&gt;work&lt;/b&gt; &amp; SDK` + "\n" + `Use &lt;a href=&#34;https://example.invalid/&#34;&gt;new&lt;/a&gt; installer &gt; old &amp; keep &lt;b&gt;tags&lt;/b&gt;?`, false},
		{"unavailable", nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", startTestSessionBus(t))
			conn, err := dbus.ConnectSessionBus()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			server := &testNotifications{capabilities: tc.capabilities}
			if tc.unavailable {
				server.capabilityError = dbus.NewError("org.freedesktop.DBus.Error.Failed", []any{"private daemon error"})
			}
			if _, err = conn.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue); err != nil {
				t.Fatal(err)
			}
			if err = conn.Export(server, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if tc.unavailable {
				// Unknown markup support must reject before the external effect,
				// and the daemon error must never reach the generic receipt.
				clock := &pr3Clock{now: 100}
				request := linuxNoneRequest(clock)
				request.Content.Subtitle, request.Content.Body = subtitle, question
				receipt := NewFreedesktopDelivery(clock).Deliver(ctx, request)
				if receipt.Status != "rejected" || receipt.Reason != "unsupported_notifier" || server.last.Load() != 0 || server.capabilityCalls.Load() != 1 {
					t.Fatalf("unverified capability reached delivery: receipt=%+v calls=%d capabilities=%d", receipt, server.last.Load(), server.capabilityCalls.Load())
				}
				return
			}
			session, err := openSessionNotifications(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			if err = session.Ready(ctx); err != nil {
				t.Fatal(err)
			}
			if err = session.Ready(ctx); err != nil {
				t.Fatal(err)
			}
			request := notification.Request{Content: notification.Content{Title: title, Subtitle: subtitle, Body: question}, Silent: true}
			for i := 0; i < 2; i++ {
				if _, err = session.Submit(ctx, request); err != nil {
					t.Fatal(err)
				}
				if server.body.Load() != tc.expected {
					t.Fatalf("body = %q, want %q", server.body.Load(), tc.expected)
				}
				if server.title.Load() != title {
					t.Fatalf("summary modified: %v", server.title.Load())
				}
				if len(server.actions.Load().([]string)) != 0 || !server.silent.Load() {
					t.Fatal("actions or silent policy changed")
				}
			}
			if server.capabilityCalls.Load() != 1 {
				t.Fatalf("capability queries = %d, want one per connection", server.capabilityCalls.Load())
			}
		})
	}
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
