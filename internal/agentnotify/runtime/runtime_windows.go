//go:build windows

package runtime

import (
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func defaultDeliveryFactory(_ notifier.ManagedInstallation, _ string, c notifier.BootClock) Delivery {
	return notifier.NewWindowsToastDelivery(c)
}

// Windows session delivery does not use the macOS native helper.
func sessionDeliveryEligible(installruntime.Ledger) bool { return true }
