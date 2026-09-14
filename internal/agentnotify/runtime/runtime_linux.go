//go:build linux

package runtime

import (
	"github.com/777genius/agent-notifications/internal/notifier"
)

func defaultDeliveryFactory(_ notifier.ManagedInstallation, _ string, c notifier.BootClock) Delivery {
	return notifier.NewFreedesktopDelivery(c)
}
