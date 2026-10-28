//go:build linux

package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
	"github.com/777genius/agent-notifications/internal/notification"
)

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
	defer listener.Close()
	done := make(chan daemon.Request, 1)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if json.NewDecoder(conn).Decode(&req) != nil {
				conn.Close()
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
			conn.Close()
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
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
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
