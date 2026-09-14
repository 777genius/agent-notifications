//go:build !linux

package runtime

import (
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func defaultDeliveryFactory(m notifier.ManagedInstallation, p string, c notifier.BootClock) Delivery {
	return &notifier.StructuredDelivery{Installation: m, Clock: c, Process: notifier.ManagedNativeProcess{}, Spool: &notifier.PrivateNativeSpool{Root: p, Clock: c}}
}

func sessionDeliveryEligible(l installruntime.Ledger) bool {
	return l.Native != nil && l.Native.DecoderFloor >= 1
}
