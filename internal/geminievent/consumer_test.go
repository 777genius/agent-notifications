package geminievent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/geminisource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/webhook"
)

type testClock struct{ seconds atomic.Int64 }

func (c *testClock) Now() (string, float64, error) {
	return "test-boot", float64(c.seconds.Load()), nil
}

type testGate struct {
	channels Channels
	check    func(context.Context, Binding, Channel) bool
}

func (g testGate) Channels(context.Context, Binding) Channels { return g.channels }
func (g testGate) Recheck(ctx context.Context, b Binding, ch Channel) bool {
	return g.check == nil || g.check(ctx, b, ch)
}

type deliveryFunc func(context.Context, notification.Request) notification.Receipt

func (f deliveryFunc) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return f(ctx, r)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func consumerFixture(t *testing.T) (Consumer, geminisource.Facts, notification.Deadline) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{}
	clock.seconds.Store(10)
	cfg := &config.Config{}
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.Webhook = config.WebhookConfig{Enabled: true, URL: "https://example.test/hook", Format: "json"}
	return Consumer{Gate: testGate{channels: Channels{true, true}}, Binding: Binding{"installation", 1}, Config: cfg, Clock: clock, Cache: &RecentCache{Root: root, Clock: clock}},
		geminisource.Facts{SessionID: "PRIVATE_NATIVE_SESSION", Event: geminisource.AfterAgent, Timestamp: "2026-10-01T05:00:00Z"}, notification.Deadline{BootID: "test-boot", NotAfter: 14}
}

// Red condition: concurrent duplicate invocations reach desktop/HTTP again,
// or an uncontended consumer retains its cache lock at a policy handoff.
// TestCacheInterprocessClaim covers concurrent first claims separately.
func TestConcurrentConsumerClaimsEachChannelOnce(t *testing.T) {
	c, facts, deadline := consumerFixture(t)
	var desktops, hooks, rechecks atomic.Int32
	c.Gate = testGate{channels: Channels{true, true}, check: func(ctx context.Context, _ Binding, _ Channel) bool {
		rechecks.Add(1)
		// No other consumer is claiming this marker during a first handoff.
		// Retaining our own lock prevents acquisition and fails the test.
		lockCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		release, err := installruntime.LockExisting(lockCtx, filepath.Join(c.Cache.Root, ".observations.lock"))
		if err != nil {
			t.Errorf("cache lock held at handoff: %v", err)
			return false
		}
		release()
		return true
	}}
	c.Desktop = deliveryFunc(func(_ context.Context, r notification.Request) notification.Receipt {
		desktops.Add(1)
		if r.Content.Body != "Gemini CLI completed a turn" || r.Content.Title != "Gemini CLI" || !r.Silent || r.Policy.SoundEnabled || r.Policy.ClickToFocus || r.Navigation != notification.None || r.Deadline != deadline {
			t.Errorf("unsafe desktop request: %+v", r)
		}
		return notification.Receipt{Status: "submitted"}
	})
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { hooks.Add(1); return nil }
	if result := c.Consume(context.Background(), facts, deadline); result.Desktop != "submitted" || result.Webhook != "submitted" {
		t.Fatalf("first handoff failed: %+v", result)
	}
	if desktops.Load() != 1 || hooks.Load() != 1 || rechecks.Load() != 2 {
		t.Fatalf("first handoff attempts %d/%d, rechecks %d", desktops.Load(), hooks.Load(), rechecks.Load())
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result := c.Consume(context.Background(), facts, deadline); result.Desktop != "duplicate" || result.Webhook != "duplicate" {
				t.Errorf("parallel duplicate classification: %+v", result)
			}
		}()
	}
	wg.Wait()
	if desktops.Load() != 1 || hooks.Load() != 1 || rechecks.Load() != 2 {
		t.Fatalf("same-marker attempts %d/%d, rechecks %d", desktops.Load(), hooks.Load(), rechecks.Load())
	}
	facts.Timestamp = "2026-10-01T05:00:01Z"
	if result := c.Consume(context.Background(), facts, deadline); result.Desktop != "submitted" || result.Webhook != "submitted" {
		t.Fatalf("distinct observation handoff failed: %+v", result)
	}
	if desktops.Load() != 2 || hooks.Load() != 2 {
		t.Fatalf("distinct equal-copy observations merged: %d/%d", desktops.Load(), hooks.Load())
	}
	data, err := os.ReadFile(filepath.Join(c.Cache.Root, "observations.json"))
	if err != nil || strings.Contains(string(data), facts.SessionID) || strings.Contains(string(data), facts.Timestamp) || strings.Contains(string(data), c.Binding.InstallationID) || strings.Contains(string(data), "test-boot") {
		t.Fatalf("private cache content: %s / %v", data, err)
	}
}

// Red condition: disabled/unselected Gemini channels borrow another channel's
// consent, fall back after failure, or reattempt a failed marker.
func TestChannelConsentIndependentAndNoRetry(t *testing.T) {
	for _, channels := range []Channels{{Desktop: true}, {Webhook: true}, {true, true}, {}} {
		t.Run(stringChannelChoice(channels), func(t *testing.T) {
			c, facts, deadline := consumerFixture(t)
			c.Gate = testGate{channels: channels}
			var desktops, hooks int
			c.Desktop = deliveryFunc(func(context.Context, notification.Request) notification.Receipt {
				desktops++
				return notification.Receipt{Status: "unknown", Reason: "PRIVATE_BACKEND_ERROR"}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
				hooks++
				return errors.New("PRIVATE_TRANSPORT_ERROR")
			}
			r := c.Consume(context.Background(), facts, deadline)
			c.Consume(context.Background(), facts, deadline)
			if desktops != boolInt(channels.Desktop) || hooks != boolInt(channels.Webhook) {
				t.Fatalf("consent/failure attempts %d/%d", desktops, hooks)
			}
			raw, _ := json.Marshal(r)
			if strings.Contains(string(raw), "PRIVATE_") {
				t.Fatalf("receipt exposes backend cause: %s", raw)
			}
		})
	}
	// A per-status channel disable still overrides an installed channel choice.
	c, facts, deadline := consumerFixture(t)
	disabled := false
	c.Config.Statuses = map[string]config.StatusInfo{"task_complete": {Webhook: &config.StatusChannelConfig{Enabled: &disabled}}}
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
		t.Fatal("status-disabled webhook reached")
		return nil
	}
	c.Consume(context.Background(), facts, deadline)
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func stringChannelChoice(c Channels) string {
	if c.Desktop && c.Webhook {
		return "both"
	}
	if c.Desktop {
		return "desktop"
	}
	if c.Webhook {
		return "webhook"
	}
	return "none"
}

// Red condition: hook recursion, unsupported notification subtypes, absent
// native identity/cache or revoked generation reaches any delivery effect.
func TestUnsupportedAndRevokedFactsAreSilent(t *testing.T) {
	for _, change := range []func(*Consumer, *geminisource.Facts){
		func(_ *Consumer, f *geminisource.Facts) { f.StopHookActive = true },
		func(_ *Consumer, f *geminisource.Facts) { f.Event, f.Subtype = geminisource.Notification, "unknown" },
		func(_ *Consumer, f *geminisource.Facts) { f.SessionID = "" },
		func(c *Consumer, _ *geminisource.Facts) { c.Cache = nil },
		func(c *Consumer, _ *geminisource.Facts) { c.Gate = nil },
		func(c *Consumer, _ *geminisource.Facts) { c.Binding.Generation = 0 },
		func(c *Consumer, _ *geminisource.Facts) {
			c.Gate = testGate{channels: Channels{true, true}, check: func(context.Context, Binding, Channel) bool { return false }}
		},
	} {
		c, facts, deadline := consumerFixture(t)
		c.Desktop = deliveryFunc(func(context.Context, notification.Request) notification.Receipt {
			t.Error("suppressed desktop reached")
			return notification.Receipt{}
		})
		c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
			t.Error("suppressed webhook reached")
			return nil
		}
		change(&c, &facts)
		if r := c.Consume(context.Background(), facts, deadline); r.Status == "submitted" {
			t.Fatalf("suppression reported submission: %+v", r)
		}
	}
}

// Red condition: missing timestamp uses an invented cross-invocation marker,
// merging independent invocations instead of allowing one attempt per call.
func TestMissingTimestampAttemptsAreInvocationLocal(t *testing.T) {
	c, facts, deadline := consumerFixture(t)
	facts.Timestamp = ""
	var desktops, hooks int
	c.Desktop = deliveryFunc(func(context.Context, notification.Request) notification.Receipt {
		desktops++
		return notification.Receipt{Status: "submitted"}
	})
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { hooks++; return nil }
	c.Consume(context.Background(), facts, deadline)
	c.Consume(context.Background(), facts, deadline)
	if desktops != 2 || hooks != 2 {
		t.Fatalf("invocation-local attempts %d/%d", desktops, hooks)
	}
	if _, err := os.Stat(filepath.Join(c.Cache.Root, "observations.json")); !os.IsNotExist(err) {
		t.Fatal("invented timestamp persisted")
	}
}

// Red condition: configured private titles/headers/fields or native identifiers
// enter a real sender payload; retry settings create extra permission attempts.
func TestPermissionPrivatePayloadThroughExistingSender(t *testing.T) {
	c, facts, deadline := consumerFixture(t)
	c.Gate = testGate{channels: Channels{Webhook: true}}
	facts.Event, facts.Subtype = geminisource.Notification, geminisource.ToolPermission
	c.Config.Notifications.Webhook.Headers = map[string]string{"X-Private": "PRIVATE_CONFIG_HEADER"}
	c.Config.Notifications.Webhook.PayloadFields = map[string]interface{}{"private": "PRIVATE_CONFIG_FIELD"}
	c.Config.Notifications.Webhook.Retry = config.RetryConfig{Enabled: true, MaxAttempts: 4}
	c.Config.Statuses = map[string]config.StatusInfo{"permission_request": {Title: "PRIVATE_CONFIG_TITLE", Sound: "PRIVATE_CONFIG_PATH"}}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	attempts := 0
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "PRIVATE_") || r.Header.Get("X-Private") != "" {
			t.Fatalf("private payload/header: %s / %v", data, r.Header)
		}
		var payload map[string]interface{}
		if json.Unmarshal(data, &payload) != nil || payload["agent_source"] != "gemini" || payload["title"] != "Gemini CLI" || payload["message"] != "Gemini CLI requested tool permission" || payload["status"] != "permission_request" || payload["session_id"] != "" {
			t.Fatalf("wrong business payload: %s", data)
		}
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("PRIVATE_REMOTE_RESPONSE"))}, nil
	})
	c.SendWebhook = func(ctx context.Context, cfg *config.Config, payload webhook.SendContext) error {
		sender := webhook.NewWithContext(ctx, cfg)
		defer func() { _ = sender.Shutdown(time.Millisecond) }()
		return sender.SendWithContext(payload)
	}
	r := c.Consume(context.Background(), facts, deadline)
	if attempts != 1 || r.Webhook != "unknown" {
		t.Fatalf("permission attempts=%d result=%+v", attempts, r)
	}
}

// Red condition: work already spent in source/locks receives a fresh timeout
// at desktop handoff, allowing the effect past the invocation's parent budget.
func TestRemainingParentDeadlineReachesBackend(t *testing.T) {
	c, facts, deadline := consumerFixture(t)
	c.Gate = testGate{channels: Channels{Desktop: true}}
	// Cache durability has its own tests. This boundary verifies the inherited
	// deadline and cancellation without a short filesystem timing assumption.
	facts.Timestamp = ""
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	parentEnd, _ := parent.Deadline()
	c.Desktop = deliveryFunc(func(ctx context.Context, r notification.Request) notification.Receipt {
		end, ok := ctx.Deadline()
		if !ok || !end.Equal(parentEnd) || r.Deadline != deadline {
			t.Errorf("budget refreshed: %+v", r.Deadline)
		}
		cancel()
		<-ctx.Done()
		return notification.Receipt{Status: "unknown"}
	})
	start := time.Now()
	r := c.Consume(parent, facts, deadline)
	if r.Desktop != "unknown" || time.Since(start) > time.Second {
		t.Fatalf("unbounded handoff: %+v", r)
	}
}

// Red condition: suspend advances continuous time beyond the original deadline
// while Go's timer still permits HTTP, or a boot change grants another budget.
func TestContinuousDeadlineCancelsHTTPAndRejectsExpiredObservation(t *testing.T) {
	c, facts, deadline := consumerFixture(t)
	c.Gate = testGate{channels: Channels{Webhook: true}}
	c.SendWebhook = func(ctx context.Context, _ *config.Config, _ webhook.SendContext) error {
		c.Clock.(*testClock).seconds.Store(15)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			t.Error("continuous expiry did not cancel HTTP")
			return nil
		}
	}
	r := c.Consume(context.Background(), facts, deadline)
	if r.Webhook != "unknown" {
		t.Fatalf("suspended outcome = %+v", r)
	}
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
		t.Error("expired webhook reached")
		return nil
	}
	if r = c.Consume(context.Background(), facts, deadline); r.Reason != "expired" {
		t.Fatalf("expired deadline refreshed: %+v", r)
	}
	c.Clock.(*testClock).seconds.Store(10)
	deadline.BootID = "previous-boot"
	if r = c.Consume(context.Background(), facts, deadline); r.Reason != "expired" {
		t.Fatalf("changed boot accepted: %+v", r)
	}
}

// Red condition: admission occurs after native decoding and gives the backend
// another full budget, despite continuous time already spent in source/input.
func TestAdmissionCapturesBudgetBeforeTypedSource(t *testing.T) {
	c, _, _ := consumerFixture(t)
	c.Gate = testGate{channels: Channels{Desktop: true}}
	ctx, deadline, cancel, err := Admission(context.Background(), c.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	facts, err := geminisource.Decode(ctx, geminisource.AfterAgent, []byte(`{"session_id":"s","hook_event_name":"AfterAgent","timestamp":"2026-10-01T05:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	c.Clock.(*testClock).seconds.Store(12)
	c.Desktop = deliveryFunc(func(backend context.Context, request notification.Request) notification.Receipt {
		end, ok := backend.Deadline()
		if !ok || time.Until(end) > 2*time.Second || request.Deadline.NotAfter != 14 {
			t.Errorf("source budget refreshed: %+v", request.Deadline)
		}
		return notification.Receipt{Status: "submitted"}
	})
	if r := c.Consume(ctx, facts, deadline); r.Desktop != "submitted" {
		t.Fatalf("typed source handoff failed: %+v", r)
	}
}

type panicWatchClock struct {
	testClock
	panicNow atomic.Bool
}

func (c *panicWatchClock) Now() (string, float64, error) {
	if c.panicNow.Load() {
		panic("PRIVATE_CLOCK_FAILURE")
	}
	return c.testClock.Now()
}

// Red condition: a clock panic in the budget watcher escapes synchronous
// entrypoint recovery, crashes the hook process and leaves HTTP uncanceled.
func TestClockWatcherPanicCancelsEffectWithoutCrashing(t *testing.T) {
	c, facts, deadline := consumerFixture(t)
	// This test exercises cancellation during an effect, independently of
	// durable-cache admission. A missing native timestamp is invocation-scoped.
	facts.Timestamp = ""
	clock := &panicWatchClock{}
	clock.seconds.Store(10)
	c.Clock, c.Cache.Clock = clock, clock
	c.Gate = testGate{channels: Channels{Webhook: true}}
	calls := 0
	c.SendWebhook = func(ctx context.Context, _ *config.Config, _ webhook.SendContext) error {
		calls++
		if err := ctx.Err(); err != nil {
			t.Errorf("effect started with a canceled context: %v", err)
			return err
		}
		// All synchronous admission checks have finished; fail the clock only
		// while its asynchronous watcher owns cancellation of the effect.
		clock.panicNow.Store(true)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			t.Error("clock failure left effect uncanceled")
			return nil
		}
	}
	result := c.Consume(context.Background(), facts, deadline)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Webhook != "unknown" || strings.Contains(string(data), "PRIVATE_") {
		t.Fatalf("unsafe clock failure outcome: %s, effect calls %d", data, calls)
	}
}
