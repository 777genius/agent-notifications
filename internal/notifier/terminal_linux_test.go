//go:build linux

package notifier

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
)

const testHookSwaySocket = "/run/user/1000/sway-ipc.1000.4242.sock"

// serveFakeDaemon answers pings and notifications on the daemon socket of a
// private XDG_RUNTIME_DIR, so no real daemon is started or reached. It returns
// the notification requests it receives.
func serveFakeDaemon(t *testing.T) <-chan daemon.Request {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	listener, err := net.Listen("unix", daemon.GetSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	requests := make(chan daemon.Request, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if json.NewDecoder(conn).Decode(&req) == nil {
				resp := daemon.Response{Type: req.Type}
				if req.Type == daemon.MessageTypePing {
					resp.Ping = &daemon.PingResponse{Version: daemon.ProtocolVersion}
				} else {
					requests <- req
					resp.Notify = &daemon.NotifyResponse{Success: true, NotificationID: 23}
				}
				_ = json.NewEncoder(conn).Encode(resp)
			}
			_ = conn.Close()
		}
	}()
	return requests
}

// assertHookAncestry checks that pids is the ancestry of this test process as
// the hook records it: nearest first, starting with its parent.
func assertHookAncestry(t *testing.T, pids []int) {
	t.Helper()
	parent := os.Getppid()
	if parent <= 1 {
		if len(pids) != 0 {
			t.Fatalf("ancestor pids = %v, want none for a process whose parent is init", pids)
		}
		return
	}
	if len(pids) == 0 || pids[0] != parent {
		t.Fatalf("ancestor pids = %v, want the hook's ancestry starting with its parent %d", pids, parent)
	}
}

// sendViaDaemon is the path Claude Code hooks take. Under Sway it must send the
// Sway socket and the hook's ancestry: several windows of one terminal share an
// app_id, and the daemon tells them apart by the process that owns each window.
func TestSendViaDaemonSwayHints(t *testing.T) {
	for _, tt := range []struct {
		name     string
		swaySock string
	}{
		{"sway", testHookSwaySocket},
		{"none", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := serveFakeDaemon(t)
			t.Setenv("SWAYSOCK", tt.swaySock)
			t.Setenv("XDG_SESSION_TYPE", "wayland")
			t.Setenv("TERMINAL_EMULATOR", "")
			t.Setenv("TMUX", "")
			t.Setenv("ZELLIJ", "")

			cfg := config.DefaultConfig()
			cfg.Notifications.Desktop.ClickToFocus = true
			if err := sendViaDaemon("title", "body", "", filepath.Join(t.TempDir(), "proj"), cfg, true, ""); err != nil {
				t.Fatal(err)
			}

			var req daemon.Request
			select {
			case req = <-requests:
			case <-time.After(5 * time.Second):
				t.Fatal("no notification reached the daemon")
			}
			if req.Notify.FocusSwaySocket != tt.swaySock {
				t.Errorf("FocusSwaySocket = %q, want %q", req.Notify.FocusSwaySocket, tt.swaySock)
			}
			if tt.swaySock == "" {
				if len(req.Notify.FocusAncestorPIDs) != 0 {
					t.Errorf("FocusAncestorPIDs = %v outside Sway, want none", req.Notify.FocusAncestorPIDs)
				}
				return
			}
			assertHookAncestry(t, req.Notify.FocusAncestorPIDs)
		})
	}
}
