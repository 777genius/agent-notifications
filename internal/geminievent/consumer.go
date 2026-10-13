// Package geminievent owns Gemini's business admission and fixed delivery copy.
// Native decoding and SDK event types belong solely to geminisource.
package geminievent

import (
	"context"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/geminisource"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/google/uuid"
)

// Binding is trusted installation state, never a value decoded from the agent.
type Binding struct {
	InstallationID string
	Generation     uint64
}
type Channels struct{ Desktop, Webhook bool }
type Channel string

const (
	DesktopChannel Channel = "desktop"
	WebhookChannel Channel = "webhook"
)

// Gate reads Gemini-specific registration and consent. Recheck must also be
// called by the effect owner under its retained policy/installation lease;
// it must not recursively acquire that lease. Consumer holds no effect lease.
type Gate interface {
	Channels(context.Context, Binding) Channels
	Recheck(context.Context, Binding, Channel) bool
}

// WebhookSender receives the remaining parent budget and private business DTO.
// Its host wrapper owns lease-time binding/channel revalidation, one attempt.
type WebhookSender func(context.Context, *config.Config, webhook.SendContext) error

type Consumer struct {
	Gate        Gate
	Binding     Binding
	Config      *config.Config
	Desktop     notification.DeliveryPort
	Clock       Clock
	Cache       *RecentCache
	SendWebhook WebhookSender
}

// Receipt never includes native IDs, paths, config or error causes.
type Receipt struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Desktop string `json:"desktop,omitempty"`
	Webhook string `json:"webhook,omitempty"`
}

func message(f geminisource.Facts) (analyzer.Status, notification.Content, bool) {
	switch f.Event {
	case geminisource.AfterAgent:
		if f.StopHookActive || f.Subtype != "" {
			break
		}
		return analyzer.StatusTaskComplete, notification.Content{Title: "Gemini CLI", Body: "Gemini CLI completed a turn", Category: "info"}, true
	case geminisource.Notification:
		if f.Subtype == geminisource.ToolPermission && !f.StopHookActive {
			return analyzer.StatusPermissionRequest, notification.Content{Title: "Gemini CLI", Body: "Gemini CLI requested tool permission", Category: "attention"}, true
		}
	}
	return "", notification.Content{}, false
}

// Consume handles only facts admitted by the source. Deadline is captured by
// Admission before input: this function never starts a new four-second budget.
func (c Consumer) Consume(parent context.Context, facts geminisource.Facts, deadline notification.Deadline) (out Receipt) {
	out = Receipt{Status: "suppressed", Reason: "invalid_fact"}
	defer func() {
		if recover() != nil {
			out = Receipt{Status: "suppressed", Reason: "consumer_unavailable"}
		}
	}()
	status, content, supported := message(facts)
	if !supported {
		return Receipt{Status: "suppressed", Reason: "unsupported_fact"}
	}
	if facts.SessionID == "" {
		return out
	}
	duration, ok := remaining(c.Clock, deadline)
	if !ok || parent.Err() != nil {
		return Receipt{Status: "suppressed", Reason: "expired"}
	}
	ctx, cancel := watchBudget(parent, c.Clock, deadline, duration)
	defer cancel()
	if c.Gate == nil || c.Binding.InstallationID == "" || c.Binding.Generation == 0 {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	channels := c.Gate.Channels(ctx, c.Binding)
	if !channels.Desktop && !channels.Webhook {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	if c.Config == nil {
		return Receipt{Status: "suppressed", Reason: "invalid_config"}
	}
	out = Receipt{Status: "suppressed", Reason: "channels_disabled"}
	// Attempts without a native timestamp are scoped to this invocation. There
	// is no retry loop or replay entrypoint, and each channel is visited once.
	claim := func(channel Channel) string {
		if _, ok := remaining(c.Clock, deadline); !ok || ctx.Err() != nil {
			return "expired"
		}
		if facts.Timestamp != "" {
			claimed, err := c.Cache.claim(ctx, c.Binding, facts, channel)
			if err != nil {
				return "cache_unavailable"
			}
			if !claimed {
				return "duplicate"
			}
		}
		// claim returned with the cache lock released. Effect wrappers independently
		// recheck this binding under their own lease (Mac backend owns its lease).
		if !c.Gate.Recheck(ctx, c.Binding, channel) {
			return "revoked"
		}
		if _, ok := remaining(c.Clock, deadline); !ok || ctx.Err() != nil {
			return "expired"
		}
		return ""
	}
	if channels.Desktop && c.Config.IsStatusDesktopEnabled(string(status)) {
		out.Desktop = "unavailable"
		if c.Desktop != nil {
			if reason := claim(DesktopChannel); reason != "" {
				out.Desktop = reason
			} else {
				out.Desktop = deliver(ctx, c.Desktop, notification.Request{
					Content: content, CorrelationID: uuid.NewString(), Deadline: deadline,
					Policy:     notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true},
					Navigation: notification.None, Silent: true,
				})
			}
		}
	}
	if channels.Webhook && c.Config.IsStatusWebhookEnabled(string(status)) {
		out.Webhook = "unavailable"
		if c.SendWebhook != nil {
			if reason := claim(WebhookChannel); reason != "" {
				out.Webhook = reason
			} else {
				out.Webhook = sendWebhook(ctx, c.SendWebhook, privateWebhookConfig(c.Config, string(status)), webhook.SendContext{
					Status: status, Message: content.Body, RawBody: content.Body, AgentSource: string(config.AgentGemini),
				})
			}
		}
	}
	switch {
	case out.Desktop == "unknown" || out.Webhook == "unknown":
		out.Status, out.Reason = "unknown", "delivery_uncertain"
	case out.Desktop == "submitted" || out.Webhook == "submitted":
		out.Status, out.Reason = "submitted", ""
	case out.Desktop == "unavailable" || out.Webhook == "unavailable" || out.Desktop == "rejected" || out.Webhook == "rejected":
		out.Status, out.Reason = "rejected", "delivery_unavailable"
	case out.Desktop == "cache_unavailable" || out.Webhook == "cache_unavailable":
		out.Reason = "cache_unavailable"
	case out.Desktop != "" || out.Webhook != "":
		out.Reason = "no_attempt"
	}
	return out
}

func deliver(ctx context.Context, port notification.DeliveryPort, request notification.Request) (status string) {
	status = "unknown"
	defer func() {
		if recover() != nil {
			status = "unknown"
		}
	}()
	switch port.Deliver(ctx, request).Status {
	case "submitted":
		return "submitted"
	case "rejected":
		return "rejected"
	case "suppressed":
		return "suppressed"
	default:
		return "unknown"
	}
}
func sendWebhook(ctx context.Context, sender WebhookSender, cfg *config.Config, payload webhook.SendContext) (status string) {
	status = "unknown"
	defer func() {
		if recover() != nil {
			status = "unknown"
		}
	}()
	if sender(ctx, cfg, payload) == nil {
		return "submitted"
	}
	return "unknown"
}
func privateWebhookConfig(source *config.Config, status string) *config.Config {
	// Reconstruct the transport-only configuration instead of copying private
	// notification enrichment, sounds, paths, filters and user status titles.
	cfg := &config.Config{}
	cfg.Notifications.Webhook = source.Notifications.Webhook
	cfg.Notifications.Webhook.Headers = nil
	cfg.Notifications.Webhook.PayloadFields = nil
	cfg.Notifications.Webhook.Retry.Enabled = false
	cfg.Statuses = map[string]config.StatusInfo{status: {Title: "Gemini CLI"}}
	return cfg
}
