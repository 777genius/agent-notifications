// Package opencodeevent consumes the bounded, neutral OpenCode observer wire.
// It never displays or forwards native event data or opaque identifiers.
package opencodeevent

import (
	"context"
	"io"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/777genius/agent-notifications/internal/webhook"
	uap "github.com/777genius/plugin-kit-ai/sdk/opencode"
	"github.com/google/uuid"
)

// Gate is the current, read-only opt-in and registration decision. A nil gate
// denies delivery. PR4 supplies the persisted implementation at composition.
type Gate interface{ Enabled(context.Context) bool }

// ChannelGate binds a product setup decision to the same event observation.
// Legacy test gates retain their existing allow-all-channel behavior.
type ChannelGate interface {
	Channels(context.Context) (desktop, webhook bool)
}

type GateFunc func(context.Context) bool

func (f GateFunc) Enabled(ctx context.Context) bool { return f(ctx) }

// WebhookSender allows a single, non-replayed external attempt.
type WebhookSender func(*config.Config, webhook.SendContext) error

type Clock interface {
	Now() (bootID string, seconds float64, err error)
}

type Consumer struct {
	Gate        Gate
	Config      *config.Config
	Desktop     notification.DeliveryPort
	Clock       Clock
	SendWebhook WebhookSender
}

// Receipt contains only stable classification. It never includes event IDs,
// raw data, transport errors, configuration, or remote response content.
type Receipt struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Desktop string `json:"desktop,omitempty"`
	Webhook string `json:"webhook,omitempty"`
}

type message struct {
	status  analyzer.Status
	content notification.Content
}

func mapEvent(kind uap.Kind) (message, bool) {
	switch kind {
	case uap.TurnIdleVerified:
		return message{analyzer.StatusTaskComplete, notification.Content{Title: "OpenCode", Body: "Task completed", Category: "info"}}, true
	case uap.QuestionAsked:
		return message{analyzer.StatusQuestion, notification.Content{Title: "OpenCode", Body: "OpenCode asked a question", Category: "attention"}}, true
	case uap.PermissionAsked:
		return message{analyzer.StatusPermissionRequest, notification.Content{Title: "OpenCode", Body: "OpenCode requested permission", Category: "attention"}}, true
	case uap.TerminalError:
		return message{analyzer.Status("opencode_error"), notification.Content{Title: "OpenCode", Body: "An error needs your attention", Category: "attention"}}, true
	default:
		return message{}, false
	}
}

// Consume reads exactly one bounded JSON object. Unknown v1 facts and
// subagent facts are intentionally silent. Delivery is at most once per call.
func (c Consumer) Consume(ctx context.Context, input io.Reader) Receipt {
	raw, err := io.ReadAll(io.LimitReader(input, uap.MaxBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > uap.MaxBytes {
		return Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	if strictjson.Validate(raw, strictjson.Budget{Bytes: uap.MaxBytes, Depth: 8, Entries: 64}) != nil {
		return Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	event, err := uap.Decode(raw)
	if err != nil {
		return Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	m, supported := mapEvent(event.Kind)
	if !supported || event.RootSession == nil || !*event.RootSession {
		return Receipt{Status: "suppressed", Reason: "unsupported_fact"}
	}
	if c.Gate == nil {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	desktopAllowed, webhookAllowed := true, true
	if gate, ok := c.Gate.(ChannelGate); ok {
		desktopAllowed, webhookAllowed = gate.Channels(ctx)
	} else if !c.Gate.Enabled(ctx) {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	if !desktopAllowed && !webhookAllowed {
		return Receipt{Status: "suppressed", Reason: "not_registered"}
	}
	if c.Config == nil {
		return Receipt{Status: "rejected", Reason: "invalid_config"}
	}
	result := Receipt{Status: "suppressed", Reason: "channels_disabled"}
	status := string(m.status)
	if desktopAllowed && c.Config.IsStatusDesktopEnabled(status) {
		result.Desktop = "unavailable"
		if c.Desktop != nil && c.Clock != nil {
			boot, now, clockErr := c.Clock.Now()
			if clockErr == nil && boot != "" {
				receipt := c.Desktop.Deliver(ctx, notification.Request{
					Content:       m.content,
					CorrelationID: uuid.NewString(),
					Deadline:      notification.Deadline{BootID: boot, NotAfter: now + 10},
					Policy:        notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, SoundEnabled: false},
					Navigation:    notification.None,
					Silent:        true,
				})
				result.Desktop = receipt.Status
			}
		}
	}
	if webhookAllowed && c.Config.IsStatusWebhookEnabled(status) {
		result.Webhook = "unavailable"
		if c.SendWebhook != nil {
			if err := c.SendWebhook(privateWebhookConfig(c.Config, status, m.content.Title), webhook.SendContext{
				Status: m.status, Message: m.content.Body, RawBody: m.content.Body, AgentSource: string(config.AgentOpenCode),
			}); err != nil {
				result.Webhook = "unknown"
			} else {
				result.Webhook = "submitted"
			}
		}
	}
	result.Status, result.Reason = aggregateStatus(result.Desktop, result.Webhook)
	return result
}

func aggregateStatus(desktop, hook string) (status, reason string) {
	if desktop == "" && hook == "" {
		return "suppressed", "channels_disabled"
	}
	if desktop == "unknown" || hook == "unknown" {
		return "unknown", "delivery_uncertain"
	}
	if desktop == "submitted" || hook == "submitted" {
		return "submitted", ""
	}
	return "rejected", "delivery_unavailable"
}

func privateWebhookConfig(source *config.Config, status, title string) *config.Config {
	copy := *source
	copy.Notifications.Webhook.Headers = nil
	copy.Notifications.Webhook.PayloadFields = nil
	copy.Notifications.Webhook.Retry.Enabled = false
	// User status titles can contain private values. Only fixed product copy is
	// allowed into a preset or custom payload.
	copy.Statuses = map[string]config.StatusInfo{status: {Title: title}}
	return &copy
}
