//go:build linux

package main

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// The gate wrapper owns the single retained lease; this backend is bare.
func newCursorDesktopPort() notification.DeliveryPort {
	return notifier.NewFreedesktopDelivery(notifier.SystemBootClock{})
}
