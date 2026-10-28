//go:build linux

package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/notification"
)

func TestContextLinuxLocalityRequiresKnownTerminalEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"missing", map[string]string{"DISPLAY": ":0"}, false},
		{"unknown", map[string]string{"DISPLAY": ":0", "TERM_PROGRAM": "TEST-unknown-terminal"}, false},
		{"known_terminal", map[string]string{"DISPLAY": ":0", "TERM_PROGRAM": "kitty"}, true},
		{"known_wayland_terminal", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "TERM_PROGRAM": "WezTerm"}, true},
		{"native_gnome_indicator", map[string]string{"DISPLAY": ":0", "TERM_PROGRAM": "TEST-unknown-terminal", "GNOME_TERMINAL_SCREEN": "native-screen"}, true},
		{"native_window", map[string]string{"DISPLAY": ":0", "TERM_PROGRAM": "TEST-unknown-terminal", "WINDOWID": "12345"}, true},
		{"invalid_window", map[string]string{"DISPLAY": ":0", "WINDOWID": "not-a-window"}, false},
		{"zero_window", map[string]string{"DISPLAY": ":0", "WINDOWID": "0"}, false},
		{"terminal_without_desktop", map[string]string{"TERM_PROGRAM": "kitty"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "WINDOWID", "DISPLAY", "WAYLAND_DISPLAY", "TERM_PROGRAM", "TERMINAL_EMULATOR", "VSCODE_INJECTION", "VSCODE_GIT_IPC_HANDLE", "GNOME_TERMINAL_SCREEN", "GNOME_TERMINAL_SERVICE", "TERMINATOR_UUID", "KONSOLE_VERSION", "KONSOLE_DBUS_SESSION", "ALACRITTY_WINDOW_ID", "WARP_FOCUS_URL", "WARP_TERMINAL_SESSION_UUID", "WARP_IS_LOCAL_SHELL_SESSION"} {
				t.Setenv(key, "")
			}
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			if got := HasLocalTerminalContext(context.Background()); got != test.want {
				t.Fatalf("local=%t, want %t", got, test.want)
			}
		})
	}
}

func TestContextGsettingsStdoutSurvivesSuccessfulDiagnostic(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte("#!/bin/sh\nprintf 'non-fatal diagnostic\\n' >&2\nprintf 'false\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	shown, known := defaultGnomeShowBanners(context.Background())
	if shown || !known {
		t.Fatalf("show-banners=false lost: shown=%t known=%t", shown, known)
	}
}

type contextCapturePort struct {
	calls   int
	request notification.Request
}

func (p *contextCapturePort) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	p.calls++
	p.request = r
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted"}
}

func TestContextLinuxFocusOffUsesExistingActionlessDelivery(t *testing.T) {
	previous := contextPlainLinuxDelivery
	t.Cleanup(func() { contextPlainLinuxDelivery = previous })
	port := &contextCapturePort{}
	contextPlainLinuxDelivery = func(BootClock) notification.DeliveryPort { return port }
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.Sound = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := New(cfg).SendDesktopContext(ctx, analyzer.StatusTaskComplete, "[literal] body", "native-session", t.TempDir(), WithHookPresentation(HookPresentation{SessionName: "Pi", Body: "[literal] body"})); err != nil {
		t.Fatal(err)
	}
	if port.calls != 1 || port.request.Navigation != notification.None || !port.request.Silent || port.request.Content.Body != "[literal] body" || port.request.Target != (notification.DesktopTarget{}) {
		t.Fatalf("request=%+v", port.request)
	}
}

func TestContextLinuxCapturesOriginHintsAndNeverFallsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", root)
	t.Setenv("TERM_PROGRAM", "xterm")
	t.Setenv("WINDOWID", "12345")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("TMUX", "")
	t.Setenv("ZELLIJ", "")
	t.Setenv("TERMINAL_EMULATOR", "")
	listener, err := net.Listen("unix", daemon.GetSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func(listener net.Listener) { _ = listener.Close() }(listener)
	done := make(chan daemon.Request, 1)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if json.NewDecoder(conn).Decode(&req) != nil {
				_ = conn.Close()
				return
			}
			resp := daemon.Response{Type: req.Type}
			if req.Type == daemon.MessageTypePing {
				resp.Ping = &daemon.PingResponse{Version: daemon.ProtocolVersion}
			} else {
				done <- req
				resp.Notify = &daemon.NotifyResponse{Success: true, NotificationID: 19}
			}
			_ = json.NewEncoder(conn).Encode(resp)
			_ = conn.Close()
		}
	}()
	withFatalLinuxBeeep(t)
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cwd := filepath.Join(root, "native-project")
	if err := New(cfg).SendDesktopContext(ctx, analyzer.StatusTaskComplete, "fixed body", "native-session", cwd); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-done:
		if req.Notify.FocusWindowID != "12345" || req.Notify.FocusFolder != "native-project" || req.Notify.FocusTarget != "xterm" {
			t.Fatalf("origin lost: %+v", req.Notify)
		}
	case <-ctx.Done():
		t.Fatal("no notification")
	}
}

func TestContextLinuxHungDaemonCannotOutliveCaller(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", root)
	listener, err := net.Listen("unix", daemon.GetSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func(listener net.Listener) { _ = listener.Close() }(listener)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func(conn net.Conn) { _ = conn.Close() }(conn)
		_, _ = io.Copy(io.Discard, conn)
	}()
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ClickToFocus = true
	cfg.Notifications.Desktop.Sound = false
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := New(cfg).SendDesktopContext(ctx, analyzer.StatusTaskComplete, "fixed", "native-session", root); err == nil || time.Since(started) > time.Second {
		t.Fatalf("hung daemon escaped budget: %v", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("context did not close IPC")
	}
}
