//go:build !darwin

package copilotvscodeinstall

import (
	"context"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func desktopPort(g Gate, b copilotvscodeevent.Binding, _ string, backend notification.DeliveryPort) notification.DeliveryPort {
	if backend == nil {
		return nil
	}
	// StructuredDelivery already owns a NativeInstallation: reject double leasing.
	if _, alreadyLeased := backend.(*notifier.StructuredDelivery); alreadyLeased {
		return nil
	}
	return leasedDesktop{gate: g, binding: b, backend: backend}
}

type leasedDesktop struct {
	gate    Gate
	binding copilotvscodeevent.Binding
	backend notification.DeliveryPort
}

func (p leasedDesktop) Deliver(ctx context.Context, r notification.Request) (out notification.Receipt) {
	out = notification.Receipt{CorrelationID: r.CorrelationID, Status: "suppressed", Reason: "not_registered"}
	lease, err := p.gate.acquire(ctx, p.binding, copilotvscodeevent.DesktopChannel)
	if err != nil {
		return out
	}
	possible := false
	defer func() {
		defer lease.Release()
		if err := lease.Complete(ctx); err != nil {
			if possible {
				out.Status, out.Reason = "unknown", "authority_changed"
			} else {
				out.Status, out.Reason = "suppressed", "revoked"
			}
		}
	}()
	if err := lease.BeforeHandoff(ctx); err != nil {
		return out
	}
	possible = true
	return p.backend.Deliver(ctx, r)
}
