package mcp

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type backendFunc func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt

func (f backendFunc) Notify(c context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
	return f(c, p, o, d)
}

type statusFake struct{}

func (statusFake) Status(context.Context) (Status, error) {
	return Status{Configuration: "disabled", Capability: "unavailable"}, nil
}
func sdkFixture(t *testing.T, b Backend) (*sdk.ClientSession, context.Context) {
	t.Helper()
	return sdkFixtureKind(t, b, "codex")
}
func sdkFixtureKind(t *testing.T, b Backend, kind string) (*sdk.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	a, peer := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, a, Options{Backend: b, Status: statusFake{}, Clock: agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "test", NotAfter: 100} }), AdapterKind: kind})
	}()
	client := sdk.NewClient(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	session, e := client.Connect(ctx, &sdk.IOTransport{Reader: peer, Writer: peer}, nil)
	if e != nil {
		cancel()
		peer.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cancel()
		peer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Run failed to join")
		}
		session.Close()
	})
	return session, ctx
}
func TestSDKToolsAndPerCallOrigin(t *testing.T) {
	var mu sync.Mutex
	var seen []origin.Context
	literal := "--help $(x) [important] 🐈‍⬛"
	session, ctx := sdkFixture(t, backendFunc(func(_ context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, o)
		if p.Title != literal || p.Body != "line\n\ttext" || p.Navigation != notification.Required || d.NotAfter != 115 {
			t.Error("payload/deadline altered")
		}
		return agentnotify.Receipt{Status: "submitted"}
	}))
	listed, e := session.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(listed.Tools) != 2 {
		t.Fatal("unexpected tools")
	}
	for _, tool := range listed.Tools {
		if tool.Name == "notify" && (tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint) {
			t.Fatal("unsafe hints")
		}
	}
	for _, id := range []string{"A", "B", "A"} {
		r, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "notify", Meta: sdk.Meta{"threadId": id, "callId": "untrusted", "unknown": map[string]any{"x": true}}, Arguments: map[string]any{"title": literal, "body": "line\n\ttext", "category": "info"}})
		if e != nil || r.IsError {
			t.Fatalf("call: %v %v", r, e)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatal("missing calls")
	}
	for i, id := range []string{"A", "B", "A"} {
		if seen[i].SessionID != id || seen[i].CallID != "" || seen[i].Interface != origin.InterfaceUnknown || seen[i].Locality != origin.LocalityUnknown {
			t.Fatal("origin corrupted")
		}
	}
}
func TestSDKSchemaStatusAndResultMapping(t *testing.T) {
	calls := 0
	session, ctx := sdkFixture(t, backendFunc(func(_ context.Context, p agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		calls++
		return agentnotify.Receipt{Status: p.Body, RetrySafe: true}
	}))
	for _, status := range []string{"submitted", "suppressed", "rejected", "unknown"} {
		r, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "notify", Meta: sdk.Meta{"threadId": "A"}, Arguments: map[string]any{"title": "title", "body": status, "category": "info"}})
		if e != nil || r.IsError != (status == "rejected") {
			t.Fatalf("mapping %s: %v %v", status, r, e)
		}
		if status == "unknown" {
			var receipt agentnotify.Receipt
			if json.Unmarshal([]byte(r.Content[0].(*sdk.TextContent).Text), &receipt) != nil || receipt.RetrySafe {
				t.Fatal("unknown retry unsafe")
			}
		}
	}
	for _, args := range []map[string]any{{"title": "t", "body": "b", "category": "info", "thread_id": "spoof"}, {"title": "t", "body": "b", "category": "info"}} {
		r, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "notify", Arguments: args})
		if e != nil || !r.IsError {
			t.Fatal("invalid call accepted")
		}
	}
	r, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "notification_status", Arguments: map[string]any{}})
	if e != nil || r.IsError {
		t.Fatal("status failed")
	}
	if calls != 4 {
		t.Fatal("validation caused effects")
	}
	if _, e = session.CallTool(ctx, &sdk.CallToolParams{Name: "does_not_exist", Arguments: map[string]any{}}); e == nil {
		t.Fatal("unknown tool lacks protocol error")
	}
}

func TestSDKConcurrentSessions(t *testing.T) {
	seen := make(chan origin.Context, 2)
	b := backendFunc(func(_ context.Context, _ agentnotify.Payload, o origin.Context, _ notification.Deadline) agentnotify.Receipt {
		seen <- o
		return agentnotify.Receipt{Status: "submitted"}
	})
	a, ca := sdkFixture(t, b)
	bSession, cb := sdkFixture(t, b)
	var wg sync.WaitGroup
	for _, s := range []struct {
		session *sdk.ClientSession
		ctx     context.Context
		id      string
	}{{a, ca, "A"}, {bSession, cb, "B"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.session.CallTool(s.ctx, &sdk.CallToolParams{Name: "notify", Meta: sdk.Meta{"threadId": s.id}, Arguments: map[string]any{"title": "t", "body": "b", "category": "info"}})
			if e != nil || r.IsError {
				t.Error("session call failed")
			}
		}()
	}
	wg.Wait()
	first, second := <-seen, <-seen
	if first.SessionID == second.SessionID {
		t.Fatal("session context shared")
	}
}

func TestPayloadBudgetsAndProviderContext(t *testing.T) {
	for _, raw := range []string{`{"title":"t","body":"b","category":"info","navigation":null}`, `{"title":"t","body":"b","category":"info","request_id":null}`, `{"title":"t","body":"b","category":"info","url":"x"}`} {
		if _, ok := decodePayload([]byte(raw)); ok {
			t.Fatal("bad payload accepted")
		}
	}
	raw, _ := json.Marshal(map[string]string{"title": "t", "body": strings.Repeat("a", 16*1024), "category": "info"})
	if _, ok := decodePayload(raw); ok {
		t.Fatal("decoded budget accepted")
	}
	for _, kind := range []string{"codex", "claude"} {
		o, reason := requestOrigin(kind, sdk.Meta{"threadId": 17, "toolUseId": "tool", "callId": "call"})
		if reason == "available" || o.SessionID != "" || o.CallID != "" {
			t.Fatal("fabricated context")
		}
	}
}

func TestSDKArgumentAllowlistAndAnonymousStatus(t *testing.T) {
	var seen []origin.Context
	session, ctx := sdkFixture(t, backendFunc(func(_ context.Context, p agentnotify.Payload, o origin.Context, _ notification.Deadline) agentnotify.Receipt {
		seen = append(seen, o)
		if p.RequestID == nil || *p.RequestID != "explicit" {
			t.Error("explicit request identity lost")
		}
		return agentnotify.Receipt{Status: "submitted"}
	}))
	for _, key := range []string{"thread", "threadId", "url", "command", "app", "profile", "host"} {
		r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "notify", Meta: sdk.Meta{"threadId": "A"}, Arguments: map[string]any{"title": "t", "body": "b", "category": "info", key: "spoof"}})
		if err != nil || !r.IsError {
			t.Fatalf("accepted %s", key)
		}
	}
	r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "notify", Meta: sdk.Meta{"progressToken": "not-durable", "callId": "not-durable"}, Arguments: map[string]any{"title": "t", "body": "b", "category": "info", "navigation": "none", "request_id": "explicit"}})
	if err != nil || r.IsError || len(seen) != 1 || seen[0].SessionID != "" || seen[0].AnonymousCaller != "shared_mcp" || seen[0].CallID != "" {
		t.Fatal("anonymous context was fabricated or explicit ID lost")
	}
	for _, meta := range []sdk.Meta{nil, {"threadId": 1}} {
		r, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "notification_status", Meta: meta, Arguments: map[string]any{}})
		if err != nil || r.IsError {
			t.Fatal("status failed without valid context")
		}
		var status Status
		if json.Unmarshal([]byte(r.Content[0].(*sdk.TextContent).Text), &status) != nil || status.ContextReason == "available" || status.Configuration != "disabled" {
			t.Fatal("dishonest status context")
		}
	}
}

func TestSDKStatusIdentityScopes(t *testing.T) {
	for _, tc := range []struct {
		name, kind, scope, reason string
		meta                      sdk.Meta
	}{
		{"codex session", "codex", "session", "available", sdk.Meta{"threadId": "private-session"}},
		{"codex missing", "codex", "shared_anonymous", "session_required", nil},
		{"codex invalid", "codex", "shared_anonymous", "invalid_session", sdk.Meta{"threadId": 17}},
		{"claude actual", "claude", "shared_anonymous", "session_unavailable", sdk.Meta{"claudecode/toolUseId": "private-tool", "progressToken": "private-progress"}},
		{"claude ignores thread", "claude", "shared_anonymous", "session_unavailable", sdk.Meta{"threadId": "private-session"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, ctx := sdkFixtureKind(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
				t.Error("status invoked notify")
				return agentnotify.Receipt{}
			}), tc.kind)
			r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "notification_status", Meta: tc.meta, Arguments: map[string]any{}})
			if err != nil || r.IsError {
				t.Fatalf("status: %v %v", r, err)
			}
			raw := r.Content[0].(*sdk.TextContent).Text
			var status Status
			if err = json.Unmarshal([]byte(raw), &status); err != nil {
				t.Fatal(err)
			}
			if status.DedupScope != tc.scope || status.RateScope != tc.scope || status.ContextReason != tc.reason || strings.Contains(raw, "private-") {
				t.Fatalf("scope: %s", raw)
			}
		})
	}
	if got := requestScope(origin.Context{}); got != "unavailable" {
		t.Fatalf("invalid origin: %s", got)
	}
}
