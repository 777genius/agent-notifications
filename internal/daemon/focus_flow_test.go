//go:build linux

package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/esiqveland/notify"
)

type focusFlowNotifier struct{ notify.Notifier }

func (focusFlowNotifier) SendNotification(notify.Notification) (uint32, error) {
	return 17, nil
}

// Exercise the real client/server mapping and click callback without D-Bus or
// desktop commands. Warp raises the window before Zellij selects its target.
func TestFocusFlowPreservesWarpAndZellij(t *testing.T) {
	for _, mode := range []string{ZellijFocusModePane, ZellijFocusModeTab} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			trace := filepath.Join(dir, "trace")
			script := "#!/bin/sh\nprintf 'zellij:%s\\n' \"$*\" >> \"$FOCUS_TEST_TRACE\"\n"
			if err := os.WriteFile(filepath.Join(dir, "zellij"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("FOCUS_TEST_TRACE", trace)
			hints := FocusHints{
				TerminalName: "WarpTerminal", FolderName: "sandbox", WindowID: "123", WindowTitle: "sandbox window",
				WezTermPaneID: "stale", WezTermSocket: "stale-socket",
				WarpFocusURL:  "warp://session/6b7be92641ae8ced80188a4d87e4b200",
				ZellijSession: "sandbox-session", ZellijPaneID: "2", ZellijTabName: "sandbox-tab", ZellijMode: mode,
			}
			original := openFocusURL
			t.Cleanup(func() { openFocusURL = original })
			openFocusURL = func(url string) error {
				if url != hints.WarpFocusURL {
					t.Errorf("opened %q, want %q", url, hints.WarpFocusURL)
				}
				return os.WriteFile(trace, []byte("warp\n"), 0600)
			}
			socket := filepath.Join(dir, "daemon.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			server := &Server{notifier: focusFlowNotifier{}, focusCtx: make(map[uint32]FocusHints)}
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				server.wg.Add(1)
				server.handleConnection(conn)
				done <- nil
			}()
			client := &Client{socketPath: socket}
			response, err := client.SendNotification("title", "body", hints, 30)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("server did not complete request")
			}
			if !response.Success || response.NotificationID != 17 {
				t.Fatalf("response = %+v", response)
			}
			if got := server.focusCtx[17]; got != hints {
				t.Fatalf("stored hints = %+v, want %+v", got, hints)
			}
			server.onActionInvoked(&notify.ActionInvokedSignal{ID: 17, ActionKey: "default"})
			got, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			action, target := "focus-pane-id", hints.ZellijPaneID
			if mode == ZellijFocusModeTab {
				action, target = "go-to-tab-name", hints.ZellijTabName
			}
			want := "warp\nzellij:-s " + hints.ZellijSession + " action " + action + " " + target + "\n"
			if string(got) != want {
				t.Fatalf("focus order = %q, want %q", got, want)
			}
			if _, exists := server.focusCtx[17]; exists {
				t.Error("click context was not cleared")
			}
		})
	}
}
