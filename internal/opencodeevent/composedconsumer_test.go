package opencodeevent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	"github.com/777genius/agent-notifications/internal/webhook"
)

type mutableSnapshot struct {
	mu     sync.Mutex
	sample ClockSnapshot
}

func (s *mutableSnapshot) SampleSnapshot() (ClockSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sample, nil
}
func (s *mutableSnapshot) advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sample.MonoLoNs += int64(d)
	s.sample.MonoHiNs += int64(d)
	s.sample.WallUnixNs += int64(d)
}

type desktopFunc func(context.Context, notification.Request) notification.Receipt

func (f desktopFunc) Deliver(c context.Context, r notification.Request) notification.Receipt {
	return f(c, r)
}
func ownedLiteral(t *testing.T, generation, kind string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(literalPrivate(t, generation, kind)))
}

func composedFixture(t *testing.T, url string) (context.Context, ComposedConsumer, *mutableSnapshot) {
	t.Helper()
	ctx, a, r, _ := admissionFixture(t)
	fields := r.Expected.Fields
	fields["route"] = json.RawMessage(`{"openCodeNotifications":{"desktop":true,"webhook":true}}`)

	notifications := map[string]any{"desktop": map[string]any{"enabled": true, "sound": true}, "webhook": map[string]any{"enabled": true, "url": url, "preset": "custom", "headers": map[string]string{"X-Secret": "CONFIG_PRIVATE_SENTINEL"}, "payloadFields": map[string]any{"secret": "CONFIG_PRIVATE_SENTINEL"}, "retry": map[string]any{"enabled": true, "maxAttempts": 3}}}
	fields["notifications"], _ = json.Marshal(notifications)
	fields["statuses"] = json.RawMessage(`{"task_complete":{"title":"CONFIG_PRIVATE_SENTINEL"},"opencode_error":{"title":"CONFIG_PRIVATE_SENTINEL"}}`)
	document, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.ControlRoot, "agent-notifications.json"), document, 0600); err != nil {
		t.Fatal(err)
	}
	s := independentSnapshot()
	s.MonoLoNs += 2000000000
	s.MonoHiNs += 2000000000
	s.WallUnixNs += 2000000000
	source := &mutableSnapshot{sample: s}
	return ctx, ComposedConsumer{ControlRoot: a.ControlRoot, Executable: r.Executable, GOOS: "linux", GOARCH: "amd64", Selection: independentSelection("v1"), Source: source,
		SendWebhook: func(ctx context.Context, cfg *config.Config, msg webhook.SendContext) error {
			return webhook.NewWithContext(ctx, cfg).SendWithContext(msg)
		}}, source
}
func privateState(t *testing.T, c ComposedConsumer) []byte {
	t.Helper()
	l, _, err := installruntime.ReadOwnership(c.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	r := l.Consumers["opencode-notifications"].OpenCode
	raw, err := os.ReadFile(filepath.Join(c.ControlRoot, "opencode-admission", r.Namespace, "claims.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestComposedOneDurableClaimBothChannelsAndPrivacy(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		header, _ := json.Marshal(r.Header)
		requests <- string(b) + string(header)
		w.WriteHeader(200)
	}))
	defer server.Close()
	ctx, c, _ := composedFixture(t, server.URL)
	var desktop []notification.Request
	c.Desktop = func(h *Handoff) notification.DeliveryPort {
		return desktopFunc(func(ctx context.Context, r notification.Request) notification.Receipt {
			if ctx != h.Context() || r.Deadline != h.Deadline() {
				t.Fatal("provider lost original handoff")
			}
			desktop = append(desktop, r)
			return notification.Receipt{Status: "submitted", Reason: "RAW_PROVIDER_SENTINEL"}
		})
	}
	result := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified"))
	if result.Status != "submitted" || result.Desktop != "submitted" || result.Webhook != "submitted" || len(desktop) != 1 {
		t.Fatalf("both channels: %+v", result)
	}
	r := desktop[0]
	if r.Content.Body != "Task completed" || r.Content.Title != "OpenCode" || r.Content.Subtitle != "" || r.Target != (notification.DesktopTarget{}) || !r.Silent || r.Navigation != notification.None || r.Policy.SoundEnabled {
		t.Fatal("privacy or silent navigation lost")
	}
	payload := <-requests
	receipt, _ := json.Marshal(result)
	for _, v := range []string{"private_sentinel", "req_native", "run_native", "msg_completed", "CONFIG_PRIVATE_SENTINEL", "RAW_PROVIDER_SENTINEL", strings.Repeat("11", 32)} {
		if strings.Contains(payload, v) || bytes.Contains(receipt, []byte(v)) {
			t.Fatal("private data escaped fixed receipt/payload")
		}
	}
	before := privateState(t, c)
	// New composition instance/reopened filesystem must not retry either channel.
	restarted := c
	dup := restarted.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified"))
	if dup.Reason != string(Duplicate) || len(desktop) != 1 || len(requests) != 0 || !bytes.Equal(before, privateState(t, c)) {
		t.Fatal("duplicate reopened a channel claim")
	}
}
func TestComposedInvalidPrivateFramesHaveZeroFilesystemOrProviderEffects(t *testing.T) {
	ctx, c, _ := composedFixture(t, "http://127.0.0.1:1")
	c.Desktop = func(*Handoff) notification.DeliveryPort { t.Fatal("invalid frame reached desktop"); return nil }
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
		t.Fatal("invalid frame reached HTTP")
		return nil
	}
	c.ReadPolicy = func(context.Context, string) (installruntime.PolicySnapshot, error) {
		t.Fatal("invalid frame reached snapshot/store boundary")
		return installruntime.PolicySnapshot{}, nil
	}
	before := privateState(t, c)
	for name, raw := range invalidPrivateFrames(t) {
		t.Run(name, func(t *testing.T) {
			result := c.Consume(ctx, time.Now(), io.NopCloser(bytes.NewReader(raw)))
			if result.Reason != "invalid_frame" {
				t.Fatal(result)
			}
			if !bytes.Equal(before, privateState(t, c)) {
				t.Fatal("invalid frame mutated durable state")
			}
		})
	}
}

func TestComposedUnknownHTTPRetainsBothChannelClaim(t *testing.T) {
	attempts := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts <- struct{}{}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()
	ctx, c, _ := composedFixture(t, server.URL)
	c.Desktop = func(*Handoff) notification.DeliveryPort {
		return desktopFunc(func(context.Context, notification.Request) notification.Receipt {
			return notification.Receipt{Status: "unknown"}
		})
	}
	got := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified"))
	if got.Status != "unknown" || got.Webhook != "unknown" || len(attempts) != 1 {
		t.Fatal("HTTP uncertainty retried or disappeared", got)
	}
	if next := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified")); next.Reason != string(Duplicate) || len(attempts) != 1 {
		t.Fatal("unknown claim replayed", next)
	}
}

func TestComposedActualIOHoldsLeaseThroughCancellationAndRevocation(t *testing.T) {
	ctx, c, _ := composedFixture(t, "http://127.0.0.1:1")
	entered, release := make(chan struct{}), make(chan struct{})
	c.Desktop = func(*Handoff) notification.DeliveryPort {
		return desktopFunc(func(context.Context, notification.Request) notification.Receipt {
			close(entered)
			<-release
			return notification.Receipt{Status: "unknown"}
		})
	}
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
		t.Error("cancelled second channel escaped")
		return errors.New("unreachable")
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, c.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	op, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan Receipt, 1)
	go func() { done <- c.Consume(op, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified")) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("desktop did not start")
	}
	cancel()
	short, stop := context.WithTimeout(ctx, 60*time.Millisecond)
	err = opencodeinstall.RevokeChannels(short, c.ControlRoot, s.Installation.Ledger.RuntimeRoot)
	stop()
	if err == nil {
		t.Fatal("cancel released lease before actual IO returned")
	}
	select {
	case <-done:
		t.Fatal("consumer returned before provider settled")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("provider completion did not join")
	}
	if err = opencodeinstall.RevokeChannels(ctx, c.ControlRoot, s.Installation.Ledger.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	if got := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "question_asked")); got.Reason != string(SnapshotChanged) {
		t.Fatal("revocation admitted new event", got)
	}
}

func TestComposedSuspendCancelsActiveHTTPAndSuppressesLaterChannel(t *testing.T) {
	// Suspend while desktop is active must synchronously fence HTTP after return.
	ctx, c, source := composedFixture(t, "http://127.0.0.1:1")
	c.Desktop = func(*Handoff) notification.DeliveryPort {
		return desktopFunc(func(ctx context.Context, r notification.Request) notification.Receipt {
			source.advance(25 * time.Second)
			return notification.Receipt{Status: "unknown"}
		})
	}
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error {
		t.Fatal("post-suspend HTTP escaped")
		return nil
	}
	if r := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified")); r.Webhook != "unavailable" {
		t.Fatal(r)
	}
	// A second fixture tests the watcher during actual HTTP with the Go timer live.
	entered, closed := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		close(entered)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	ctx, c, source = composedFixture(t, server.URL)
	c.Desktop = nil
	done := make(chan Receipt, 1)
	go func() { done <- c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified")) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("HTTP did not start")
	}
	source.advance(25 * time.Second)
	select {
	case result := <-done:
		if result.Webhook != "unknown" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("continuous suspend failed to abort HTTP")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("HTTP context not cancelled")
	}
}

func TestComposedSnapshotChangeDoesNotAdoptAmbientConfig(t *testing.T) {
	ctx, c, _ := composedFixture(t, "http://127.0.0.1:1")
	ambient := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(ambient, []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", ambient)
	c.ReadPolicy = func(ctx context.Context, root string) (installruntime.PolicySnapshot, error) {
		s, err := installruntime.ReadPolicySnapshot(ctx, root)
		if err == nil {
			err = os.WriteFile(filepath.Join(root, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":false}`), 0600)
		}
		return s, err
	}
	before := privateState(t, c)
	c.Desktop = func(*Handoff) notification.DeliveryPort { t.Fatal("changed snapshot reached provider"); return nil }
	got := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified"))
	if got.Reason != string(SnapshotChanged) || !bytes.Equal(before, privateState(t, c)) {
		t.Fatal("config re-read or snapshot adoption", got)
	}
}

func TestComposedOriginalBudgetBeforePolicyReadAndLockWait(t *testing.T) {
	for _, mode := range []string{"policyRead", "componentLock", "storeLock"} {
		t.Run(mode, func(t *testing.T) {
			ctx, c, source := composedFixture(t, "http://127.0.0.1:1")
			before := privateState(t, c)
			c.Desktop = func(*Handoff) notification.DeliveryPort { t.Fatal("expired wait reached provider"); return nil }
			started := time.Now().Add(-20*time.Second + 100*time.Millisecond)
			if mode == "policyRead" {
				c.ReadPolicy = func(ctx context.Context, root string) (installruntime.PolicySnapshot, error) {
					<-ctx.Done()
					return installruntime.PolicySnapshot{}, ctx.Err()
				}
			} else {
				lock := installruntime.OpenCodeStoreLock
				if mode == "componentLock" {
					lock = ".component-install.lock"
				}
				// E1's component lease uses its frozen permanent lock name.
				if mode == "componentLock" {
					lock = ".component-install.lock"
				}
				release, err := installruntime.LockExisting(ctx, filepath.Join(c.ControlRoot, lock))
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			got := c.Consume(ctx, started, ownedLiteral(t, "v1", "turn_idle_verified"))
			if got.Status == "submitted" || !bytes.Equal(before, privateState(t, c)) {
				t.Fatal("wait reset original deadline", got)
			}
			_ = source
		})
	}
}
func TestComposedRealCellCannotReachFilesystem(t *testing.T) {
	c := ComposedConsumer{Source: snapshotFunc(func() (ClockSnapshot, error) { return independentSnapshot(), nil }), ReadPolicy: func(context.Context, string) (installruntime.PolicySnapshot, error) {
		t.Fatal("unverified policy read")
		return installruntime.PolicySnapshot{}, nil
	}}
	if r := c.Consume(context.Background(), time.Now(), ownedLiteral(t, "v1", "turn_idle_verified")); r.Reason != string(TimeUnverified) {
		t.Fatal(r)
	}
}

func TestComposedIndependentLiteralAllKindsAdmitAndStayDeduplicated(t *testing.T) {
	for _, generation := range []string{"v1", "v2"} {
		for _, kind := range []string{"turn_idle_verified", "question_asked", "permission_asked", "terminal_error"} {
			t.Run(generation+kind, func(t *testing.T) {
				ctx, c, _ := composedFixture(t, "http://127.0.0.1:1")
				c.Selection = independentSelection(generation)
				requests := make(chan notification.Request, 2)
				c.Desktop = func(*Handoff) notification.DeliveryPort {
					return desktopFunc(func(_ context.Context, r notification.Request) notification.Receipt {
						requests <- r
						return notification.Receipt{Status: "submitted"}
					})
				}
				c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { return nil }
				got := c.Consume(ctx, time.Now(), ownedLiteral(t, generation, kind))
				if got.Desktop != "submitted" || got.Webhook != "submitted" {
					t.Fatal("qualified literal failed admission", got)
				}
				want := map[string]string{"turn_idle_verified": "Task completed", "question_asked": "OpenCode asked a question", "permission_asked": "OpenCode requested permission", "terminal_error": "An error needs your attention"}[kind]
				if (<-requests).Content.Body != want {
					t.Fatal("native kind changed fixed public copy")
				}
				if dup := c.Consume(ctx, time.Now(), ownedLiteral(t, generation, kind)); dup.Reason != string(Duplicate) || len(requests) != 0 {
					t.Fatal("typed native identity not durable", dup)
				}
			})
		}
	}
}

func TestComposedContinuousAbortDuringHeldStoreLock(t *testing.T) {
	ctx, c, source := composedFixture(t, "http://127.0.0.1:1")
	before := privateState(t, c)
	release, err := installruntime.LockExisting(ctx, filepath.Join(c.ControlRoot, installruntime.OpenCodeStoreLock))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	read := make(chan struct{})
	c.ReadPolicy = func(ctx context.Context, root string) (installruntime.PolicySnapshot, error) {
		s, err := installruntime.ReadPolicySnapshot(ctx, root)
		close(read)
		return s, err
	}
	c.Desktop = func(*Handoff) notification.DeliveryPort { t.Error("suspend during lock admitted"); return nil }
	done := make(chan Receipt, 1)
	go func() { done <- c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified")) }()
	select {
	case <-read:
	case <-ctx.Done():
		t.Fatal("config read did not complete")
	}
	source.advance(25 * time.Second)
	select {
	case got := <-done:
		if got.Status == "submitted" || !bytes.Equal(before, privateState(t, c)) {
			t.Fatal("lock wait ignored continuous abort", got)
		}
	case <-time.After(time.Second):
		t.Fatal("held store ignored native deadline")
	}
}

// Red if event delivery fails to resolve the allowed token, or an arbitrary
// injected environment value can escape into the webhook transport.
func TestComposedWebhookEnvRestrictedAtActualHTTPBoundary(t *testing.T) {
	for _, token := range []string{config.OpenCodeWebhookURLEnv, "MY_WEBHOOK_URL"} {
		t.Run(token, func(t *testing.T) {
			requests := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- struct{}{}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			ctx, c, _ := composedFixture(t, "${"+token+"}")
			c.Assets.LookupEnv = func(key string) (string, bool) {
				if key != config.OpenCodeWebhookURLEnv {
					t.Error("unsupported environment lookup reached ambient authority")
				}
				return server.URL, true
			}
			c.Desktop = func(*Handoff) notification.DeliveryPort {
				return desktopFunc(func(context.Context, notification.Request) notification.Receipt {
					return notification.Receipt{Status: "submitted"}
				})
			}
			result := c.Consume(ctx, time.Now(), ownedLiteral(t, "v1", "turn_idle_verified"))
			if token == config.OpenCodeWebhookURLEnv {
				if result.Desktop != "submitted" || result.Webhook != "submitted" || len(requests) != 1 {
					t.Fatalf("allowed URL did not reach both channels: %+v", result)
				}
			} else if result.Reason != "invalid_config" || len(requests) != 0 {
				t.Fatalf("unsupported URL reached delivery: %+v", result)
			}
		})
	}
}

// Optional desktop context crosses the production private decoder without
// altering the original claim identity/time, channel consent or webhook copy.
func TestComposedOptionalDisplayIsDesktopOnlyAndCannotChangeAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, display string
		context       bool
	}{
		{"native", `{"sessionID":"ses_private_sentinel","requestID":"req_native_4","sessionTitle":"Native title","question":"Continue?"}`, true},
		{"other session", `{"sessionID":"other","requestID":"req_native_4","sessionTitle":"Native title","question":"Continue?"}`, false},
		{"other request", `{"sessionID":"ses_private_sentinel","requestID":"other","question":"Continue?"}`, false},
		{"unexpected field", `{"sessionID":"ses_private_sentinel","requestID":"req_native_4","question":"Continue?","prompt":"PRIVATE"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c, _ := composedFixture(t, "http://127.0.0.1:1")
			var desktop []notification.Request
			c.Desktop = func(*Handoff) notification.DeliveryPort {
				return desktopFunc(func(_ context.Context, req notification.Request) notification.Receipt {
					desktop = append(desktop, req)
					return notification.Receipt{Status: "submitted"}
				})
			}
			webhooks := 0
			c.SendWebhook = func(ctx context.Context, cfg *config.Config, msg webhook.SendContext) error {
				webhooks++
				if msg.Message != "OpenCode asked a question" || msg.RawBody != msg.Message || cfg.Statuses[string(msg.Status)].Title != "OpenCode" {
					t.Fatal("desktop context changed generic webhook")
				}
				encoded, _ := json.Marshal(msg)
				if bytes.Contains(encoded, []byte("Native title")) || bytes.Contains(encoded, []byte("Continue?")) {
					t.Fatal("desktop context leaked")
				}
				return nil
			}
			original := literalPrivate(t, "v1", "question_asked")
			var frame map[string]json.RawMessage
			if err := json.Unmarshal(original, &frame); err != nil {
				t.Fatal(err)
			}
			frame["display"] = json.RawMessage(tc.display)
			raw, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			before, err := DecodePrivate(original, c.Selection)
			if err != nil {
				t.Fatal(err)
			}
			after, err := DecodePrivate(raw, c.Selection)
			if err != nil || before.Fact != after.Fact || before.Provenance != after.Provenance || before.Origin != after.Origin {
				t.Fatal("display changed native authority")
			}
			result := c.Consume(ctx, time.Now(), io.NopCloser(bytes.NewReader(raw)))
			if result.Status != "submitted" || len(desktop) != 1 || webhooks != 1 {
				t.Fatalf("delivery: %+v", result)
			}
			req := desktop[0]
			if tc.context {
				if req.Content.Title != "❓ Continue?" || req.Content.Subtitle != "Native title" || req.Content.Body != "Continue?" {
					t.Fatal("desktop context absent")
				}
			} else if req.Content.Title != "OpenCode" || req.Content.Subtitle != "" || req.Content.Body != "OpenCode asked a question" {
				t.Fatal("invalid optional association escaped")
			}
			if !req.Silent || req.Navigation != notification.None || req.Policy.SoundEnabled {
				t.Fatal("display changed delivery policy")
			}
			receipt, _ := json.Marshal(result)
			if bytes.Contains(receipt, []byte("Native title")) || bytes.Contains(receipt, []byte("Continue?")) {
				t.Fatal("private receipt")
			}
		})
	}
}
