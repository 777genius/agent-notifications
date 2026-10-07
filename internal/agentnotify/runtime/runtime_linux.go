//go:build linux

package runtime

import (
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func defaultDeliveryFactory(m notifier.ManagedInstallation, _ string, c notifier.BootClock) Delivery {
	return &sessionDelivery{Delivery: notifier.NewFreedesktopDelivery(c), installation: m, clock: c, acquire: installruntime.AcquireInstalledLease}
}

// Linux session delivery does not use the macOS native helper.
func sessionDeliveryEligible(installruntime.Ledger) bool { return true }
