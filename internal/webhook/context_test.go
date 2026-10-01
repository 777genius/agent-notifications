package webhook

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

type cancellationTransport func(*http.Request) (*http.Response, error)

func (f cancellationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Red condition: New gives requests a background context and ten-second HTTP
// timeout, so an invoking hook's cancellation does not stop its HTTP attempt.
func TestNewWithContextCancelsActualHTTPRequest(t *testing.T) {
	cfg := &config.Config{}
	cfg.Notifications.Webhook = config.WebhookConfig{Enabled: true, URL: "https://example.test/hook", Format: "json"}
	parent, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	sender := NewWithContext(parent, cfg)
	var attempts atomic.Int32
	sender.client.Transport = cancellationTransport(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		end, ok := r.Context().Deadline()
		if !ok || time.Until(end) > 50*time.Millisecond {
			t.Error("request did not inherit parent deadline")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	start := time.Now()
	if err := sender.SendWithContext(SendContext{Status: analyzer.StatusTaskComplete, Message: "Gemini CLI completed a turn", AgentSource: "gemini"}); err == nil {
		t.Fatal("canceled HTTP reported success")
	}
	if attempts.Load() != 1 || time.Since(start) > time.Second {
		t.Fatalf("HTTP attempts/time = %d/%s", attempts.Load(), time.Since(start))
	}
	if err := sender.Shutdown(time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
