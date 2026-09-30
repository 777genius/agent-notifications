//go:build windows

package notifier

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

func TestWindowsPowerShellToastSessionForwardsSilentPolicy(t *testing.T) {
	previous := submitWindowsToast
	t.Cleanup(func() { submitWindowsToast = previous })
	var got windowsToastPayload
	submitWindowsToast = func(_ context.Context, p windowsToastPayload) error {
		got = p
		return nil
	}
	r := notification.Request{Content: notification.Content{Title: "title", Body: "body"}, Policy: notification.PolicySnapshot{SoundEnabled: false}}
	if err := (windowsPowerShellToastSession{}).Submit(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if !got.Silent || got.Title != r.Content.Title || got.Body != r.Content.Body || got.AppID != windowsToastAppID {
		t.Fatalf("wrong toast payload: %+v", got)
	}
}

func TestOpenCodeToastUsesSeparateIdentityAndRejectsMissingShortcut(t *testing.T) {
	previous := submitWindowsToast
	t.Cleanup(func() { submitWindowsToast = previous })
	var got windowsToastPayload
	submitWindowsToast = func(_ context.Context, p windowsToastPayload) error { got = p; return nil }
	s := windowsPowerShellToastSession{appID: opencodeinstall.OpenCodeToastAppID,
		controlRoot: filepath.Join(t.TempDir(), "control"), executable: filepath.Join(t.TempDir(), "notification.exe"), requireOpenCodeShortcut: true}
	if err := s.Ready(context.Background()); err == nil {
		t.Fatal("missing owned shortcut passed readiness")
	}
	if err := s.Submit(context.Background(), notification.Request{}); err != nil {
		t.Fatal(err)
	}
	if got.AppID != opencodeinstall.OpenCodeToastAppID || got.AppID == windowsToastAppID {
		t.Fatalf("OpenCode toast identity = %q", got.AppID)
	}
}

func TestWindowsPowerShellToastSessionSubmissionHonorsCancellation(t *testing.T) {
	previous := submitWindowsToast
	t.Cleanup(func() { submitWindowsToast = previous })
	started := make(chan struct{})
	submitWindowsToast = func(ctx context.Context, _ windowsToastPayload) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (windowsPowerShellToastSession{}).Submit(ctx, notification.Request{}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("submission did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("submission ignored cancellation")
	}
}

func TestOpenWindowsToastRequiresPowerShell(t *testing.T) {
	previous := resolveWindowsPowerShell
	resolveWindowsPowerShell = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { resolveWindowsPowerShell = previous })
	if session, err := openWindowsToast(context.Background()); !errors.Is(err, os.ErrNotExist) || session != nil {
		t.Fatalf("open = %#v, %v", session, err)
	}
}

func TestSystemPowerShellIgnoresUserPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	got, err := systemWindowsPowerShell()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
	if got != want {
		t.Fatalf("PowerShell path = %q, want %q", got, want)
	}
}
