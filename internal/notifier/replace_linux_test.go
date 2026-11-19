//go:build linux

package notifier

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/notification"
)

func receiveDaemonRequest(t *testing.T, requests <-chan daemon.Request) *daemon.NotifyRequest {
	t.Helper()
	select {
	case req := <-requests:
		if req.Notify == nil {
			t.Fatal("daemon request has no notify payload")
		}
		return req.Notify
	case <-time.After(5 * time.Second):
		t.Fatal("no notification reached the daemon")
	}
	return nil
}

// The hook path keys a replaceable notification on the session id, with or
// without click-to-focus. Without it the daemon is used only to replace, so the
// request carries no focus hints and asks for no click action.
func TestLinuxReplaceKeyHook(t *testing.T) {
	for _, tt := range []struct {
		name  string
		focus bool
	}{
		{"click-to-focus", true},
		{"replace only", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := serveFakeDaemon(t)
			t.Setenv("TERM_PROGRAM", "xterm")
			t.Setenv("TMUX", "")
			t.Setenv("ZELLIJ", "")
			withFatalLinuxBeeep(t)
			cfg := config.DefaultConfig()
			cfg.Notifications.Desktop.ClickToFocus = tt.focus
			if err := sendLinuxNotification("title", "body", "", cfg, filepath.Join(t.TempDir(), "proj"), tt.focus, "session-a"); err != nil {
				t.Fatal(err)
			}
			req := receiveDaemonRequest(t, requests)
			if req.ReplaceKey != "session-a" || req.NoFocus == tt.focus {
				t.Fatalf("replace_key=%q no_focus=%t, want session-a and %t", req.ReplaceKey, req.NoFocus, !tt.focus)
			}
			if !tt.focus && (req.FocusTarget != "" || req.FocusFolder != "") {
				t.Fatalf("replace-only request carries focus hints: %+v", req)
			}
			if tt.focus && req.FocusTarget != "xterm" {
				t.Fatalf("focus target = %q, want xterm", req.FocusTarget)
			}
		})
	}
}

// SendDesktop sends a replaceable notification through the daemon only when
// the option is on and the session id is known.
func TestLinuxReplaceKeyConfig(t *testing.T) {
	enabled, disabled := true, false
	for _, tt := range []struct {
		name      string
		replace   *bool
		sessionID string
		wantKey   string
	}{
		{"enabled", &enabled, " session-a ", "session-a"},
		{"unknown session", &enabled, "unknown", ""},
		{"empty session", &enabled, "", ""},
		{"disabled", &disabled, "session-a", ""},
		{"default", nil, "session-a", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := serveFakeDaemon(t)
			t.Setenv("TMUX", "")
			t.Setenv("ZELLIJ", "")
			withFatalLinuxBeeep(t)
			cfg := config.DefaultConfig()
			cfg.Notifications.Desktop.ClickToFocus = true
			cfg.Notifications.Desktop.Sound = false
			cfg.Notifications.ReplaceNotificationsPerSession = tt.replace
			if err := New(cfg).SendDesktop(analyzer.StatusTaskComplete, "body", tt.sessionID, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			if got := receiveDaemonRequest(t, requests).ReplaceKey; got != tt.wantKey {
				t.Fatalf("replace_key = %q, want %q", got, tt.wantKey)
			}
		})
	}
}

// The context path (Pi) also routes a replaceable notification through the
// daemon when click-to-focus is off, instead of the one-shot D-Bus delivery
// that cannot know the session's previous notification.
func TestLinuxReplaceKeyContext(t *testing.T) {
	previous := contextPlainLinuxDelivery
	t.Cleanup(func() { contextPlainLinuxDelivery = previous })
	port := &contextCapturePort{}
	contextPlainLinuxDelivery = func(BootClock) notification.DeliveryPort { return port }
	requests := serveFakeDaemon(t)
	withFatalLinuxBeeep(t)
	enabled := true
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.Sound = false
	cfg.Notifications.ReplaceNotificationsPerSession = &enabled
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := New(cfg).SendDesktopContext(ctx, analyzer.StatusTaskComplete, "body", "native-session", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	req := receiveDaemonRequest(t, requests)
	if req.ReplaceKey != "native-session" || !req.NoFocus || req.FocusTarget != "" {
		t.Fatalf("request = %+v, want replace key native-session without focus", req)
	}
	if port.calls != 0 {
		t.Fatalf("one-shot delivery also used (%d calls)", port.calls)
	}
}
