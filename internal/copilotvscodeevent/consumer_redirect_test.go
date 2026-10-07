package copilotvscodeevent_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	local "github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
)

// Red: the context sender follows a redirect, reports submitted, or an uncertain
// response releases its claim and lets the identical Stop send a second time.
func TestLocalRedirectRetainsUncertainClaim(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			checkLocalRedirect(t, status)
		})
	}
}

func checkLocalRedirect(t *testing.T, status int) {
	t.Helper()
	var originCalls, redirectedCalls atomic.Int32
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer redirected.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Location", redirected.URL+"/TEST-unconfigured")
		w.WriteHeader(status)
	}))
	defer origin.Close()
	root, clock := cacheRoot(t), notifier.SystemBootClock{}
	g := testGate{channels: local.Channels{Webhook: true}}
	c := local.Consumer{Binding: binding(), Gate: g, Config: cfg(t, origin.URL), Clock: clock, Cache: &observation.RecentCache{Root: root, Clock: clock}}
	c.SendWebhook = leasedSender(t, g, c.Binding, root)
	// Exact independent review input; public SDK, existing sender and one lease.
	f := facts(t, `{"hook_event_name":"Stop","timestamp":"2026-10-02T06:45:01Z","stop_hook_active":false,"session_id":"TEST-session"}`)
	first := consume(t, c, f)
	if first.Status != "unknown" || first.Webhook != "unknown" || first.Reason != "delivery_uncertain" {
		t.Fatalf("redirect receipt: %+v", first)
	}
	path := filepath.Join(root, "observations.json")
	claimed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second := consume(t, c, f)
	retained, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%d origin=%d redirected=%d first=%+v repeat=%+v", status, originCalls.Load(), redirectedCalls.Load(), first, second)
	if originCalls.Load() != 1 || redirectedCalls.Load() != 0 || second.Status != "suppressed" || second.Reason != "duplicate" || !bytes.Equal(claimed, retained) {
		t.Fatal("redirect replayed or uncertain claim was not retained")
	}
}
