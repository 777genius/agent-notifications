package opencodeevent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/google/uuid"
)

// ComposedConsumer is the production boundary: one snapshot and one E1 claim.
// Trusted ports permit independent test authority without a shipped test profile.
type ComposedConsumer struct {
	ControlRoot, Executable, GOOS, GOARCH string
	Source                                SnapshotPort
	Selection                             ClockSelection
	ReadPolicy                            func(context.Context, string) (installruntime.PolicySnapshot, error)
	Desktop                               func(*Handoff) notification.DeliveryPort
	SendWebhook                           func(context.Context, *config.Config, webhook.SendContext) error
	Assets                                config.AssetContext
}

func (c ComposedConsumer) Consume(ctx context.Context, started time.Time, input io.ReadCloser) Receipt {
	entry, err := BeginEntry(ctx, started, c.Source)
	if err != nil {
		return Receipt{Status: "suppressed", Reason: string(TimeUnverified)}
	}
	defer entry.Close()
	return c.ConsumeEntry(entry, input)
}

// ConsumeEntry lets the command start its budget before validating argv/env.
func (c ComposedConsumer) ConsumeEntry(entry *Entry, input io.ReadCloser) Receipt {
	if !c.Selection.valid() {
		return Receipt{Status: "suppressed", Reason: string(TimeUnverified)}
	}
	raw, err := ReadOwnedBounded(entry.Context(), input)
	if err != nil {
		return Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	fact, err := DecodePrivate(raw, c.Selection)
	if err != nil {
		return Receipt{Status: "rejected", Reason: "invalid_frame"}
	}
	if !entry.Tighten(fact.Provenance, c.Selection.Policy) {
		return Receipt{Status: "suppressed", Reason: string(Expired)}
	}
	read := c.ReadPolicy
	if read == nil {
		read = installruntime.ReadPolicySnapshot
	}
	expected, err := read(entry.Context(), c.ControlRoot)
	if err != nil || !entry.Check() {
		return Receipt{Status: "suppressed", Reason: string(SnapshotChanged)}
	}
	// Serialize an independently owned copy, without consulting ambient config.
	fields, err := json.Marshal(expected.Fields)
	if err != nil || len(fields) > config.MaxDocumentBytes {
		return Receipt{Status: "rejected", Reason: "invalid_config"}
	}
	doc, err := config.ParseDocument(fields, filepath.Join(c.ControlRoot, "agent-notifications.json"), true)
	if err != nil {
		return Receipt{Status: "rejected", Reason: "invalid_config"}
	}
	assets := c.Assets
	assets.Agent = config.AgentOpenCode
	assets.LookupEnv = config.OpenCodeWebhookLookup(assets.LookupEnv)
	cfg, err := doc.Effective(assets)
	if err != nil {
		return Receipt{Status: "rejected", Reason: "invalid_config"}
	}
	if !entry.Check() {
		return Receipt{Status: "suppressed", Reason: string(Expired)}
	}
	clock := TrustedClock{Source: c.Source, Selection: c.Selection}
	a := Admission{ControlRoot: c.ControlRoot, Clock: clock, TimePolicy: c.Selection.Policy}
	h, status := a.Admit(entry.Context(), AdmissionRequest{Fact: fact.Fact, Origin: fact.Origin, Provenance: fact.Provenance,
		Expected: expected, Executable: c.Executable, GOOS: c.GOOS, GOARCH: c.GOARCH, CommandStarted: entry.Started})
	if h == nil {
		return Receipt{Status: "suppressed", Reason: string(status)}
	}
	defer h.Close() // actual synchronous provider return precedes lease release
	m, _ := mapEvent(fact.Event.Kind)
	desktop, hook := h.Channels()
	key := string(m.status)
	result := Receipt{}
	desktopReason := ""
	webhookReason := ""
	ready := func() bool { return entry.Check() && h.Context().Err() == nil }
	if desktop && cfg.IsStatusDesktopEnabled(key) {
		result.Desktop = "unavailable"
		if ready() && c.Desktop != nil {
			if port := c.Desktop(h); port != nil && ready() {
				r := port.Deliver(h.Context(), notification.Request{Content: desktopContent(fact.Event, fact.Display, m.content, cfg.IsSessionLabelEnabled()), CorrelationID: uuid.NewString(), Deadline: h.Deadline(),
					Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, SoundEnabled: false}, Navigation: notification.None, Silent: true})
				// Provider-specific text is not a private helper receipt authority.
				switch r.Status {
				case "submitted", "unknown", "unavailable", "rejected":
					result.Desktop = r.Status
				default:
					result.Desktop = "unknown"
				}
				if r.Status == "unknown" || r.Status == "rejected" || r.Status == "unavailable" {
					switch r.Reason {
					case "malformed_request", "configuration_invalid", "navigation_disabled",
						"navigation_unavailable", "unsupported_notifier", "expired",
						"spool_unavailable", "authority_changed", "launch_failed",
						"timeout", "handoff_unconfirmed", "readiness_unavailable",
						"activation_required", "permission_denied", "unsupported_version",
						"unsupported_action", "invalid_file", "os_rejected",
						"native_submission_deadline", "native_submission_cancelled":
						desktopReason = r.Reason
					}
				}
			}
		}
	}
	if hook && cfg.IsStatusWebhookEnabled(key) {
		result.Webhook = "unavailable"
		if ready() && c.SendWebhook != nil {
			err = c.SendWebhook(h.Context(), privateWebhookConfig(cfg, key, m.content.Title), webhook.SendContext{
				Status: m.status, Message: m.content.Body, RawBody: m.content.Body, AgentSource: string(config.AgentOpenCode)})
			if err != nil {
				result.Webhook = "unknown"
				if errors.Is(err, context.DeadlineExceeded) {
					webhookReason = "webhook_deadline"
				} else if errors.Is(err, context.Canceled) {
					webhookReason = "webhook_cancelled"
				}
			} else {
				result.Webhook = "submitted"
			}
		}
	}
	result.Status, result.Reason = aggregateStatus(result.Desktop, result.Webhook)
	// Preserve a finite contributing desktop failure, never a success or raw provider detail.
	if desktopReason != "" && (result.Status == "rejected" || result.Status == "unknown" && result.Desktop == "unknown") {
		result.Reason = desktopReason
	} else if result.Status == "unknown" && result.Webhook == "unknown" && result.Desktop != "unknown" && webhookReason != "" {
		result.Reason = webhookReason
	}
	return result
}
