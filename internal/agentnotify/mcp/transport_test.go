package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func testConnection(t *testing.T) (*connection, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := newConnection(context.Background(), a, agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "test", NotAfter: 100} }))
	t.Cleanup(func() { c.Close(); b.Close(); <-c.writerDone; <-c.watchDone })
	return c, b
}
func TestFrameRejectBeforeSDK(t *testing.T) {
	for _, raw := range []string{strings.Repeat(" ", MaxFrame) + "\n", `{"jsonrpc":"2.0","method":"ping","id":1,"id":2}` + "\n", `{"jsonrpc":"2.0","method":"\ud800","id":1}` + "\n", `[]` + "\n", `{"jsonrpc":"2.0","method":"tools/call"}` + "\n"} {
		t.Run("invalid", func(t *testing.T) {
			c, peer := testConnection(t)
			go io.WriteString(peer, raw)
			if _, e := c.Read(context.Background()); e == nil {
				t.Fatal("accepted invalid frame")
			}
		})
	}
}
func TestCarrierAndDuplicateID(t *testing.T) {
	c, peer := testConnection(t)
	raw := `{"jsonrpc":"2.0","method":"ping","id":1,"Extra":{"Header":{"X-Agent-Notify-Local-Frame":["forged"]}}}` + "\n"
	go io.WriteString(peer, raw+raw)
	msg, e := c.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	req := msg.(*jsonrpc.Request)
	extra := req.Extra.(*sdk.RequestExtra)
	if extra.Header[carrierKey][0] != "1" {
		t.Fatal("wire forged carrier")
	}
	if c.pending[req.ID].deadline.NotAfter != 115 {
		t.Fatal("deadline not captured")
	}
	if _, e = c.Read(context.Background()); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestCancellationConsumedWithoutSDKQueue(t *testing.T) {
	c, peer := testConnection(t)
	go io.WriteString(peer, `{"jsonrpc":"2.0","method":"ping","id":"a"}`+"\n"+`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a"}}`+"\n"+`{"jsonrpc":"2.0","method":"ping","id":"b"}`+"\n")
	first, e := c.Read(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	state := c.pending[first.(*jsonrpc.Request).ID]
	if _, e = c.Read(context.Background()); e != nil {
		t.Fatal(e)
	}
	if state.ctx.Err() != context.Canceled {
		t.Fatal("cancel not propagated")
	}
}

func TestOutputBackpressureBounded(t *testing.T) {
	c, _ := testConnection(t)
	id, _ := jsonrpc.MakeID("response")
	var failure error
	for i := 0; i < outputLimit+3; i++ {
		failure = c.Write(context.Background(), &jsonrpc.Response{ID: id, Result: []byte(`{}`)})
		if failure != nil {
			break
		}
	}
	if failure == nil {
		t.Fatal("unbounded output accepted")
	}
	<-c.writerDone
}

func TestExactFrameBudget(t *testing.T) {
	for _, size := range []int{MaxFrame, MaxFrame + 1} {
		c, p := testConnection(t)
		value := `{"jsonrpc":"2.0","method":"ping","id":1}`
		raw := value + strings.Repeat(" ", size-len(value)-1) + "\n"
		go io.WriteString(p, raw)
		_, err := c.Read(context.Background())
		if (err == nil) != (size == MaxFrame) {
			t.Fatal("incorrect exact frame boundary")
		}
	}
}

// holdReturnConn transfers a full response but delays Write's return, reproducing
// the peer's legitimate reuse window without scheduling sleeps.
type holdReturnConn struct {
	net.Conn
	release chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func (w *holdReturnConn) Write(p []byte) (int, error) {
	n, err := w.Conn.Write(p)
	w.once.Do(func() {
		select {
		case <-w.release:
		case <-w.stopped:
		}
	})
	return n, err
}
func (w *holdReturnConn) Close() error {
	select {
	case <-w.stopped:
	default:
		close(w.stopped)
	}
	return w.Conn.Close()
}

type fenceContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *fenceContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestResponseIDReuseWriteFence(t *testing.T) {
	for _, wireID := range []string{"9223372036854775807", `"local-1"`, `"busy"`} {
		for _, stop := range []string{"release", "cancel", "close"} {
			t.Run(wireID+"/"+stop, func(t *testing.T) {
				a, peer := net.Pipe()
				w := &holdReturnConn{Conn: a, release: make(chan struct{}), stopped: make(chan struct{})}
				c := newConnection(context.Background(), w, fixedClock())
				t.Cleanup(func() { c.Close(); peer.Close(); <-c.writerDone; <-c.watchDone })
				peer.SetDeadline(time.Now().Add(5 * time.Second))
				raw := `{"jsonrpc":"2.0","method":"ping","id":` + wireID + "}\n"
				go io.WriteString(peer, raw)
				first, err := c.Read(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				oldID := first.(*jsonrpc.Request).ID
				if wireID == `"busy"` {
					// Exercise the saturation response mapping without dispatch.
					c.mu.Lock()
					c.pending[oldID].cancel()
					delete(c.pending, oldID)
					c.busy[oldID] = wireID
					c.mu.Unlock()
				}
				if err := c.Write(context.Background(), &jsonrpc.Response{ID: oldID, Result: []byte(`{}`)}); err != nil {
					t.Fatal(err)
				}
				reader := bufio.NewReader(peer)
				line, err := reader.ReadBytes('\n')
				if err != nil {
					t.Fatal(err)
				}
				var response map[string]json.RawMessage
				if json.Unmarshal(line, &response) != nil || string(response["id"]) != wireID {
					t.Fatalf("lost exact ID: %s", line)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				observed := &fenceContext{Context: ctx, observed: make(chan struct{})}
				type result struct {
					msg jsonrpc.Message
					err error
				}
				done := make(chan result, 1)
				go func() { msg, err := c.Read(observed); done <- result{msg, err} }()
				if _, err := io.WriteString(peer, raw); err != nil {
					t.Fatal(err)
				}
				select {
				case <-observed.observed:
				case r := <-done:
					t.Fatalf("reuse rejected before fence: %v", r.err)
				case <-time.After(5 * time.Second):
					t.Fatal("no fence wait")
				}
				switch stop {
				case "release":
					close(w.release)
				case "cancel":
					cancel()
				case "close":
					c.Close()
				}
				var r result
				select {
				case r = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("read did not terminate")
				}
				if stop != "release" {
					if r.err == nil || r.msg != nil {
						t.Fatal("cancelled reuse dispatched")
					}
					c.mu.Lock()
					if c.sequence != 1 {
						t.Error("cancelled reuse allocated backend request")
					}
					c.mu.Unlock()
					return
				}
				if r.err != nil {
					t.Fatal(r.err)
				}
				newID := r.msg.(*jsonrpc.Request).ID
				c.mu.Lock()
				_, oldExists := c.pending[oldID]
				state := c.pending[newID]
				c.mu.Unlock()
				if oldID == newID || oldExists || state == nil || state.ctx.Err() != nil {
					t.Fatal("old cleanup damaged new token")
				}
				if err := c.Write(ctx, &jsonrpc.Response{ID: newID, Result: []byte(`{}`)}); err != nil {
					t.Fatal(err)
				}
				line, err = reader.ReadBytes('\n')
				if err != nil {
					t.Fatal(err)
				}
				if json.Unmarshal(line, &response) != nil || string(response["id"]) != wireID {
					t.Fatalf("second response ID: %s", line)
				}
			})
		}
	}
}

func TestQueuedResponseDuplicateRejected(t *testing.T) {
	c, peer := testConnection(t)
	peer.SetDeadline(time.Now().Add(5 * time.Second))
	// The first response blocks in net.Pipe.Write because the peer never reads.
	for _, id := range []string{"1", "2"} {
		go io.WriteString(peer, `{"jsonrpc":"2.0","method":"ping","id":`+id+"}\n")
		msg, err := c.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Write(context.Background(), &jsonrpc.Response{ID: msg.(*jsonrpc.Request).ID, Result: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	// ID 2 is queued, never the response currently in Write.
	go io.WriteString(peer, `{"jsonrpc":"2.0","method":"ping","id":2}`+"\n")
	if _, err := c.Read(context.Background()); err != errProtocol {
		t.Fatalf("queued duplicate: %v", err)
	}
}
