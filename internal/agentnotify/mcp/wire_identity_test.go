package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

func reviewFixture(t *testing.T) *wireFixture {
	f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		t.Error("unexpected notify effect")
		return agentnotify.Receipt{Status: "submitted"}
	}), fixedClock(), nil)
	t.Cleanup(func() {
		f.cancel()
		select {
		case <-f.done:
		case <-time.After(time.Second):
			t.Error("Run did not drain")
		}
	})
	return f
}

func TestReviewStatusOmittedArguments(t *testing.T) {
	f := reviewFixture(t)
	f.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"notification_status","arguments":{}}}`)
	control := f.receive(t)
	t.Logf("explicit empty arguments: %s", control["result"])
	f.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"notification_status"}}`)
	got := f.receive(t)
	t.Logf("omitted optional arguments: %s", got["result"])
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(got["result"], &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("valid zero-argument status call rejected")
	}
}

func TestReviewNumericIDRoundTrip(t *testing.T) {
	f := reviewFixture(t)
	f.send(t, `{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`)
	got := f.receive(t)
	t.Logf("request id=9007199254740993, response id=%s", got["id"])
	if string(got["id"]) != "9007199254740993" {
		t.Error("response changed the integer request ID")
	}
}

func TestStatusSuppliedControls(t *testing.T) {
	f := reviewFixture(t)
	for i, args := range []string{"{}", "null", "[]", "true", "1", `"x"`, `{"x":1}`} {
		f.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"notification_status","arguments":%s}}`, 42+i, args))
		got := f.receive(t)
		var result struct{ IsError bool }
		if err := json.Unmarshal(got["result"], &result); err != nil {
			t.Fatal(err)
		}
		if result.IsError != (args != "{}") {
			t.Fatalf("arguments %s: %s", args, got["result"])
		}
	}
}

func TestExactWireIDsSDKReconnect(t *testing.T) {
	for session := 0; session < 2; session++ {
		f := reviewFixture(t)
		for _, id := range []string{"9007199254740993", "9007199254740992", "9223372036854775807", "-9223372036854775808", `"local-2"`, `""`} {
			f.send(t, `{"jsonrpc":"2.0","method":"ping","id":`+id+`}`)
			if got := f.receive(t); string(got["id"]) != id {
				t.Fatalf("%s -> %s", id, got["id"])
			}
		}
	}
}

func TestExactAdjacentCancellation(t *testing.T) {
	c, peer := testConnection(t)
	frames := `{"jsonrpc":"2.0","method":"ping","id":9007199254740992}
{"jsonrpc":"2.0","method":"ping","id":9007199254740993}
{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9007199254740993}}
{"jsonrpc":"2.0","method":"ping","id":"local-1"}
`
	go io.WriteString(peer, frames)
	a, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := c.pending[a.(*jsonrpc.Request).ID]
	second := c.pending[b.(*jsonrpc.Request).ID]
	if _, err = c.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.ctx.Err() != nil || second.ctx.Err() != context.Canceled {
		t.Fatal("inexact cancellation")
	}
}

func TestUnsupportedWireIDs(t *testing.T) {
	for _, id := range []string{"null", "9223372036854775808", "-9223372036854775809", "1.5", "1.0", "1e0", "1e999", "true", "[]", "{}"} {
		for _, cancel := range []bool{false, true} {
			c, peer := testConnection(t)
			frame := `{"jsonrpc":"2.0","method":"ping","id":` + id + `}` + "\n"
			if cancel {
				frame = `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":` + id + `}}` + "\n"
			}
			go io.WriteString(peer, frame)
			if _, err := c.Read(context.Background()); err == nil {
				t.Fatalf("accepted %s cancellation=%v", id, cancel)
			}
		}
	}
}

func TestMappingRetainedUntilOutputAndBoundedClose(t *testing.T) {
	c, peer := testConnection(t)
	for i := 0; i < pendingLimit; i++ {
		go io.WriteString(peer, fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"method\":\"ping\",\"id\":%d}\n", i))
		if _, err := c.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Writer remains blocked: busy responses must keep their identity reservations.
	done := make(chan error, 1)
	go func() { _, err := c.Read(context.Background()); done <- err }()
	for i := 0; i < outputLimit+3; i++ {
		_, err := io.WriteString(peer, fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"method\":\"ping\",\"id\":%d}\n", 100+i))
		if err != nil {
			break
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("overflow did not close")
	}
	<-c.writerDone
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != pendingLimit || len(c.busy) > outputLimit+1 || len(c.busy) == 0 {
		t.Fatal("mapping bounds/lifetime")
	}
	for _, s := range c.pending {
		if s.ctx.Err() == nil {
			t.Fatal("close did not cancel")
		}
	}
}

func TestNormalizedDuplicateWireIDs(t *testing.T) {
	for _, pair := range [][2]string{{"0", "-0"}, {`"a"`, `"\u0061"`}, {"9007199254740993", "9007199254740993"}} {
		c, peer := testConnection(t)
		go io.WriteString(peer, `{"jsonrpc":"2.0","method":"ping","id":`+pair[0]+"}\n"+`{"jsonrpc":"2.0","method":"ping","id":`+pair[1]+"}\n")
		if _, err := c.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Read(context.Background()); err == nil {
			t.Fatal("duplicate accepted")
		}
	}
}

func TestSDKAdjacentOutstandingWireIDs(t *testing.T) {
	f := reviewFixture(t)
	f.send(t, `{"jsonrpc":"2.0","method":"ping","id":9007199254740992}`)
	f.send(t, `{"jsonrpc":"2.0","method":"ping","id":9007199254740993}`)
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		seen[string(f.receive(t)["id"])] = true
	}
	if !seen["9007199254740992"] || !seen["9007199254740993"] {
		t.Fatal(seen)
	}
}

func TestMappingOutputLifecycle(t *testing.T) {
	c, peer := testConnection(t)
	go io.WriteString(peer, "{\"jsonrpc\":\"2.0\",\"method\":\"ping\",\"id\":9223372036854775807}\n")
	msg, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := msg.(*jsonrpc.Request).ID
	if err = c.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	retained := c.pending[id] != nil && c.pending[id].responding
	c.mu.Unlock()
	if !retained {
		t.Fatal("mapping released before write")
	}
	line, err := bufio.NewReader(peer).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	json.Unmarshal(line, &out)
	if string(out["id"]) != "9223372036854775807" {
		t.Fatal(string(line))
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		remaining := len(c.pending)
		c.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mapping not released after write")
		}
		runtime.Gosched()
	}
}

func TestSDKExactCancellation(t *testing.T) {
	started := make(chan string, 2)
	canceled := make(chan string, 2)
	f := rawFixture(t, backendFunc(func(ctx context.Context, p agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		started <- p.Title
		<-ctx.Done()
		canceled <- p.Title
		return agentnotify.Receipt{Status: "unknown"}
	}), fixedClock(), nil)
	defer func() {
		f.cancel()
		select {
		case <-f.done:
		case <-time.After(time.Second):
			t.Error("drain")
		}
	}()
	for _, id := range []string{"9007199254740992", "9007199254740993"} {
		f.send(t, `{"jsonrpc":"2.0","method":"tools/call","id":`+id+`,"params":{"name":"notify","arguments":{"title":"`+id+`","body":"b","category":"info"},"_meta":{"threadId":"A"}}}`)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("start")
		}
	}
	f.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9007199254740993}}`)
	select {
	case id := <-canceled:
		if id != "9007199254740993" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel")
	}
	if got := f.receive(t); string(got["id"]) != "9007199254740993" {
		t.Fatal(got)
	}
	select {
	case id := <-canceled:
		t.Fatal("sibling canceled", id)
	default:
	}
	f.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9007199254740992}}`)
	if got := f.receive(t); string(got["id"]) != "9007199254740992" {
		t.Fatal(got)
	}
}

func TestStatusInvalidJSONWire(t *testing.T) {
	for _, args := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{bad}`} {
		f := rawFixture(t, backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
			t.Error("unexpected effect")
			return agentnotify.Receipt{}
		}), fixedClock(), nil)
		f.send(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"notification_status","arguments":`+args+`}}`)
		select {
		case <-f.done:
		case <-time.After(time.Second):
			t.Fatal("invalid JSON accepted")
		}
	}
}
