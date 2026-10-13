package main

import (
	"context"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
)

// Construction accepts only the already-held E1 reference. StructuredDelivery
// owns its single Acquire/Release; this boundary never acquires policy again.
func newOpenCodeHeldDesktop(held notifier.NativeInstallation, spool string) heldOpenCodeDesktop {
	port := notifier.NewStructuredDelivery(held, spool)
	return heldOpenCodeDesktop{port: port, clock: port.Clock}
}

type heldOpenCodeDesktop struct {
	port  notification.DeliveryPort
	clock notifier.BootClock
}

func (p heldOpenCodeDesktop) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	raw, _, err := p.clock.Now()
	if err != nil {
		return notification.Receipt{Status: "unavailable"}
	}
	d, err := opencodeevent.BridgeNativeDeadline(r.Deadline, raw)
	if err != nil {
		return notification.Receipt{Status: "unavailable"}
	}
	r.Deadline = d
	return p.port.Deliver(ctx, r)
}
