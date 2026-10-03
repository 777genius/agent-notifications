package copilotvscodeevent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	local "github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	source "github.com/777genius/agent-notifications/internal/copilotvscodesource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/webhook"
)

const frame = `{"hook_event_name":"Stop","timestamp":"2026-10-02T06:45:01Z","stop_hook_active":false,"session_id":"TEST-private-session-秘密","cwd":"TEST-private-path","transcript_path":"TEST-private-transcript","prompt":"TEST-private-prompt\u001b]0;bad\u0007","tool_input":{"private":"TEST-private-tool"}}`

type testClock struct{ seconds atomic.Int64 }

func (c *testClock) Now() (string, float64, error) {
	return "TEST-boot", 100 + float64(c.seconds.Load()), nil
}

type testGate struct {
	channels local.Channels
	revoked  bool
}

func (g testGate) Channels(context.Context, local.Binding) local.Channels     { return g.channels }
func (g testGate) Recheck(context.Context, local.Binding, local.Channel) bool { return !g.revoked }

type desktopFunc func(context.Context, notification.Request) notification.Receipt

func (f desktopFunc) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return f(ctx, r)
}
func binding() local.Binding {
	return local.Binding{InstallationID: "TEST-private-install", BindingID: "TEST-private-binding", ProfileIdentity: "TEST-private-profile", Generation: 1, Product: "copilot-vscode"}
}
func cfg(t *testing.T, endpoint string) *config.Config {
	t.Helper()
	// Opposing global/Local/sibling settings prove actual consumer selection.
	data := `{"schemaVersion":2,"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}},"agents":{"gemini":{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}},"copilot-vscode":{"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true,"url":` + strconvJSON(endpoint) + `,"preset":"telegram","chat_id":"TEST-private-chat","headers":{"X-Private":"TEST-private-header"},"payloadFields":{"private":"TEST-private-extra"},"retry":{"enabled":true,"maxAttempts":7}}},"statuses":{"agent_stopping":{"title":"TEST-private-title","sound":"TEST-private-sound"}}}}}`
	d, err := config.ParseDocument([]byte(data), "/TEST/config.json", false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Effective(config.AssetContext{Agent: config.AgentCopilotVSCode, PluginRoot: "/TEST"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func strconvJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
func facts(t *testing.T, input string) source.Facts {
	t.Helper()
	f, err := source.Decode(context.Background(), source.Stop, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func cacheRoot(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func consume(t *testing.T, c local.Consumer, f source.Facts) local.Receipt {
	t.Helper()
	ctx, d, cancel, err := observation.Admission(context.Background(), c.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	return c.Consume(ctx, f, d)
}

// Test effect owners acquire exactly one lease, then recheck. Consumer must
// have released its cache lock first. Production policy composition is N2.
func leasedSender(t *testing.T, g testGate, b local.Binding, root string) local.WebhookSender {
	t.Helper()
	return func(ctx context.Context, c *config.Config, p webhook.SendContext) error {
		unlock, err := installruntime.LockExisting(ctx, filepath.Join(root, ".observations.lock"))
		if err != nil {
			return err
		}
		unlock()
		release, err := installruntime.Lock(ctx, filepath.Join(root, "TEST-effect.lock"))
		if err != nil {
			return err
		}
		defer release()
		if !g.Recheck(ctx, b, local.WebhookChannel) {
			return errors.New("revoked")
		}
		sender := webhook.NewWithContext(ctx, c)
		defer func() { _ = sender.Shutdown(50 * time.Millisecond) }()
		return sender.SendWithContext(p)
	}
}

// Red: private enrichment reaches real HTTP/cache/request; navigation defaults
// to focus; uncertain attempts are replayed; channel bits shift the shared window.
func TestLocalSourceConsumerHTTPPrivacyAndClaims(t *testing.T) {
	logRoot := t.TempDir()
	logger, err := logging.InitLogger(logRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()
	var requests atomic.Int32
	captured := make(chan []byte, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		captured <- b
		requests.Add(1)
		if r.Header.Get("X-Private") != "" {
			t.Error("private header escaped")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	clock := &testClock{}
	root := cacheRoot(t)
	g := testGate{channels: local.Channels{Desktop: true, Webhook: true}}
	c := local.Consumer{Binding: binding(), Gate: g, Config: cfg(t, server.URL), Clock: clock, Cache: &observation.RecentCache{Root: root, Clock: clock}}
	desktopCalls := 0
	c.Desktop = leasedDesktop(t, g, c.Binding, root, &desktopCalls)
	c.SendWebhook = leasedSender(t, g, c.Binding, root)
	f := facts(t, frame)
	if r := consume(t, c, f); r.Status != "submitted" {
		t.Fatal(r)
	}
	body := <-captured
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || payload["status"] != "agent_stopping" || payload["agent_source"] != "copilot-vscode" || payload["message"] != "Copilot in VS Code is stopping" {
		t.Fatalf("incorrect HTTP payload: %s", body)
	}
	if r := consume(t, c, f); r.Status != "suppressed" || desktopCalls != 1 || requests.Load() != 1 {
		t.Fatal("duplicate effect", r)
	}
	c.Binding.ProfileIdentity = "TEST-private-other-profile"
	if r := consume(t, c, f); r.Status != "submitted" {
		t.Fatal("profile was cross-suppressed", r)
	}
	<-captured
	checkSharedWindowAndUncertainty(t, c, clock, f, captured, body, root, g)
	diagnostics, err := os.ReadFile(filepath.Join(logRoot, "notification-debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(diagnostics, []byte("TEST-private")) {
		t.Fatal("private diagnostics escaped")
	}
}

func checkSharedWindowAndUncertainty(t *testing.T, c local.Consumer, clock *testClock, f source.Facts, captured <-chan []byte, body []byte, root string, g testGate) {
	t.Helper()
	// Both channels must share expiry; late bit addition cannot extend it.
	c.Binding = binding()
	f.Timestamp = "2026-10-02T06:45:02Z"
	c.Gate = testGate{channels: local.Channels{Desktop: true}}
	if r := consume(t, c, f); r.Desktop != "submitted" {
		t.Fatal(r)
	}
	before, err := os.ReadFile(filepath.Join(root, "observations.json"))
	if err != nil {
		t.Fatal(err)
	}
	clock.seconds.Store(30)
	c.Gate = g
	if r := consume(t, c, f); r.Desktop != "suppressed" || r.Webhook != "submitted" {
		t.Fatal(r)
	}
	<-captured
	after, err := os.ReadFile(filepath.Join(root, "observations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a, z struct {
		Entries []struct {
			Key   string
			Until float64
			Bits  uint8
		}
	}
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &z) != nil || len(a.Entries) != 3 || len(z.Entries) != 3 {
		t.Fatal("bad cache")
	}
	for i, e := range z.Entries {
		if e.Bits != 3 || e.Until != 160 || e.Until != a.Entries[i].Until {
			t.Fatal("shared window/bits changed")
		}
	}
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { panic("TEST-private-delivery") }
	f.Timestamp = "2026-10-02T06:45:03Z"
	r := consume(t, c, f)
	if r.Status != "unknown" || r.Desktop != "submitted" {
		t.Fatal("uncertainty did not dominate", r)
	}
	if r := consume(t, c, f); r.Status != "suppressed" {
		t.Fatal("uncertain claim replayed", r)
	}
	receipt, _ := json.Marshal(r)
	for _, b := range [][]byte{body, after, receipt} {
		if bytes.Contains(b, []byte("TEST-private")) {
			t.Fatal("private sentinel escaped")
		}
	}
}

// Red: malformed or unknown stop boolean becomes eligible, optional session is
// fabricated/cached, policy/config is treated as consent, or revoked is ignored.
func TestLocalEligibilityAtEffectBoundary(t *testing.T) {
	clock := &testClock{}
	calls := 0
	root := cacheRoot(t)
	c := local.Consumer{Binding: binding(), Gate: testGate{channels: local.Channels{Desktop: true}}, Config: cfg(t, "http://127.0.0.1:1"), Clock: clock, Cache: &observation.RecentCache{Root: root, Clock: clock}}
	c.Desktop = desktopFunc(func(context.Context, notification.Request) notification.Receipt {
		calls++
		return notification.Receipt{Status: "submitted"}
	})
	noSession := strings.Replace(frame, `,"session_id":"TEST-private-session-秘密"`, "", 1)
	for i := 0; i < 2; i++ {
		if r := consume(t, c, facts(t, noSession)); r.Status != "submitted" {
			t.Fatal("optional session denied", r)
		}
	}
	if calls != 2 {
		t.Fatal("optional session cross-cached")
	}
	calls = 0
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("absent session created cache")
	}
	for _, input := range []string{
		`{`, strings.Replace(frame, `"Stop"`, `"SubagentStop"`, 1), strings.Replace(frame, `"Stop"`, `"Notification"`, 1),
		strings.Replace(frame, `"stop_hook_active":false,`, "", 1), strings.Replace(frame, `false`, `null`, 1), strings.Replace(frame, `false`, `"false"`, 1), strings.Replace(frame, `false`, `true`, 1),
		strings.Replace(frame, `2026-10-02T06:45:01Z`, `bad`, 1), strings.Replace(frame, `TEST-private-session-秘密`, `bad\u0000`, 1), strings.Replace(frame, `TEST-private-session-秘密`, "bad\xff", 1), strings.Repeat("x", source.MaxPayloadBytes+1),
	} {
		f, err := source.Decode(context.Background(), source.Stop, []byte(input))
		if err == nil {
			consume(t, c, f)
		}
		if calls != 0 {
			t.Fatal("invalid observation reached effect")
		}
	}
	f := facts(t, noSession)
	denied := []local.Consumer{c, c, c, c, c, c, c, c, c, c}
	denied[0].Gate = nil
	denied[1].Binding.Product = "unknown"
	denied[2].Binding.ProfileIdentity = ""
	denied[3].Gate = testGate{}
	denied[4].Gate = testGate{channels: local.Channels{Desktop: true}, revoked: true}
	disabled := *c.Config
	disabled.Notifications.Desktop.Enabled = false
	denied[5].Config = &disabled
	denied[6].Binding.InstallationID = ""
	denied[7].Binding.BindingID = ""
	denied[8].Binding.Generation = 0
	denied[9].Config = nil
	for _, v := range denied {
		consume(t, v, f)
	}
	if calls != 0 {
		t.Fatal("denied effect")
	}
}

// Red: HTTP uses a fresh four-second lease or survives continuous-clock expiry.
func TestLocalStalledHTTPCanceledByRemainingBudget(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		close(entered)
		<-r.Context().Done()
		close(finished)
	}))
	defer server.Close()
	clock := &testClock{}
	root := cacheRoot(t)
	g := testGate{channels: local.Channels{Webhook: true}}
	c := local.Consumer{Binding: binding(), Gate: g, Config: cfg(t, server.URL), Clock: clock, Cache: &observation.RecentCache{Root: root, Clock: clock}}
	c.SendWebhook = leasedSender(t, g, c.Binding, root)
	ctx, d, cancel, err := observation.Admission(context.Background(), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	clock.seconds.Store(3)
	done := make(chan local.Receipt, 1)
	go func() { done <- c.Consume(ctx, facts(t, frame), d) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTP not entered")
	}
	start := time.Now()
	clock.seconds.Store(4)
	select {
	case r := <-done:
		if r.Status != "unknown" {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP outlived budget")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("HTTP connection survived")
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown slow")
	}
}

// Red: independent processes claim the same Local binding twice, or a profile
// gets suppressed by another profile. Uses real consumer and loopback HTTP.
func TestLocalConsumerMultiprocess(t *testing.T) {
	if os.Getenv("TEST_LOCAL_CHILD") == "1" {
		runLocalConsumerChild(t)
		return
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	root := cacheRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var children []*exec.Cmd
	var pipes []io.WriteCloser
	for i, profile := range []string{"TEST-profile-a", "TEST-profile-a", "TEST-profile-b", "TEST-profile-b", "TEST-profile-a", "TEST-profile-a"} {
		bindingID := "TEST-binding-a"
		if i >= 4 {
			bindingID = "TEST-binding-b"
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalConsumerMultiprocess$")
		cmd.Env = append(os.Environ(), "TEST_LOCAL_CHILD=1", "TEST_LOCAL_ROOT="+root, "TEST_LOCAL_HTTP="+server.URL, "TEST_LOCAL_PROFILE="+profile, "TEST_LOCAL_BINDING="+bindingID)
		p, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		children = append(children, cmd)
		pipes = append(pipes, p)
	}
	for _, p := range pipes {
		if _, err := io.WriteString(p, frame); err != nil {
			t.Error(err)
		}
		_ = p.Close()
	}
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Error(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("expected one attempt per binding/profile, got %d", calls.Load())
	}
}

func runLocalConsumerChild(t *testing.T) {
	t.Helper()
	clock := &testClock{}
	g := testGate{channels: local.Channels{Webhook: true}}
	root := os.Getenv("TEST_LOCAL_ROOT")
	c := local.Consumer{Binding: binding(), Gate: g, Config: cfg(t, os.Getenv("TEST_LOCAL_HTTP")), Clock: clock, Cache: &observation.RecentCache{Root: root, Clock: clock}}
	c.Binding.ProfileIdentity = os.Getenv("TEST_LOCAL_PROFILE")
	c.Binding.BindingID = os.Getenv("TEST_LOCAL_BINDING")
	c.SendWebhook = leasedSender(t, g, c.Binding, root)
	ctx, d, cancel, err := observation.Admission(context.Background(), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	data, err := io.ReadAll(io.LimitReader(os.Stdin, source.MaxPayloadBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	f, err := source.Decode(ctx, source.Stop, data)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Consume(ctx, f, d)
	if r.Status != "submitted" && r.Status != "suppressed" {
		t.Fatal(r)
	}
}

func leasedDesktop(t *testing.T, g testGate, b local.Binding, root string, calls *int) notification.DeliveryPort {
	t.Helper()
	return desktopFunc(func(ctx context.Context, r notification.Request) notification.Receipt {
		(*calls)++
		if r.Navigation != notification.None || !r.Silent || r.Target != (notification.DesktopTarget{}) || r.Content.Title != "Copilot in VS Code" || r.Content.Body != "Copilot in VS Code is stopping" || r.Content.Category != "info" {
			t.Error("unsafe desktop request")
		}
		release, err := installruntime.LockExisting(ctx, filepath.Join(root, ".observations.lock"))
		if err != nil {
			t.Error(err)
		} else {
			release()
		}
		leaseRelease, err := installruntime.Lock(ctx, filepath.Join(root, "TEST-effect.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer leaseRelease()
		if !g.Recheck(ctx, b, local.DesktopChannel) {
			return notification.Receipt{Status: "suppressed"}
		}
		return notification.Receipt{Status: "submitted", Reason: "TEST-private-backend"}
	})
}
