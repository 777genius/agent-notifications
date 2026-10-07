//go:build darwin

package copilotvscodeinstall

import (
	"context"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// NativeInstallation REPLACES ManagedInstallation's acquisition. The native
// backend owns the sole authority lease through its result-bearing finalizer.
type NativeInstallation struct {
	Gate    Gate
	Binding copilotvscodeevent.Binding
}

func (n NativeInstallation) Acquire(ctx context.Context) (notifier.NativeLease, error) {
	return n.Gate.acquire(ctx, n.Binding, copilotvscodeevent.DesktopChannel)
}

func desktopPort(g Gate, b copilotvscodeevent.Binding, spool string, backend notification.DeliveryPort) notification.DeliveryPort {
	// Mac must use the single native acquisition above, never an already-leased
	// backend supplied by another adapter.
	if backend != nil {
		return nil
	}
	return notifier.NewStructuredDelivery(NativeInstallation{Gate: g, Binding: b}, spool)
}
