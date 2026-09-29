package opencodeevent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/webhook"
)

type captureDesktop struct{ calls []notification.Request }

func (d *captureDesktop) Deliver(_ context.Context, req notification.Request) notification.Receipt {
	d.calls = append(d.calls, req)
	return notification.Receipt{Status: "submitted"}
}

type fixedClock struct{}

func (fixedClock) Now() (string, float64, error) { return "test-boot", 100, nil }

func frame(kind, marker string, root bool) string {
	return fmt.Sprintf(`{"version":1,"kind":%q,"sessionID":%q,"turnID":"turn","messageID":"message","rootSession":%t}`, kind, marker, root)
}

func TestConsumeRejectsAmbiguousOrOversizedFrames(t *testing.T) {
	for name, input := range map[string]string{
		"duplicate scope": `{"version":1,"kind":"turn_idle_verified","sessionID":"s","turnID":"t","messageID":"m","rootSession":true,"rootSession":false}`,
		"trailing object": frame("turn_idle_verified", "s", true) + `{}`,
		"oversized":       frame("turn_idle_verified", strings.Repeat("s", 4096), true),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			c := Consumer{Gate: GateFunc(func(context.Context) bool { calls++; return true })}
			got := c.Consume(context.Background(), strings.NewReader(input))
			if got.Status != "rejected" || calls != 0 {
				t.Fatalf("receipt=%+v gate calls=%d", got, calls)
			}
		})
	}
}

func TestConsumeRequiresCurrentGateAndRootFact(t *testing.T) {
	desktop := &captureDesktop{}
	cfg := config.DefaultConfig()
	c := Consumer{Config: cfg, Desktop: desktop, Clock: fixedClock{}}
	if got := c.Consume(context.Background(), strings.NewReader(frame("turn_idle_verified", "private", true))); got.Reason != "not_registered" {
		t.Fatalf("nil gate: %+v", got)
	}
	c.Gate = GateFunc(func(context.Context) bool { return true })
	if got := c.Consume(context.Background(), strings.NewReader(frame("turn_idle_verified", "private", false))); got.Status != "suppressed" {
		t.Fatalf("subagent fact: %+v", got)
	}
	if got := c.Consume(context.Background(), strings.NewReader(frame("future_fact", "private", true))); got.Status != "suppressed" {
		t.Fatalf("unknown fact: %+v", got)
	}
	if len(desktop.calls) != 0 {
		t.Fatalf("unexpected desktop calls: %d", len(desktop.calls))
	}
}

func TestFourFactsKeepDistinctNotificationCategories(t *testing.T) {
	for _, tc := range []struct {
		kind, requestID, messageID, body, category string
	}{
		{"turn_idle_verified", "", "m", "Task completed", "info"},
		{"question_asked", "r", "", "Question needs your answer", "attention"},
		{"permission_asked", "r", "", "Permission needs your decision", "attention"},
		{"terminal_error", "", "", "An error needs your attention", "attention"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			frame := fmt.Sprintf(`{"version":1,"kind":%q,"sessionID":"s","turnID":"t","messageID":%q,"requestID":%q,"rootSession":true}`, tc.kind, tc.messageID, tc.requestID)
			desktop := &captureDesktop{}
			c := Consumer{Gate: GateFunc(func(context.Context) bool { return true }), Config: config.DefaultConfig(), Desktop: desktop, Clock: fixedClock{}}
			got := c.Consume(context.Background(), strings.NewReader(frame))
			if got.Desktop != "submitted" || len(desktop.calls) != 1 {
				t.Fatalf("receipt=%+v calls=%d", got, len(desktop.calls))
			}
			content := desktop.calls[0].Content
			if content.Body != tc.body || content.Category != tc.category || content.Title != "OpenCode" {
				t.Fatalf("content=%+v", content)
			}
		})
	}
}

func TestConsumePrivateOutputsAndSingleWebhookAttempt(t *testing.T) {
	const secret = "SECRET_FROM_NATIVE_EVENT_9c73"
	var calls atomic.Int32
	var receivedBody, receivedHeaders string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		receivedHeaders = fmt.Sprint(r.Header)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := config.DefaultConfig()
	cfg.Notifications.Webhook.Enabled = true
	cfg.Notifications.Webhook.URL = srv.URL
	cfg.Notifications.Webhook.Headers = map[string]string{"X-Secret": secret}
	cfg.Notifications.Webhook.PayloadFields = map[string]interface{}{"private": secret}
	cfg.Statuses["task_complete"] = config.StatusInfo{Title: secret}
	desktop := &captureDesktop{}
	c := Consumer{
		Gate: GateFunc(func(context.Context) bool { return true }), Config: cfg,
		Desktop: desktop, Clock: fixedClock{},
		SendWebhook: func(cfg *config.Config, ctx webhook.SendContext) error { return webhook.New(cfg).SendWithContext(ctx) },
	}
	got := c.Consume(context.Background(), strings.NewReader(frame("turn_idle_verified", secret, true)))
	if got.Status != "unknown" || got.Desktop != "submitted" || got.Webhook != "unknown" || calls.Load() != 1 {
		t.Fatalf("receipt=%+v webhook calls=%d", got, calls.Load())
	}
	if len(desktop.calls) != 1 || desktop.calls[0].Navigation != notification.None || desktop.calls[0].Target != (notification.DesktopTarget{}) {
		t.Fatalf("desktop routing: %+v", desktop.calls)
	}
	if desktop.calls[0].Deadline.BootID != "test-boot" || desktop.calls[0].Deadline.NotAfter != 110 {
		t.Fatalf("desktop deadline: %+v", desktop.calls[0].Deadline)
	}
	all, _ := json.Marshal(struct {
		Receipt       Receipt
		Desktop       notification.Request
		Body, Headers string
	}{got, desktop.calls[0], receivedBody, receivedHeaders})
	if strings.Contains(string(all), secret) {
		t.Fatal("native or config secret escaped to delivery or receipt")
	}
	if !strings.Contains(receivedBody, `"agent_source":"opencode"`) {
		t.Fatalf("missing product identity: %s", receivedBody)
	}
}

func TestAggregateReceiptMatchesProducerProtocol(t *testing.T) {
	for _, tc := range []struct{ desktop, webhook, want string }{
		{"", "", "suppressed"},
		{"unavailable", "", "rejected"},
		{"rejected", "", "rejected"},
		{"submitted", "", "submitted"},
		{"", "submitted", "submitted"},
		{"submitted", "unknown", "unknown"},
	} {
		got, _ := aggregateStatus(tc.desktop, tc.webhook)
		if got != tc.want {
			t.Fatalf("desktop=%q webhook=%q: %q, want %q", tc.desktop, tc.webhook, got, tc.want)
		}
	}
}
