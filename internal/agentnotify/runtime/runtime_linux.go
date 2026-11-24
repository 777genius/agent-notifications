//go:build linux

package runtime

import (
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func defaultDeliveryFactory(m notifier.ManagedInstallation, _ string, c notifier.BootClock) Delivery {
	port := notifier.NewFreedesktopDelivery(c)
	port.AgentNotifyPresentation = true
	return &sessionDelivery{Delivery: port, installation: m, clock: c, acquire: installruntime.AcquireInstalledLease}
}

// Linux session delivery does not use the macOS native helper.
func sessionDeliveryEligible(installruntime.Ledger) bool { return true }
