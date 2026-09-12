package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

type countedIO struct {
	net.Conn
	closes atomic.Int32
}

func (c *countedIO) Close() error { c.closes.Add(1); return c.Conn.Close() }

type wireFixture struct {
	peer   net.Conn
	reader *bufio.Reader
	done   chan error
	cancel context.CancelFunc
	owned  *countedIO
}

func rawFixture(t *testing.T, b Backend, clock agentnotify.Clock, closeResources func() error) *wireFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a, peer := net.Pipe()
	owned := &countedIO{Conn: a}
	f := &wireFixture{peer: peer, reader: bufio.NewReader(peer), done: make(chan error, 1), cancel: cancel, owned: owned}
	go func() {
		f.done <- Run(ctx, owned, Options{Backend: b, Status: statusFake{}, Clock: clock, AdapterKind: "codex", CloseResources: closeResources})
	}()
	t.Cleanup(func() { cancel(); peer.Close() })
	f.send(t, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"raw","version":"1"}}}`)
	f.receive(t)
	f.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return f
}
func (f *wireFixture) send(t *testing.T, s string) {
	t.Helper()
	f.peer.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, e := io.WriteString(f.peer, s+"\n"); e != nil {
		t.Fatal(e)
	}
}
func (f *wireFixture) receive(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f.peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, e := f.reader.ReadBytes('\n')
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(line, &v) != nil {
		t.Fatal("stdout is not JSON-RPC")
	}
	return v
}
func callFrame(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"notify","_meta":{"threadId":"A"},"arguments":{"title":"t","body":"b","category":"info"}}}`, id)
}
func fixedClock() agentnotify.Clock {
	return agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "test", NotAfter: 100} })
}
func await(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}
}

func TestSDKSaturationCancellationFloodAndDrain(t *testing.T) {
	var calls, active, closed atomic.Int32
	started := make(chan struct{}, pendingLimit)
	canceled := make(chan struct{}, pendingLimit)
	release := make(chan struct{})
	backend := backendFunc(func(ctx context.Context, _ agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-release
		return agentnotify.Receipt{Status: "unknown"}
	})
	f := rawFixture(t, backend, fixedClock(), func() error {
		if active.Load() != 0 {
			t.Error("resources closed before handler drain")
		}
		closed.Add(1)
		return nil
	})
	baseline := runtime.NumGoroutine()
	for i := 1; i <= pendingLimit; i++ {
		f.send(t, callFrame(i))
	}
	for i := 0; i < pendingLimit; i++ {
		await(t, started)
	}
	f.send(t, callFrame(99))
	busy := f.receive(t)
	if string(busy["id"]) != "99" || len(busy["error"]) == 0 {
		t.Fatalf("not busy: %s", busy)
	}
	for i := 0; i < 1000; i++ {
		f.send(t, `{"jsonrpc":"2.0","method":"notifications/unknown","params":{"opaque":"data"}}`)
		f.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	}
	await(t, canceled)
	if runtime.NumGoroutine() > baseline+4*pendingLimit+10 {
		t.Fatal("unbounded goroutines")
	}
	if calls.Load() != pendingLimit {
		t.Fatal("overflow caused effect")
	}
	f.peer.Close()
	for i := 1; i < pendingLimit; i++ {
		await(t, canceled)
	}
	if closed.Load() != 0 {
		t.Fatal("early resource close")
	}
	close(release)
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown failed")
	}
	if closed.Load() != 1 || f.owned.closes.Load() != 1 {
		t.Fatal("resources not closed exactly once")
	}
}
func TestSDKOriginalDeadlineExpiredBeforeHandler(t *testing.T) {
	var now atomic.Int64
	now.Store(100)
	var calls atomic.Int32
	// Frame capture obtains 100, then injected continuous clock advances before
	// middleware starts. A handler-created fresh budget would wrongly call backend.
	clock := agentnotify.ClockFunc(func() notification.Deadline {
		return notification.Deadline{BootID: "test", NotAfter: float64(now.Swap(200))}
	})
	f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls.Add(1)
		return agentnotify.Receipt{Status: "submitted"}
	}), clock, nil)
	now.Store(100)
	f.send(t, callFrame(1))
	r := f.receive(t)
	var result struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(r["result"], &result)
	if !result.IsError || calls.Load() != 0 {
		t.Fatal("original deadline was reset")
	}
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown failed")
	}
}
func TestSDKIdlePartialAndOutputClose(t *testing.T) {
	for _, mode := range []string{"idle", "partial", "write"} {
		t.Run(mode, func(t *testing.T) {
			f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
				return agentnotify.Receipt{Status: "submitted"}
			}), fixedClock(), nil)
			if mode == "partial" {
				f.peer.SetWriteDeadline(time.Now().Add(time.Second))
				io.WriteString(f.peer, `{"jsonrpc":`)
			}
			if mode == "write" {
				f.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
			}
			f.cancel()
			select {
			case <-f.done:
			case <-time.After(3 * time.Second):
				t.Fatal("owned I/O not unblocked")
			}
			if f.owned.closes.Load() != 1 {
				t.Fatal("IO closed more than once")
			}
		})
	}
}

func TestSDKMalformedFramesHaveNoEffects(t *testing.T) {
	for _, frame := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notify","arguments":{"title":"t","title":"x"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notify","arguments":{"title":"\ud800"}}}`,
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"" + string([]byte{255}) + "\"}",
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"notify"}}`,
	} {
		t.Run("malformed", func(t *testing.T) {
			var calls atomic.Int32
			f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
				calls.Add(1)
				return agentnotify.Receipt{Status: "submitted"}
			}), fixedClock(), nil)
			f.send(t, frame)
			select {
			case <-f.done:
			case <-time.After(time.Second):
				t.Fatal("invalid frame not closed")
			}
			if calls.Load() != 0 {
				t.Fatal("malformed frame caused effect")
			}
		})
	}
}

func TestSDKUnknownMethodsAndSanitizedErrors(t *testing.T) {
	f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		return agentnotify.Receipt{Status: "submitted"}
	}), fixedClock(), nil)
	for _, method := range []string{"secret-body-method", "subscriptions/listen"} {
		f.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q}`, method))
		r := f.receive(t)
		var e struct {
			Code    int
			Message string
		}
		json.Unmarshal(r["error"], &e)
		if e.Code != -32601 || e.Message != "protocol_error" {
			t.Fatalf("unsanitized/nonstandard error: %s", r["error"])
		}
	}
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed")
	}
}

func TestSDKStalledOutputOriginalDeadline(t *testing.T) {
	var now atomic.Int64
	now.Store(100)
	clock := agentnotify.ClockFunc(func() notification.Deadline {
		return notification.Deadline{BootID: "test", NotAfter: float64(now.Load())}
	})
	entered := make(chan struct{})
	f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		close(entered)
		return agentnotify.Receipt{Status: "submitted"}
	}), clock, nil)
	f.send(t, callFrame(1))
	await(t, entered)
	now.Store(116)
	select {
	case <-f.done:
	case <-time.After(time.Second):
		t.Fatal("stalled writer ignored original deadline")
	}
}

func TestSDKSingleMessageVersionNegotiation(t *testing.T) {
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", "2026-07-28", "2099-01-01"} {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a, peer := net.Pipe()
			defer peer.Close()
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, a, Options{Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
					return agentnotify.Receipt{}
				}), Status: statusFake{}, Clock: fixedClock(), AdapterKind: "codex"})
			}()
			f := &wireFixture{peer: peer, reader: bufio.NewReader(peer)}
			f.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}`, version))
			response := f.receive(t)
			var result struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if err := json.Unmarshal(response["result"], &result); err != nil {
				t.Fatal(err)
			}
			want := version
			if version == "2025-03-26" || version >= "2026" {
				want = "2025-11-25"
			}
			if result.ProtocolVersion != want {
				t.Fatalf("got %s want %s", result.ProtocolVersion, want)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("shutdown failed")
			}
		})
	}
}

func TestSDKReconnectRPCIdentityIsNotDurable(t *testing.T) {
	var calls atomic.Int32
	b := backendFunc(func(_ context.Context, p agentnotify.Payload, o origin.Context, _ notification.Deadline) agentnotify.Receipt {
		if o.CallID != "" {
			t.Error("unqualified durable identity")
		}
		calls.Add(1)
		return agentnotify.Receipt{Status: "submitted"}
	})
	for i := 0; i < 2; i++ {
		f := rawFixture(t, b, fixedClock(), nil)
		f.send(t, callFrame(1))
		f.receive(t)
		f.cancel()
		select {
		case <-f.done:
		case <-time.After(time.Second):
			t.Fatal("shutdown failed")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("reused RPC id falsely replayed")
	}
}

func TestSDKSessionlessMetadataCannotBypassHandshake(t *testing.T) {
	var calls atomic.Int32
	f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls.Add(1)
		return agentnotify.Receipt{Status: "submitted"}
	}), fixedClock(), nil)
	f.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notify","_meta":{"threadId":"A","io.modelcontextprotocol/protocolVersion":"2026-07-28"},"arguments":{"title":"t","body":"b","category":"info"}}}`)
	r := f.receive(t)
	if len(r["error"]) == 0 || calls.Load() != 0 {
		t.Fatal("unqualified protocol reached backend")
	}
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed")
	}
}
