package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

// Red: Local's opt-in restriction changes an ordinary sibling context sender's
// historical POST redirect behavior or its successful submission result.
func TestContextSenderDefaultStillFollowsRedirect(t *testing.T) {
	var originCalls, redirectedPosts atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			redirectedPosts.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	cfg := &config.Config{}
	cfg.Notifications.Webhook = config.WebhookConfig{Enabled: true, URL: origin.URL, Format: "json"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sender := NewWithContext(ctx, cfg)
	defer func() { _ = sender.Shutdown(time.Second) }()
	if err := sender.Send(analyzer.StatusTaskComplete, "TEST-sibling-message", ""); err != nil {
		t.Fatal(err)
	}
	if originCalls.Load() != 1 || redirectedPosts.Load() != 1 {
		t.Fatal("ordinary sender redirect semantics changed")
	}
}
