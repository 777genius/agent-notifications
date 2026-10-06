package cursorevent_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	cursor "github.com/777genius/agent-notifications/internal/cursorevent"
	source "github.com/777genius/agent-notifications/internal/cursorsource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/google/uuid"
)

const frame = `{"hook_event_name":"stop","conversation_id":"TEST-private-conversation-秘密","generation_id":"TEST-private-generation","status":"completed","workspace_roots":["TEST-private-workspace"],"model":"TEST-private-model","model_params":[{"id":"TEST-private-param","value":"TEST-private-value"}],"user_email":"TEST-private-email","transcript_path":"TEST-private-transcript","prompt":"TEST-private-prompt","installation_id":"UNTRUSTED","binding_id":"UNTRUSTED","profile_identity":"UNTRUSTED","product":"UNTRUSTED","generation":999}`

type testClock struct {
	seconds       atomic.Int64
	fail, newBoot atomic.Bool
}

func (c *testClock) Now() (string, float64, error) {
	if c.fail.Load() {
		return "", 0, errors.New("TEST-private-clock")
	}
	boot := "TEST-boot"
	if c.newBoot.Load() {
		boot = "TEST-new-boot"
	}
	return boot, 100 + float64(c.seconds.Load()), nil
}

type testGate struct {
	channels cursor.Channels
	recheck  func(context.Context, cursor.Binding, cursor.Channel) bool
}

func (g testGate) Channels(context.Context, cursor.Binding) cursor.Channels { return g.channels }
func (g testGate) Recheck(ctx context.Context, b cursor.Binding, ch cursor.Channel) bool {
	return g.recheck == nil || g.recheck(ctx, b, ch)
}

type desktopFunc func(context.Context, notification.Request) notification.Receipt

func (f desktopFunc) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return f(ctx, r)
}

func binding() cursor.Binding {
	return cursor.Binding{InstallationID: "TEST-private-install", BindingID: "TEST-private-binding", ProfileIdentity: "TEST-private-profile", Product: "cursor", Generation: 1}
}
func cfg(t *testing.T, endpoint string) *config.Config {
	t.Helper()
	url, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise a real schema-1 Config view with hostile enrichment; schema-2
	// profile isolation is exercised separately below.
	data := `{"schemaVersion":1,"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true,"url":` + string(url) + `,"preset":"telegram","chat_id":"TEST-private-chat","headers":{"X-Private":"TEST-private-header"},"payloadFields":{"private":"TEST-private-extra"},"retry":{"enabled":true,"maxAttempts":7}}},"statuses":{"agent_stopping":{"title":"TEST-private-title","sound":"TEST-private-sound"},"task_complete":{"enabled":false}}}`
	d, err := config.ParseDocument([]byte(data), "/TEST/config.json", false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Effective(config.AssetContext{Agent: config.AgentCursor, PluginRoot: "/TEST"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Failure caught: global/foreign opt-in overrides Cursor opt-out, or Cursor's
// selected channel/status policy is ignored before real claims and delivery.
func TestSchema2CursorProfileIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, global, profile string
		desktop, webhook      bool
	}{
		{"channel opt-out", "true", `"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}`, false, false},
		{"status opt-out", "true", `"statuses":{"agent_stopping":{"enabled":false}}`, false, false},
		{"desktop status opt-out", "true", `"statuses":{"agent_stopping":{"desktop":{"enabled":false}}}`, false, true},
		{"webhook status opt-out", "true", `"statuses":{"agent_stopping":{"webhook":{"enabled":false}}}`, true, false},
		{"Cursor opt-in", "false", `"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true}}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || payload["status"] != "agent_stopping" || payload["agent_source"] != "cursor" || payload["title"] != "Cursor CLI" || payload["message"] != "Cursor CLI is stopping" {
					t.Error("wrong fixed stopping webhook")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			endpoint, err := json.Marshal(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			data := `{"schemaVersion":2,"notifications":{"desktop":{"enabled":` + tc.global + `},"webhook":{"enabled":` + tc.global + `,"url":` + string(endpoint) + `}},"agents":{"gemini":{"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true}}},"cursor":{` + tc.profile + `}}}`
			d, err := config.ParseDocument([]byte(data), "/TEST/config.json", false)
			if err != nil {
				t.Fatal(err)
			}
			view, err := d.Effective(config.AssetContext{Agent: config.AgentCursor, PluginRoot: "/TEST"})
			if err != nil {
				t.Fatal(err)
			}
			if view.IsStatusDesktopEnabled("agent_stopping") != tc.desktop || view.IsStatusWebhookEnabled("agent_stopping") != tc.webhook {
				t.Fatal("Effective selected wrong Cursor profile", view.IsStatusDesktopEnabled("agent_stopping"), view.IsStatusWebhookEnabled("agent_stopping"))
			}
			c, _ := fixture(t)
			c.Config = view
			desktopCalls := 0
			c.Desktop = desktopFunc(func(ctx context.Context, r notification.Request) notification.Receipt {
				defer effectLease(t, ctx, c, cursor.DesktopChannel)()
				desktopCalls++
				if r.Content != (notification.Content{Title: "Cursor CLI", Body: "Cursor CLI is stopping", Category: "info"}) || !r.Silent || r.Navigation != notification.None || r.Policy != (notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}) {
					t.Fatal("wrong fixed stopping desktop request")
				}
				return notification.Receipt{Status: "submitted"}
			})
			c.SendWebhook = func(ctx context.Context, safe *config.Config, p webhook.SendContext) error {
				defer effectLease(t, ctx, c, cursor.WebhookChannel)()
				if safe.Notifications.Webhook.URL != server.URL {
					t.Fatal("lost selected endpoint")
				}
				sender := webhook.NewWithContext(ctx, safe)
				defer func() { _ = sender.Shutdown(50 * time.Millisecond) }()
				return sender.SendWithContext(p)
			}
			f := facts(t, frame)
			r := consume(t, c, f)
			if (desktopCalls == 1) != tc.desktop || (requests.Load() == 1) != tc.webhook || desktopCalls > 1 || requests.Load() > 1 {
				t.Fatal("selected policy did not control effects", r, desktopCalls, requests.Load())
			}
			if !tc.desktop && !tc.webhook {
				if r != (cursor.Receipt{Status: "suppressed", Reason: "channels_disabled"}) {
					t.Fatal("Cursor opt-out bypassed", r)
				}
				if entries, err := os.ReadDir(c.Cache.Root); err != nil || len(entries) != 0 {
					t.Fatal("Cursor opt-out consumed claims", err)
				}
				return
			}
			want := cursor.Receipt{Status: "submitted"}
			var bits uint8
			if tc.desktop {
				want.Desktop, bits = "submitted", bits|1
			}
			if tc.webhook {
				want.Webhook, bits = "submitted", bits|2
			}
			state, _ := cacheState(t, c.Cache.Root)
			if r != want || len(state.Entries) != 1 || state.Entries[0].Bits != bits {
				t.Fatal("selected channels/claims differ", r, state)
			}
			if duplicate := consume(t, c, f); duplicate.Status != "suppressed" || duplicate.Reason != "duplicate" || desktopCalls > 1 || requests.Load() > 1 {
				t.Fatal("selected view replayed effects", duplicate)
			}
		})
	}
}
func facts(t *testing.T, input string) source.Facts {
	t.Helper()
	f, err := source.Decode(context.Background(), source.Selector, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func fixture(t *testing.T) (cursor.Consumer, *testClock) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{}
	return cursor.Consumer{Binding: binding(), Gate: testGate{channels: cursor.Channels{Desktop: true, Webhook: true}}, Config: cfg(t, "http://127.0.0.1:1"), Clock: clock, Cache: &observation.RecentCache{Root: root, Clock: clock}}, clock
}
func consume(t *testing.T, c cursor.Consumer, f source.Facts) cursor.Receipt {
	t.Helper()
	ctx, d, cancel, err := observation.Admission(context.Background(), c.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	return c.Consume(ctx, f, d)
}

type cacheDocument struct {
	Entries []struct {
		Key   string
		Until float64
		Bits  uint8
	}
}

func cacheState(t *testing.T, root string) (cacheDocument, []byte) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "observations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d cacheDocument
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d, b
}

// This TEST effect owner releases the cache lock probe, then holds one retained
// effect lease through recheck and delivery. No installed eligibility is claimed.
func effectLease(t *testing.T, ctx context.Context, c cursor.Consumer, ch cursor.Channel) func() {
	t.Helper()
	release, err := installruntime.LockExisting(ctx, filepath.Join(c.Cache.Root, ".observations.lock"))
	if err != nil {
		t.Fatalf("consumer retained cache lock at effect: %v", err)
	}
	release()
	release, err = installruntime.Lock(ctx, filepath.Join(c.Cache.Root, "TEST-effect.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Gate.Recheck(ctx, c.Binding, ch) {
		release()
		t.Fatal("effect owner recheck denied")
	}
	return release
}

// Failure caught: private content/IDs/config enrichment reaches effects or
// diagnostics, a completed loop is labeled task-success, or leases are nested.
func TestSDKToStoppingEffectsFixedCopyAndPrivacy(t *testing.T) {
	logRoot := t.TempDir()
	logger, err := logging.InitLogger(logRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()
	var requests atomic.Int32
	captured := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		captured <- b
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Private") != "" {
			t.Error("unexpected HTTP contract")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c, _ := fixture(t)
	c.Config = cfg(t, server.URL)
	originalConfig, err := json.Marshal(c.Config)
	if err != nil {
		t.Fatal(err)
	}
	var rechecks, desktopCalls int
	c.Gate = testGate{channels: cursor.Channels{Desktop: true, Webhook: true}, recheck: func(_ context.Context, b cursor.Binding, ch cursor.Channel) bool {
		if b != binding() || (ch != cursor.DesktopChannel && ch != cursor.WebhookChannel) {
			t.Fatal("untrusted binding/channel reached gate")
		}
		rechecks++
		return true
	}}
	ctx, d, cancel, err := observation.Admission(context.Background(), c.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	c.Desktop = desktopFunc(func(ctx context.Context, r notification.Request) notification.Receipt {
		defer effectLease(t, ctx, c, cursor.DesktopChannel)()
		desktopCalls++
		if r.Content != (notification.Content{Title: "Cursor CLI", Body: "Cursor CLI is stopping", Category: "info"}) || r.Navigation != notification.None || !r.Silent || r.Target != (notification.DesktopTarget{}) || r.Deadline != d || r.Policy != (notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}) {
			t.Fatalf("unsafe desktop request: %+v", r)
		}
		if _, err := uuid.Parse(r.CorrelationID); err != nil {
			t.Fatal("missing private delivery correlation")
		}
		return notification.Receipt{Status: "submitted"}
	})
	c.SendWebhook = func(ctx context.Context, safe *config.Config, p webhook.SendContext) error {
		defer effectLease(t, ctx, c, cursor.WebhookChannel)()
		want := &config.Config{Statuses: map[string]config.StatusInfo{"agent_stopping": {Title: "Cursor CLI"}}}
		want.Notifications.Webhook = config.WebhookConfig{Enabled: true, URL: server.URL, Preset: "custom", Format: "json"}
		if safe == c.Config || !reflect.DeepEqual(safe, want) || p != (webhook.SendContext{Status: analyzer.StatusAgentStopping, AgentSource: "cursor", Message: "Cursor CLI is stopping", RawBody: "Cursor CLI is stopping"}) {
			t.Fatal("webhook boundary allowed native/config enrichment")
		}
		sender := webhook.NewWithContext(ctx, safe)
		defer func() { _ = sender.Shutdown(50 * time.Millisecond) }()
		return sender.SendWithContext(p)
	}
	f := facts(t, frame)
	r := c.Consume(ctx, f, d)
	if r.Status != "submitted" || r.Desktop != "submitted" || r.Webhook != "submitted" || desktopCalls != 1 || requests.Load() != 1 || rechecks != 4 {
		t.Fatal("effects/rechecks failed", r)
	}
	body := <-captured
	var p map[string]any
	if json.Unmarshal(body, &p) != nil || p["status"] != "agent_stopping" || p["agent_source"] != "cursor" || p["message"] != "Cursor CLI is stopping" || p["title"] != "Cursor CLI" {
		t.Fatalf("wrong stopping payload: %s", body)
	}
	if r := consume(t, c, f); r.Reason != "duplicate" || desktopCalls != 1 || requests.Load() != 1 {
		t.Fatal("duplicate effect", r)
	}
	state, cache := cacheState(t, c.Cache.Root)
	if len(state.Entries) != 1 || state.Entries[0].Bits != 3 || state.Entries[0].Until != 160 || len(state.Entries[0].Key) != 64 {
		t.Fatal("invalid shared privacy claims", state)
	}
	receipt, _ := json.Marshal(r)
	diagnostics, err := os.ReadFile(filepath.Join(logRoot, "notification-debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{body, cache, receipt, diagnostics} {
		if bytes.Contains(b, []byte("TEST-private")) || bytes.Contains(b, []byte("UNTRUSTED")) {
			t.Fatal("private data escaped")
		}
	}
	after, _ := json.Marshal(c.Config)
	if !bytes.Equal(after, originalConfig) {
		t.Fatal("consumer modified caller configuration")
	}
}

// Failure caught: a second channel extends dedup expiry, trusted/native scopes
// merge, or nil loop count becomes the same identity as explicit zero.
func TestRealCacheChannelAndBindingNativeScoping(t *testing.T) {
	c, clock := fixture(t)
	desktop, webhooks := 0, 0
	c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
		desktop++
		return notification.Receipt{Status: "submitted"}
	})
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { webhooks++; return nil }
	f := facts(t, frame)
	c.Gate = testGate{channels: cursor.Channels{Desktop: true}}
	if r := consume(t, c, f); r.Desktop != "submitted" || r.Webhook != "" {
		t.Fatal(r)
	}
	before, _ := cacheState(t, c.Cache.Root)
	clock.seconds.Store(30)
	c.Gate = testGate{channels: cursor.Channels{Desktop: true, Webhook: true}}
	if r := consume(t, c, f); r.Desktop != "suppressed" || r.Webhook != "submitted" || desktop != 1 || webhooks != 1 {
		t.Fatal("channel claim did not isolate", r)
	}
	after, _ := cacheState(t, c.Cache.Root)
	if len(after.Entries) != 1 || after.Entries[0].Key != before.Entries[0].Key || after.Entries[0].Until != 160 || after.Entries[0].Bits != 3 {
		t.Fatal("channel renewed shared window")
	}
	variants := []struct {
		name   string
		change func(*cursor.Consumer, *source.Facts)
	}{
		{"installation", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.InstallationID += "-other" }},
		{"binding", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.BindingID += "-other" }},
		{"profile", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.ProfileIdentity += "-other" }},
		{"trusted generation", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.Generation++ }},
		{"conversation", func(_ *cursor.Consumer, f *source.Facts) { f.ConversationID += "-other" }},
		{"native generation", func(_ *cursor.Consumer, f *source.Facts) { f.GenerationID += "-other" }},
		{"aborted", func(_ *cursor.Consumer, f *source.Facts) { f.Status = "aborted" }},
		{"error", func(_ *cursor.Consumer, f *source.Facts) { f.Status = "error" }},
		{"explicit zero", func(_ *cursor.Consumer, f *source.Facts) { f.LoopCountKnown = true }},
		{"explicit one", func(_ *cursor.Consumer, f *source.Facts) { f.LoopCountKnown = true; f.LoopCount = 1 }},
		{"framing a bc", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.InstallationID = "a"; c.Binding.BindingID = "bc" }},
		{"framing ab c", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.InstallationID = "ab"; c.Binding.BindingID = "c" }},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			other, ff := c, f
			v.change(&other, &ff)
			if r := consume(t, other, ff); r.Desktop != "submitted" || r.Webhook != "submitted" {
				t.Fatal("distinct scope suppressed", r)
			}
			if r := consume(t, other, ff); r.Reason != "duplicate" || r.Status != "suppressed" {
				t.Fatal("mandatory dedup skipped", r)
			}
		})
	}
	if desktop != 1+len(variants) || webhooks != 1+len(variants) {
		t.Fatal("duplicate/distinct effect counts", desktop, webhooks)
	}
	state, _ := cacheState(t, c.Cache.Root)
	if len(state.Entries) != 1+len(variants) {
		t.Fatal("distinct cache scopes merged")
	}
}

// Failure caught: unknown native status, invalid facts/authority, missing gate,
// refusal or disabled stopping settings have effects or consume shared claims.
func TestUnknownInvalidAndUnqualifiedHaveNoEffect(t *testing.T) {
	for _, status := range []string{`"future"`, `""`, `null`, "missing"} {
		t.Run("status/"+status, func(t *testing.T) {
			c, _ := fixture(t)
			c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
				t.Fatal("unknown desktop effect")
				return notification.Receipt{}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
				t.Fatal("unknown webhook effect")
				return nil
			}
			input := strings.Replace(frame, `"status":"completed"`, `"status":`+status, 1)
			if status == "missing" {
				input = strings.Replace(frame, `"status":"completed",`, "", 1)
			}
			if r := consume(t, c, facts(t, input)); r.Status != "suppressed" || r.Reason != "invalid_fact" {
				t.Fatal(r)
			}
			if entries, err := os.ReadDir(c.Cache.Root); err != nil || len(entries) != 0 {
				t.Fatal("unknown status claimed cache")
			}
		})
	}
	no := false
	variants := []struct {
		name   string
		change func(*cursor.Consumer, *source.Facts)
	}{
		{"conversation absent", func(_ *cursor.Consumer, f *source.Facts) { f.ConversationID = "" }},
		{"native generation absent", func(_ *cursor.Consumer, f *source.Facts) { f.GenerationID = "" }},
		{"control", func(_ *cursor.Consumer, f *source.Facts) { f.GenerationID = "bad\x00" }},
		{"utf8", func(_ *cursor.Consumer, f *source.Facts) { f.ConversationID = "bad\xff" }},
		{"ID bound", func(_ *cursor.Consumer, f *source.Facts) { f.ConversationID = strings.Repeat("x", 257) }},
		{"wrong event", func(_ *cursor.Consumer, f *source.Facts) { f.Event = "Stop" }},
		{"negative loop", func(_ *cursor.Consumer, f *source.Facts) { f.LoopCountKnown = true; f.LoopCount = -1 }},
		{"overflow loop", func(_ *cursor.Consumer, f *source.Facts) { f.LoopCountKnown = true; f.LoopCount = 2147483648 }},
		{"no gate", func(c *cursor.Consumer, _ *source.Facts) { c.Gate = nil }},
		{"refused", func(c *cursor.Consumer, _ *source.Facts) { c.Gate = testGate{} }},
		{"installation absent", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.InstallationID = "" }},
		{"binding absent", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.BindingID = "" }},
		{"profile absent", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.ProfileIdentity = "" }},
		{"authority control", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.ProfileIdentity = "bad\x00" }},
		{"generation zero", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.Generation = 0 }},
		{"wrong product", func(c *cursor.Consumer, _ *source.Facts) { c.Binding.Product = "copilot-vscode" }},
		{"no config", func(c *cursor.Consumer, _ *source.Facts) { c.Config = nil }},
		{"channels disabled", func(c *cursor.Consumer, _ *source.Facts) {
			c.Config.Notifications.Desktop.Enabled = false
			c.Config.Notifications.Webhook.Enabled = false
		}},
		{"status disabled", func(c *cursor.Consumer, _ *source.Facts) {
			c.Config.Statuses["agent_stopping"] = config.StatusInfo{Enabled: &no}
		}},
		{"status channels disabled", func(c *cursor.Consumer, _ *source.Facts) {
			c.Config.Statuses["agent_stopping"] = config.StatusInfo{Desktop: &config.StatusChannelConfig{Enabled: &no}, Webhook: &config.StatusChannelConfig{Enabled: &no}}
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			c, _ := fixture(t)
			f := facts(t, frame)
			calls := 0
			c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
				calls++
				return notification.Receipt{Status: "submitted"}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { calls++; return nil }
			v.change(&c, &f)
			if r := consume(t, c, f); r.Status != "suppressed" || calls != 0 {
				t.Fatal("ineligible effect", r, calls)
			}
			if entries, err := os.ReadDir(c.Cache.Root); err != nil || len(entries) != 0 {
				t.Fatal("ineligible event claimed cache")
			}
		})
	}
}

// Failure caught: gate revocation or budget expiry after a persisted claim
// permits an effect, refreshes the admission deadline, or rolls back the claim.
func TestRecheckAndDeadlineAfterClaimRetainSuppression(t *testing.T) {
	for _, mode := range []string{"revoked", "expired", "cancelled", "panic"} {
		t.Run(mode, func(t *testing.T) {
			c, clock := fixture(t)
			c.Gate = testGate{channels: cursor.Channels{Desktop: true}}
			calls := 0
			c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
				calls++
				return notification.Receipt{Status: "submitted"}
			})
			f := facts(t, frame)
			ctx, d, cancel, err := observation.Admission(context.Background(), clock)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			rechecked := false
			c.Gate = testGate{channels: cursor.Channels{Desktop: true}, recheck: func(context.Context, cursor.Binding, cursor.Channel) bool {
				rechecked = true
				state, _ := cacheState(t, c.Cache.Root)
				if len(state.Entries) != 1 || state.Entries[0].Bits != 1 {
					t.Fatal("gate rechecked before claim")
				}
				switch mode {
				case "revoked":
					return false
				case "expired":
					clock.seconds.Store(4)
				case "cancelled":
					cancel()
				case "panic":
					panic("TEST-private-gate")
				}
				return true
			}}
			r := c.Consume(ctx, f, d)
			if !rechecked || calls != 0 || r.Status != "suppressed" {
				t.Fatal("post-claim denial admitted", r)
			}
			want := map[string]string{"revoked": "revoked", "expired": "expired", "cancelled": "expired", "panic": "consumer_unavailable"}[mode]
			if r.Reason != want {
				t.Fatal("wrong denial", r)
			}
			clock.seconds.Store(0)
			c.Gate = testGate{channels: cursor.Channels{Desktop: true}}
			if r := consume(t, c, f); r.Reason != "duplicate" || calls != 0 {
				t.Fatal("denied claim was rolled back", r)
			}
		})
	}
}

// Failure caught: expired/foreign-boot/broken-clock admission is silently
// renewed, or missing cache permits native IDs to bypass mandatory dedup.
func TestAdmissionAndCacheFailuresDenyEffects(t *testing.T) {
	for _, mode := range []string{"expired", "boot", "clock", "missing cache", "corrupt cache"} {
		t.Run(mode, func(t *testing.T) {
			c, clock := fixture(t)
			calls := 0
			c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
				calls++
				return notification.Receipt{Status: "submitted"}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { calls++; return nil }
			ctx, d, cancel, err := observation.Admission(context.Background(), clock)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			switch mode {
			case "expired":
				clock.seconds.Store(4)
			case "boot":
				clock.newBoot.Store(true)
			case "clock":
				clock.fail.Store(true)
			case "missing cache":
				c.Cache = nil
			case "corrupt cache":
				if err := os.WriteFile(filepath.Join(c.Cache.Root, "observations.json"), []byte("TEST-private-corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := c.Consume(ctx, facts(t, frame), d)
			if r.Status != "suppressed" || calls != 0 {
				t.Fatal("failure admitted effect", r)
			}
			want := "expired"
			if strings.Contains(mode, "cache") {
				want = "cache_unavailable"
			}
			if r.Reason != want {
				t.Fatal("wrong failure reason", r)
			}
		})
	}
}

// Failure caught: an uncertain/panicking effect loses its attempted channel
// claim and retries or routes to the other channel on the next callback.
func TestUncertainEffectsKeepClaimsWithoutRetryOrFallback(t *testing.T) {
	for _, mode := range []string{"desktop panic", "desktop unknown", "webhook panic", "webhook error"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := fixture(t)
			desktop, webhooks := 0, 0
			c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
				desktop++
				if mode == "desktop panic" {
					panic("TEST-private-effect")
				}
				return notification.Receipt{Status: "unknown", Reason: "TEST-private-receipt"}
			})
			c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
				webhooks++
				if mode == "webhook panic" {
					panic("TEST-private-effect")
				}
				return errors.New("TEST-private-effect")
			}
			isDesktop := strings.HasPrefix(mode, "desktop")
			c.Gate = testGate{channels: cursor.Channels{Desktop: isDesktop, Webhook: !isDesktop}}
			f := facts(t, frame)
			r := consume(t, c, f)
			if r.Status != "unknown" || r.Reason != "delivery_uncertain" {
				t.Fatal("uncertain effect reported success", r)
			}
			t.Logf("first effects: desktop=%d webhook=%d", desktop, webhooks)
			if (isDesktop && (desktop != 1 || webhooks != 0)) || (!isDesktop && (webhooks != 1 || desktop != 0)) {
				t.Fatal("wrong first uncertain effect", desktop, webhooks)
			}
			before, original := cacheState(t, c.Cache.Root)
			bit := uint8(2)
			if isDesktop {
				bit = 1
			}
			if len(before.Entries) != 1 || before.Entries[0].Bits != bit || before.Entries[0].Until != 160 {
				t.Fatal("invalid retained uncertain claim", before)
			}
			key, err := hex.DecodeString(before.Entries[0].Key)
			if err != nil || len(key) != 32 {
				t.Fatal("uncertain marker is not a privacy hash")
			}
			t.Logf("first retained claim: key=%s bit=%d expiry=%g", before.Entries[0].Key, bit, before.Entries[0].Until)
			second := consume(t, c, f)
			t.Logf("second effects: desktop=%d webhook=%d receipt=%+v", desktop, webhooks, second)
			if (isDesktop && (desktop != 1 || webhooks != 0)) || (!isDesktop && (webhooks != 1 || desktop != 0)) {
				t.Fatal("retried or fallback effect", desktop, webhooks)
			}
			after, cache := cacheState(t, c.Cache.Root)
			if !reflect.DeepEqual(before, after) || !bytes.Equal(original, cache) {
				t.Fatal("uncertain claim history changed")
			}
			b, _ := json.Marshal(r)
			secondReceipt, _ := json.Marshal(second)
			if bytes.Contains(b, []byte("TEST-private")) || bytes.Contains(secondReceipt, []byte("TEST-private")) || bytes.Contains(original, []byte("TEST-private")) || bytes.Contains(cache, []byte("TEST-private")) {
				t.Fatal("private uncertainty escaped")
			}
			t.Log("retained key/bit/expiry/document unchanged; no private payload leakage")
			if second.Reason != "duplicate" || second.Status != "suppressed" {
				t.Fatal("uncertain effect replayed", second)
			}
		})
	}
}
