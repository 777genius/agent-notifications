package copilotvscodeinstall

import (
	"context"
	"runtime"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/cursorevent"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	"github.com/777genius/agent-notifications/internal/webhook"
)

// NewCursorDesktop enters the existing single-lease Linux desktop owner.
func NewCursorDesktop(g CursorGate, b cursorevent.Binding, backend notification.DeliveryPort) notification.DeliveryPort {
	if g.gate.Binding.Integration != portable.Cursor || g.gate.Proof == nil || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return localDesktop{}
	}
	if _, leased := backend.(localDesktop); leased {
		return localDesktop{}
	}
	return NewDesktop(g.gate, localCursorBinding(b), "", backend)
}

// NewCursorWebhookSender converts only the consumer's typed handoff. The
// existing sender owns its one lease and no-redirect completion semantics.
func NewCursorWebhookSender(g CursorGate, b cursorevent.Binding, send cursorevent.WebhookSender) cursorevent.WebhookSender {
	if g.gate.Binding.Integration != portable.Cursor || g.gate.Proof == nil {
		send = nil
	}
	return cursorevent.WebhookSender(NewWebhookSender(g.gate, localCursorBinding(b), copilotvscodeevent.WebhookSender(send)))
}

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
		if g.Binding.Integration == portable.Cursor {
			fresh, identity, err := readCursorConfig(g.Binding)
			if err != nil || identity != lease.initial.configObservation {
				return ErrDenied
			}
			// Consumer content remains private and fixed; the endpoint comes from
			// this lease's admitted config, never a stale caller Config pointer.
			copy := *c
			copy.Notifications.Webhook = config.WebhookConfig{Enabled: true, URL: fresh.Notifications.Webhook.URL, Preset: "custom", Format: "json"}
			c = &copy
		}
		return send(webhook.WithNoRedirects(ctx), c, request)
	}
}
