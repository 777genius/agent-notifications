//go:build windows

package notifier

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
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
	previous := lookWindowsPowerShell
	lookWindowsPowerShell = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { lookWindowsPowerShell = previous })
	if session, err := openWindowsToast(context.Background()); !errors.Is(err, exec.ErrNotFound) || session != nil {
		t.Fatalf("open = %#v, %v", session, err)
	}
}
