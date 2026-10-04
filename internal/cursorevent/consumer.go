// Package cursorevent owns Cursor admission, fixed informational copy and
// private observation claims. Installed policy/effect composition belongs to N2.
package cursorevent

import (
	"context"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	source "github.com/777genius/agent-notifications/internal/cursorsource"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/google/uuid"
)

// Binding comes exclusively from trusted host installation state. Event data
// never chooses any of these identities, generation or product authority.
type Binding struct {
	InstallationID, BindingID, ProfileIdentity, Product string
	Generation                                          uint64
}
type Channels struct{ Desktop, Webhook bool }
type Channel string

const (
	DesktopChannel Channel = "desktop"
	WebhookChannel Channel = "webhook"
)

// Gate denies unknown/missing policy. Effect owners independently Recheck under
// their single retained lease; it must not acquire another lease recursively.
type Gate interface {
	Channels(context.Context, Binding) Channels
	Recheck(context.Context, Binding, Channel) bool
}

// WebhookSender is an effect-owner seam: acquire its retained installation/policy
// lease and Recheck before NewWithContext(ctx). Consumer supplies the no-redirect context. No retry/fallback/legacy route.
type WebhookSender func(context.Context, *config.Config, webhook.SendContext) error

// Desktop is likewise an effect owner; macOS already owns its helper lease.
// Consumer never wraps either effect owner in a second lease.
type Consumer struct {
	Binding     Binding
	Gate        Gate
	Config      *config.Config
	Desktop     notification.DeliveryPort
	SendWebhook WebhookSender
	Clock       observation.Clock
	Cache       *observation.RecentCache
}

// Receipt reports boundary acceptance, never visibility. Causes/IDs stay private.
type Receipt struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Desktop string `json:"desktop,omitempty"`
	Webhook string `json:"webhook,omitempty"`
}

func validIdentity(s string) bool {
	if s == "" || len(s) > 1024 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (b Binding) valid() bool {
	return validIdentity(b.InstallationID) && validIdentity(b.BindingID) && validIdentity(b.ProfileIdentity) && b.Generation != 0 && b.Product == string(config.AgentCursor)
}

// Consume uses exactly the pre-stdin Admission deadline. Claim is persisted and
// its lock released before Recheck/effects. Mandatory native IDs always claim.
func (c Consumer) Consume(parent context.Context, f source.Facts, d notification.Deadline) (out Receipt) {
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
	if !f.Valid() || !stoppingStatus(f.Status) {
		return out
	}
	remaining, ok := observation.Remaining(c.Clock, d)
	if !ok || parent.Err() != nil {
		return Receipt{Status: "suppressed", Reason: "expired"}
	}
	ctx, cancel := observation.WatchBudget(parent, c.Clock, d, remaining)
	defer cancel()
	if c.Gate == nil || !c.Binding.valid() {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	channels := c.Gate.Channels(ctx, c.Binding)
	if c.Config == nil {
		return Receipt{Status: "suppressed", Reason: "invalid_config"}
	}
	out = Receipt{Status: "suppressed", Reason: "channels_disabled"}
	if channels.Desktop && c.Config.IsStatusDesktopEnabled(string(analyzer.StatusAgentStopping)) {
		out.Desktop = "rejected"
		if c.Desktop != nil {
			if reason := c.claim(ctx, f, d, DesktopChannel); reason != "" {
				out.Desktop = "suppressed"
				out.Reason = reason
			} else {
				attempted = true
				out.Desktop = deliver(ctx, c.Desktop, notification.Request{Content: notification.Content{Title: "Cursor CLI", Body: "Cursor CLI is stopping", Category: "info"}, CorrelationID: uuid.NewString(), Deadline: d, Navigation: notification.None, Silent: true, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}})
			}
		}
	}
	if channels.Webhook && c.Config.IsStatusWebhookEnabled(string(analyzer.StatusAgentStopping)) {
		out.Webhook = "rejected"
		if c.SendWebhook != nil {
			if reason := c.claim(ctx, f, d, WebhookChannel); reason != "" {
				out.Webhook = "suppressed"
				out.Reason = reason
			} else {
				attempted = true
				out.Webhook = send(ctx, c.SendWebhook, privateWebhookConfig(c.Config), webhook.SendContext{Status: analyzer.StatusAgentStopping, AgentSource: string(config.AgentCursor), Message: "Cursor CLI is stopping", RawBody: "Cursor CLI is stopping"})
			}
		}
	}
	return aggregate(out)
}
func (c Consumer) claim(ctx context.Context, f source.Facts, d notification.Deadline, ch Channel) string {
	if _, ok := observation.Remaining(c.Clock, d); !ok || ctx.Err() != nil {
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
	if !c.Gate.Recheck(ctx, c.Binding, ch) {
		return "revoked"
	}
	if _, ok := observation.Remaining(c.Clock, d); !ok || ctx.Err() != nil {
		return "expired"
	}
	return ""
}

// All three documented statuses mean stopping, never task success.
func stoppingStatus(status string) bool {
	return status == "completed" || status == "aborted" || status == "error"
}

func aggregate(out Receipt) Receipt {
	switch {
	case out.Desktop == "unknown" || out.Webhook == "unknown":
		out.Status, out.Reason = "unknown", "delivery_uncertain"
	case out.Desktop == "submitted" || out.Webhook == "submitted":
		out.Status, out.Reason = "submitted", ""
	case out.Desktop == "rejected" || out.Webhook == "rejected":
		out.Status, out.Reason = "rejected", "delivery_unavailable"
	}
	return out
}
func deliver(ctx context.Context, p notification.DeliveryPort, r notification.Request) (status string) {
	status = "unknown"
	defer func() {
		if recover() != nil {
			status = "unknown"
		}
	}()
	switch s := p.Deliver(ctx, r).Status; s {
	case "submitted", "suppressed", "rejected":
		return s
	default:
		return "unknown"
	}
}
func send(ctx context.Context, s WebhookSender, c *config.Config, p webhook.SendContext) (status string) {
	status = "unknown"
	defer func() {
		if recover() != nil {
			status = "unknown"
		}
	}()
	if s(webhook.WithNoRedirects(ctx), c, p) == nil {
		return "submitted"
	}
	return status
}
func privateWebhookConfig(c *config.Config) *config.Config {
	safe := &config.Config{}
	// Only endpoint is copied. Fixed JSON transport prevents arbitrary templates,
	// headers, preset/chat IDs, payload fields, titles and status enrichment.
	safe.Notifications.Webhook = config.WebhookConfig{Enabled: true, URL: c.Notifications.Webhook.URL, Preset: "custom", Format: "json"}
	safe.Statuses = map[string]config.StatusInfo{string(analyzer.StatusAgentStopping): {Title: "Cursor CLI"}}
	return safe
}
