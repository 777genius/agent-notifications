//go:build windows

package main

import (
	"context"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

// The Cursor gate retains the one lease; this backend performs the OS effect.
func newCursorDesktopPort(b portable.Binding, executable string) notification.DeliveryPort {
	key, _, _, err := b.Registration()
	if err != nil {
		return nil
	}
	return notifier.NewTrustedWindowsToastDelivery(notifier.SystemBootClock{}, opencodeinstall.CursorToastAppID, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return opencodeinstall.WindowsCursorShortcutReady(key, b.ControlRoot, executable)
	})
}
