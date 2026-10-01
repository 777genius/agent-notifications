//go:build linux

package main

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
)

func newOpenCodeDesktopPort(_ *opencodeevent.Handoff, _ string) notification.DeliveryPort {
	return notifier.NewFreedesktopDelivery(notifier.SystemBootClock{})
}
