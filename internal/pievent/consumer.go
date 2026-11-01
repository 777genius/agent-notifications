package pievent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/pisource"
	"github.com/777genius/agent-notifications/internal/webhook"
)

type Binding struct {
	InstallationID, BindingID string
	Generation                uint64
}
type Channels struct{ Desktop, Webhook bool }
type Channel string

const (
	DesktopChannel Channel = "desktop"
	WebhookChannel Channel = "webhook"
)

// Acquire revalidates the captured revision, current channel consent and binding
// under one retained installation lease. Consumer never recursively leases it.
type Gate interface {
	Channels(context.Context, Binding, pisource.Revision) Channels
	Acquire(context.Context, Binding, pisource.Revision, Channel) (func(), error)
}
type DesktopRequest struct {
	Status               analyzer.Status
	Body, SessionID, CWD string
}
type DesktopSender func(context.Context, DesktopRequest) error
type WebhookSender func(context.Context, *config.Config, webhook.SendContext) error
type Consumer struct {
	Binding       Binding
	Gate          Gate
	Config        *config.Config
	LocalTerminal bool // trusted native process context, never a frame assertion
	Desktop       DesktopSender
	SendWebhook   WebhookSender
	Clock         observation.Clock
	Cache         *observation.RecentCache
}
type Receipt struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Desktop string `json:"desktop,omitempty"`
	Webhook string `json:"webhook,omitempty"`
}

func message(f pisource.Facts) (analyzer.Status, string, bool) {
	switch f.Kind {
	case pisource.Completed:
		return analyzer.StatusTaskComplete, "Pi completed a task", true
	case pisource.Failed:
		return analyzer.StatusAPIError, "Pi encountered an error", true
	case pisource.Question:
		return analyzer.StatusQuestion, "Pi is waiting for input", true
	default:
		return "", "", false
	}
}
func marker(b Binding, f pisource.Facts) string {
	data, _ := json.Marshal([]string{"AN/Pi/observation/v1", b.InstallationID, b.BindingID, f.ObserverID, f.ObservationID})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
func (c Consumer) Consume(parent context.Context, f pisource.Facts, d notification.Deadline) (out Receipt) {
	out = Receipt{Status: "suppressed", Reason: "invalid_fact"}
	attempted := false
	defer func() {
		if recover() != nil {
			if attempted {
				out = Receipt{Status: "unknown", Reason: "delivery_uncertain"}
			} else {
				out = Receipt{Status: "suppressed", Reason: "consumer_unavailable"}
			}
		}
	}()
	if !f.Valid() {
		return out
	}
	status, body, supported := message(f)
	if !supported {
		return Receipt{Status: "suppressed", Reason: "unsupported_fact"}
	}
	duration, ok := remaining(c.Clock, d)
	if parent == nil || parent.Err() != nil || !ok {
		return Receipt{Status: "suppressed", Reason: "expired"}
	}
	ctx, cancel := watch(parent, c.Clock, d, duration)
	defer cancel()
	if c.Gate == nil || c.Binding.InstallationID == "" || c.Binding.BindingID == "" || c.Binding.Generation == 0 || f.Revision.BindingID != c.Binding.BindingID {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	if c.Config == nil {
		return Receipt{Status: "suppressed", Reason: "invalid_config"}
	}
	channels := c.Gate.Channels(ctx, c.Binding, f.Revision)
	out = Receipt{Status: "suppressed", Reason: "channels_disabled"}
	effect := func(ch Channel, invoke func() error) string {
		if _, ok := remaining(c.Clock, d); !ok || ctx.Err() != nil {
			return "expired"
		}
		bit := uint8(1)
		if ch == WebhookChannel {
			bit = 2
		}
		claimed, err := c.Cache.Claim(ctx, marker(c.Binding, f), bit)
		if err != nil {
			return "cache_unavailable"
		}
		if !claimed {
			return "duplicate"
		}
		release, err := c.Gate.Acquire(ctx, c.Binding, f.Revision, ch)
		if err != nil || release == nil {
			return "revoked"
		}
		defer release()
		if _, ok := remaining(c.Clock, d); !ok || ctx.Err() != nil {
			return "expired"
		}
		attempted = true
		err = invoke()
		if errors.Is(err, notification.ErrSuppressed) {
			return "suppressed"
		}
		if err != nil {
			return "unknown"
		}
		return "submitted"
	}
	if channels.Desktop && c.LocalTerminal && f.Mode == "tui" && c.Config.IsStatusDesktopEnabled(string(status)) && c.Desktop != nil {
		out.Desktop = effect(DesktopChannel, func() error {
			return c.Desktop(ctx, DesktopRequest{Status: status, Body: body, SessionID: f.SessionID, CWD: f.CWD})
		})
	}
	if channels.Webhook && c.Config.IsStatusWebhookEnabled(string(status)) && c.SendWebhook != nil {
		out.Webhook = effect(WebhookChannel, func() error {
			return c.SendWebhook(ctx, privateWebhookConfig(c.Config, string(status)), webhook.SendContext{Status: status, Message: body, RawBody: body, AgentSource: string(config.AgentPi)})
		})
	}
	switch {
	case out.Desktop == "unknown" || out.Webhook == "unknown":
		out.Status, out.Reason = "unknown", "delivery_uncertain"
	case out.Desktop == "submitted" || out.Webhook == "submitted":
		out.Status, out.Reason = "submitted", "hook_handoff"
	case out.Desktop != "" || out.Webhook != "":
		out.Reason = "no_attempt"
	}
	return out
}
func privateWebhookConfig(source *config.Config, status string) *config.Config {
	cfg := &config.Config{}
	cfg.Notifications.Webhook = source.Notifications.Webhook
	cfg.Notifications.Webhook.Headers = nil
	cfg.Notifications.Webhook.PayloadFields = nil
	cfg.Notifications.Webhook.Retry.Enabled = false
	cfg.Statuses = map[string]config.StatusInfo{status: {Title: "Pi"}}
	return cfg
}
