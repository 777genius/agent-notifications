//go:build !windows

package notifier

import (
	"context"
	"errors"
)

func openWindowsToast(context.Context) (windowsToastSession, error) {
	return nil, errors.New("windows toast unavailable")
}
