//go:build !windows

package notifier

import (
	"context"
	"errors"
	"github.com/777genius/agent-notifications/internal/notification"
)

func openWindowsToast(context.Context) (windowsToastSession, error) {
	return nil, errors.New("windows toast unavailable")
}

func openWindowsNavigation(context.Context, notification.Request) (windowsToastSession, error) {
	return nil, errors.New("windows callback unavailable")
}
