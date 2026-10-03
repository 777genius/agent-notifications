package copilotvscodeinstall

import (
	"context"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	"github.com/777genius/agent-notifications/internal/webhook"
)

const CopilotVSCodeToastAppID = opencodeinstall.CopilotVSCodeToastAppID

// PrepareNativeSpool reuses the existing product spool ownership/deadline rules.
// Only a later explicit setup caller may prepare it; Gate never creates assets.
func PrepareNativeSpool(ctx context.Context, root string) (string, error) {
	return opencodeinstall.PrepareNativeSpoolFor(ctx, root, opencodeinstall.CopilotVSCodeDesktop)
}

// NewDesktop accepts only the native informational route. The other-platform
// backend must be the unleased Linux/Windows effect port selected by the trusted
// composer; Mac builds its own single-lease StructuredDelivery.
func NewDesktop(g Gate, b copilotvscodeevent.Binding, spool string, backend notification.DeliveryPort) notification.DeliveryPort {
	return localDesktop{backend: desktopPort(g, b, spool, backend)}
}

type localDesktop struct{ backend notification.DeliveryPort }

func (p localDesktop) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	if p.backend == nil || r.Navigation != notification.None || !r.Silent || !r.Policy.Valid ||
		!r.Policy.ExplicitEnabled || !r.Policy.DesktopEnabled {
		return notification.Receipt{CorrelationID: r.CorrelationID, Status: "rejected", Reason: "local_route_unavailable"}
	}
	return p.backend.Deliver(ctx, r)
}

// NewWebhookSender uses the same one-lease checks as desktop. N1 supplies fixed
// private content/config and already retains the observation claim on errors.
// Even a successful POST becomes uncertain if completion authority drifted.
func NewWebhookSender(g Gate, b copilotvscodeevent.Binding, send copilotvscodeevent.WebhookSender) copilotvscodeevent.WebhookSender {
	return func(ctx context.Context, c *config.Config, request webhook.SendContext) (result error) {
		if send == nil || c == nil || !c.Notifications.Webhook.Enabled {
			return ErrDenied
		}
		lease, err := g.acquire(ctx, b, copilotvscodeevent.WebhookChannel)
		if err != nil {
			return err
		}
		defer func() {
			defer lease.Release()
			if err := lease.Complete(ctx); err != nil {
				result = err
			}
		}()
		if err := lease.BeforeHandoff(ctx); err != nil {
			return err
		}
		return send(webhook.WithNoRedirects(ctx), c, request)
	}
}
