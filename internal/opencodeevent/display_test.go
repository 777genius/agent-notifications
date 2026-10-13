package opencodeevent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/webhook"
)

func displayFrame(kind, display string) string {
	request := ""
	message := "m"
	if kind == "question_asked" {
		request, message = "r", ""
	}
	return fmt.Sprintf(`{"version":1,"kind":%q,"sessionID":"s","turnID":"t","messageID":%q,"requestID":%q,"rootSession":true,"display":%s}`, kind, message, request, display)
}

// Regression: generic-only mapping, duplicated completion/question words,
// missing context subtitle, ignored label preference or private webhook copy.
func TestDisplayContextIsDesktopOnlyAndRespectsLabels(t *testing.T) {
	for _, tc := range []struct {
		name, kind, title, subtitle string
		labels                      bool
	}{
		{"completion", "turn_idle_verified", "✅ [Installer work]", "", true},
		{"question", "question_asked", "❓ Use the new installer?", "Installer work", true},
		{"hidden completion", "turn_idle_verified", "OpenCode", "", false},
		{"hidden question", "question_asked", "❓ Use the new installer?", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Notifications.Desktop.ShowSessionLabel = &tc.labels
			cfg.Notifications.Webhook.Enabled = true
			cfg.Notifications.Webhook.URL = "https://example.invalid/unused"
			desktop := &captureDesktop{}
			webhookCalls := 0
			c := Consumer{Gate: channelGate{true, true}, Config: cfg, Desktop: desktop, Clock: fixedClock{},
				SendWebhook: func(cfg *config.Config, ctx webhook.SendContext) error {
					webhookCalls++
					encoded, _ := json.Marshal(struct {
						Config  *config.Config
						Context webhook.SendContext
					}{cfg, ctx})
					if strings.Contains(string(encoded), "Installer work") || strings.Contains(string(encoded), "Use the new installer?") {
						t.Fatal("desktop context leaked to webhook")
					}
					if cfg.Statuses[string(ctx.Status)].Title != "OpenCode" {
						t.Fatal("webhook title changed")
					}
					expected := "Task completed"
					if tc.kind == "question_asked" {
						expected = "OpenCode asked a question"
					}
					if ctx.Message != expected || ctx.RawBody != expected {
						t.Fatalf("webhook body changed: %+v", ctx)
					}
					return nil
				}}
			data := `{"sessionID":"s","sessionTitle":"Installer work"}`
			if tc.kind == "question_asked" {
				data = `{"sessionID":"s","requestID":"r","sessionTitle":"Installer work","question":"Use the new installer?"}`
			}
			got := c.Consume(context.Background(), strings.NewReader(displayFrame(tc.kind, data)))
			if got.Status != "submitted" || len(desktop.calls) != 1 || webhookCalls != 1 {
				t.Fatalf("receipt=%+v desktop=%d webhooks=%d", got, len(desktop.calls), webhookCalls)
			}
			req := desktop.calls[0]
			if req.Content.Title != tc.title || req.Content.Subtitle != tc.subtitle {
				t.Fatalf("content=%+v", req.Content)
			}
			if tc.kind == "question_asked" && req.Content.Body != "Use the new installer?" {
				t.Fatalf("concrete question body missing: %+v", req.Content)
			}
			if !req.Silent || req.Navigation != notification.None || req.Policy.SoundEnabled {
				t.Fatalf("delivery policy changed: %+v", req)
			}
			receipt, _ := json.Marshal(got)
			if strings.Contains(string(receipt), "Installer work") || strings.Contains(string(receipt), "Use the new installer?") {
				t.Fatal("private receipt")
			}
		})
	}
}

// Regression: optional malformed data, cross-session/request identity or bad
// UTF encoding rejects the whole fact or leaks a misleading private title.
func TestInvalidOptionalDisplayKeepsGenericNotification(t *testing.T) {
	for name, display := range map[string]string{
		"wrong session":      `{"sessionID":"other","requestID":"r","sessionTitle":"SECRET","question":"SECRET?"}`,
		"wrong request":      `{"sessionID":"s","requestID":"other","question":"SECRET?"}`,
		"missing request":    `{"sessionID":"s","question":"SECRET?"}`,
		"wrong type":         `{"sessionID":"s","requestID":"r","question":123}`,
		"not object":         `["SECRET"]`,
		"duplicate":          `{"sessionID":"s","requestID":"r","question":"SECRET?","question":"OTHER?"}`,
		"invalid surrogate":  `{"sessionID":"s","requestID":"r","question":"\ud800"}`,
		"invalid UTF8":       "{\"sessionID\":\"s\",\"requestID\":\"r\",\"question\":\"\xff\"}",
		"control":            `{"sessionID":"s","requestID":"r","question":"SECRET\u0000?"}`,
		"oversized title":    `{"sessionID":"s","requestID":"r","sessionTitle":"` + strings.Repeat("s", 1025) + `"}`,
		"oversized question": `{"sessionID":"s","requestID":"r","question":"` + strings.Repeat("q", 2049) + `"}`,
		"unexpected field":   `{"sessionID":"s","requestID":"r","question":"SECRET?","options":["PRIVATE"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			desktop := &captureDesktop{}
			c := Consumer{Gate: GateFunc(func(context.Context) bool { return true }), Config: config.DefaultConfig(), Desktop: desktop, Clock: fixedClock{}}
			got := c.Consume(context.Background(), strings.NewReader(displayFrame("question_asked", display)))
			if got.Desktop != "submitted" || len(desktop.calls) != 1 {
				t.Fatalf("bad optional data suppressed neutral event: %+v", got)
			}
			if desktop.calls[0].Content.Title != "OpenCode" || desktop.calls[0].Content.Subtitle != "" {
				t.Fatalf("private or invalid display: %+v", desktop.calls[0].Content)
			}
		})
	}
}

func TestDisplayDoesNotBroadenRootOrChannelConsent(t *testing.T) {
	desktop := &captureDesktop{}
	c := Consumer{Gate: channelGate{false, false}, Config: config.DefaultConfig(), Desktop: desktop, Clock: fixedClock{}}
	input := displayFrame("question_asked", `{"sessionID":"s","requestID":"r","question":"PRIVATE?"}`)
	if got := c.Consume(context.Background(), strings.NewReader(input)); got.Status != "suppressed" {
		t.Fatalf("consent: %+v", got)
	}
	c.Gate = channelGate{true, false}
	input = strings.Replace(input, `"rootSession":true`, `"rootSession":false`, 1)
	if got := c.Consume(context.Background(), strings.NewReader(input)); got.Status != "suppressed" {
		t.Fatalf("root-only: %+v", got)
	}
	if len(desktop.calls) != 0 {
		t.Fatal("display broadened consent")
	}
}
