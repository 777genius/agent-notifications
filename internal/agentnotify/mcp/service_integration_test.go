//go:build linux || darwin

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type servicePorts struct {
	mu    sync.Mutex
	calls []notification.Request
}

func (*servicePorts) CheckReadiness(_ context.Context, r notification.Request) notification.Readiness {
	return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "ready", Navigation: notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}}
}
func (p *servicePorts) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, r)
	out := notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "fixture", Navigation: notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}}
	switch r.Target.ThreadID {
	case "C":
		out.Status = "suppressed"
		out.Reason = "disabled"
		out.Navigation = notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "disabled"}
	case "D":
		out.Status = "unknown"
		out.Reason = "handoff_unconfirmed"
	}
	return out
}

// This exercises the actual SDK -> service -> durable journal boundary, while
// the OS effect remains an injected port. It is not installed Desktop E2E.
func TestSDKServiceDurableReplayAndScopedOutcomes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	setupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := journal.Initialize(setupContext, journal.Options{Root: root, Clock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "test", Seconds: 100, Available: true} })})
	if err != nil {
		t.Fatal(err)
	}
	ports := &servicePorts{}
	service, err := agentnotify.New(agentnotify.Dependencies{Admission: store, Clock: agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "test", NotAfter: 100} }), Policy: agentnotify.PolicyFunc(func(context.Context, origin.Context) (agentnotify.Policy, error) {
		return agentnotify.Policy{Rates: journal.RatePolicy{SessionPerMinute: 6, RuntimePerMinute: 30, Burst: 6}, Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true}, Route: origin.RoutePolicy{LocalRouting: true, AllowUnknownCaller: true, ApplicationPath: "/fixture/Codex.app", TeamID: "fixture"}}, nil
	}), Target: agentnotify.TargetFunc(func(_ context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
		return origin.ResolveCodex(o, p), nil
	}), Readiness: ports, Delivery: ports})
	if err != nil {
		t.Fatal(err)
	}
	session, ctx := sdkFixture(t, service)
	call := func(s *sdk.ClientSession, c context.Context, thread, body string) agentnotify.Receipt {
		t.Helper()
		result, e := s.CallTool(c, &sdk.CallToolParams{Name: "notify", Meta: sdk.Meta{"threadId": thread}, Arguments: map[string]any{"title": "--help [important]", "body": body, "category": "attention", "request_id": "same-key"}})
		if e != nil {
			t.Fatal(e)
		}
		var receipt agentnotify.Receipt
		if len(result.Content) != 1 {
			t.Fatalf("unexpected content: %+v", result)
		}
		text, ok := result.Content[0].(*sdk.TextContent)
		if !ok {
			t.Fatal("missing text receipt")
		}
		if e = json.Unmarshal([]byte(text.Text), &receipt); e != nil {
			t.Fatal(e)
		}
		if result.IsError != (receipt.Status == "rejected") {
			t.Fatalf("wrong error mapping: %+v", receipt)
		}
		return receipt
	}
	for _, thread := range []string{"A", "B", "C", "D"} {
		want := "submitted"
		if thread == "C" {
			want = "suppressed"
		}
		if thread == "D" {
			want = "unknown"
		}
		first := call(session, ctx, thread, "literal body")
		if first.Status != want || first.Replayed || first.RetrySafe {
			t.Fatalf("first %s: %+v", thread, first)
		}
		replay := call(session, ctx, thread, "literal body")
		if replay.Status != want || !replay.Replayed || replay.TrackingID != first.TrackingID || replay.Navigation != first.Navigation || replay.Reason != first.Reason {
			t.Fatalf("replay %s: %+v / %+v", thread, first, replay)
		}
		if thread == "C" && replay.Navigation.Capability != "disabled" {
			t.Fatal("suppressed replay upgraded navigation")
		}
	}
	conflict := call(session, ctx, "A", "changed body")
	if conflict.Status != "rejected" || conflict.Reason != "idempotency_conflict" {
		t.Fatalf("conflict: %+v", conflict)
	}
	second, c2 := sdkFixture(t, service)
	if replay := call(second, c2, "D", "literal body"); !replay.Replayed || replay.Status != "unknown" {
		t.Fatalf("reconnect: %+v", replay)
	}
	ports.mu.Lock()
	defer ports.mu.Unlock()
	if len(ports.calls) != 4 {
		t.Fatalf("duplicate delivery calls: %d", len(ports.calls))
	}
	for i, thread := range []string{"A", "B", "C", "D"} {
		if ports.calls[i].Target.ThreadID != thread || ports.calls[i].Content.Title != "--help [important]" {
			t.Fatal("scope/literal changed")
		}
	}
}

// nonePorts is a counted native boundary, with no fabricated desktop target.
type nonePorts struct {
	mu    sync.Mutex
	calls int
}

func (*nonePorts) CheckReadiness(_ context.Context, r notification.Request) notification.Readiness {
	return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "ready", Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "navigation_none"}}
}
func (p *nonePorts) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "fixture", Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "navigation_none"}}
}

func TestSDKClaudeSharedAnonymousDurableScope(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ports := &nonePorts{}
	// Reopen the durable journal and reconstruct the production service on
	// reconnect, so process-local counters cannot make this assertion pass.
	options := journal.Options{Root: root, Clock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "test", Seconds: 100, Available: true} })}
	if _, err = journal.Initialize(ctx, options); err != nil {
		t.Fatal(err)
	}
	newService := func() *agentnotify.Service {
		store, e := journal.Open(ctx, options)
		if e != nil {
			t.Fatal(e)
		}
		service, e := agentnotify.New(agentnotify.Dependencies{Admission: store, Clock: agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "test", NotAfter: 100} }), Policy: agentnotify.PolicyFunc(func(_ context.Context, o origin.Context) (agentnotify.Policy, error) {
			if o.Interface != origin.InterfaceUnknown || o.Locality != origin.LocalityUnknown || o.CallID != "" {
				t.Error("fabricated origin")
			}
			return agentnotify.Policy{Rates: journal.RatePolicy{SessionPerMinute: 2, RuntimePerMinute: 30, Burst: 30}, Delivery: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}, Route: origin.RoutePolicy{AllowUnknownCaller: true}}, nil
		}), Target: agentnotify.TargetFunc(func(context.Context, origin.Context, origin.RoutePolicy) (origin.Target, error) {
			t.Error("none resolved a target")
			return origin.Target{}, nil
		}), Readiness: ports, Delivery: ports})
		if e != nil {
			t.Fatal(e)
		}
		return service
	}
	service := newService()
	a, ca := sdkFixtureKind(t, service, "claude")
	b, cb := sdkFixtureKind(t, service, "claude")
	call := func(s *sdk.ClientSession, c context.Context, id, body string, meta sdk.Meta) agentnotify.Receipt {
		t.Helper()
		r, e := s.CallTool(c, &sdk.CallToolParams{Name: "notify", Meta: meta, Arguments: map[string]any{"title": "event", "body": body, "category": "info", "navigation": "none", "request_id": id}})
		if e != nil {
			t.Fatal(e)
		}
		var receipt agentnotify.Receipt
		if e = json.Unmarshal([]byte(r.Content[0].(*sdk.TextContent).Text), &receipt); e != nil {
			t.Fatal(e)
		}
		if r.IsError != (receipt.Status == "rejected") {
			t.Fatalf("mapping: %+v", receipt)
		}
		return receipt
	}
	metaA := sdk.Meta{"claudecode/toolUseId": "toolu_A", "progressToken": "progress_A"}
	metaB := sdk.Meta{"claudecode/toolUseId": "toolu_B", "progressToken": "progress_B"}
	idA, idB, idC := "c3cd01ce-bf32-4c58-9c8f-df295020af9a", "e26d5d31-dbdd-4777-ab37-69b75ca36cf5", "733456b9-7870-4d58-817e-6d82c0fbb7da"
	first := call(a, ca, idA, "same payload", metaA)
	second := call(b, cb, idB, "same payload", metaB)
	if first.Status != "submitted" || second.Status != "submitted" || first.Replayed || second.Replayed || first.TrackingID == "" || first.TrackingID == second.TrackingID {
		t.Fatalf("independent: %+v %+v", first, second)
	}
	replay := call(b, cb, idA, "same payload", metaB)
	if !replay.Replayed || replay.Status != "submitted" || replay.TrackingID != first.TrackingID {
		t.Fatalf("replay: %+v", replay)
	}
	if r := call(b, cb, idA, "different payload", metaB); r.Reason != "idempotency_conflict" || r.Status != "rejected" {
		t.Fatalf("conflict: %+v", r)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	reconnected, cr := sdkFixtureKind(t, newService(), "claude")
	// Status must neither mutate durable state nor disclose tool/session IDs.
	snapshot := func() map[string]string {
		files := map[string]string{}
		e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() {
				raw, e := os.ReadFile(path)
				if e != nil {
					return e
				}
				files[path] = string(raw)
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		return files
	}
	before := snapshot()
	result, e := reconnected.CallTool(cr, &sdk.CallToolParams{Name: "notification_status", Meta: metaB, Arguments: map[string]any{}})
	if e != nil {
		t.Fatal(e)
	}
	var status Status
	if e = json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &status); e != nil {
		t.Fatal(e)
	}
	if result.IsError || status.DedupScope != "shared_anonymous" || status.RateScope != "shared_anonymous" || status.ContextReason != "session_unavailable" {
		t.Fatalf("reconnect status: %+v", status)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("status mutated journal")
	}
	if r := call(reconnected, cr, idA, "same payload", metaB); !r.Replayed || r.TrackingID != first.TrackingID {
		t.Fatalf("durable replay: %+v", r)
	}
	if r := call(reconnected, cr, idC, "new event", metaB); r.Status != "suppressed" || r.Reason != "rate_limited" {
		t.Fatalf("shared anonymous quota: %+v", r)
	}
	// Same runtime, fresh typed session: submission proves the runtime/burst
	// caps are not the cause of the anonymous rejection above.
	codex, cc := sdkFixtureKind(t, newService(), "codex")
	if r := call(codex, cc, idC, "new event", sdk.Meta{"threadId": "shared_mcp"}); r.Status != "submitted" {
		t.Fatalf("runtime quota: %+v", r)
	}
	ports.mu.Lock()
	defer ports.mu.Unlock()
	if ports.calls != 3 {
		t.Fatalf("native effects: %d", ports.calls)
	}
}
