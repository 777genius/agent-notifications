//go:build linux

package main

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func newOpenCodeDesktopPort() notification.DeliveryPort {
	return notifier.NewFreedesktopDelivery(notifier.SystemBootClock{})
}
