//go:build windows

package notifier

import (
	"context"
	"runtime"

	toast "git.sr.ht/~jackmordaunt/go-toast"

	"github.com/777genius/agent-notifications/internal/notification"
)

type goToastSession struct{}

func openWindowsToast(context.Context) (windowsToastSession, error) {
	return goToastSession{}, nil
}

func (goToastSession) Ready(ctx context.Context) error { return ctx.Err() }

func (goToastSession) Submit(ctx context.Context, r notification.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n := toast.Notification{
		AppID: windowsToastAppID,
		Title: r.Content.Title,
		Body:  r.Content.Body,
	}
	runtime.LockOSThread()
	err := n.Push()
	runtime.UnlockOSThread()
	if toastDeliveredDespiteError(err) {
		return nil
	}
	return err
}

func (goToastSession) Close() error { return nil }
