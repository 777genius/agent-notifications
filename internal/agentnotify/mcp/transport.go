// Package mcp provides an independently composed, bounded SDK adapter.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const MaxFrame = 64 * 1024
const pendingLimit = 8
const outputLimit = 16
const carrierKey = "X-Agent-Notify-Local-Frame"

var errProtocol = errors.New("invalid_frame")
var errBackpressure = errors.New("output_backpressure")

type frameState struct {
	ctx        context.Context
	cancel     context.CancelFunc
	deadline   notification.Deadline
	token      string
	responding bool
	wireID     string
}

type outbound struct {
	bytes []byte
	id    jsonrpc.ID
}

// connection admits requests before SDK allocation/dispatch. Only one initialized
// notification reaches the SDK. Cancellation is handled here synchronously: SDK
// v1.7's preempter otherwise launches a goroutine and still queues each notice.
type connection struct {
	io          io.ReadWriteCloser
	reader      *bufio.Reader
	clock       agentnotify.Clock
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	pending     map[jsonrpc.ID]*frameState
	busy        map[jsonrpc.ID]string
	sequence    uint64
	initialized bool
	closed      bool
	once        sync.Once
	output      chan outbound
	writerDone  chan struct{}
	writingID   jsonrpc.ID
	writeFence  chan struct{}
	watchDone   chan struct{}
}

func newConnection(ctx context.Context, owned io.ReadWriteCloser, clock agentnotify.Clock) *connection {
	ctx, cancel := context.WithCancel(ctx)
	c := &connection{io: owned, reader: bufio.NewReaderSize(owned, MaxFrame+1), clock: clock, ctx: ctx, cancel: cancel, pending: make(map[jsonrpc.ID]*frameState), busy: make(map[jsonrpc.ID]string), output: make(chan outbound, outputLimit), writerDone: make(chan struct{}), watchDone: make(chan struct{})}
	go c.writeLoop()
	go c.watchDeadlines()
	go func() { <-ctx.Done(); c.Close() }()
	return c
}
func (c *connection) Connect(context.Context) (sdk.Connection, error) { return c, nil }
func (c *connection) SupportsProtocolVersion(v string) bool {
	// March 2025 required batch support, which this bounded single-frame
	// transport deliberately does not implement. Do not advertise future versions.
	return v == "2024-11-05" || v == "2025-06-18" || v == "2025-11-25"
}
func (c *connection) SessionID() string { return "" }
func (c *connection) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		for _, s := range c.pending {
			s.cancel()
		}
		c.mu.Unlock()
		_ = c.io.Close()
	})
	return nil
}
func (c *connection) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		// ReadSlice uses a fixed buffer; oversized whitespace never reaches a JSON decoder.
		raw, err := c.reader.ReadSlice('\n')
		if err != nil {
			c.Close()
			if err == io.EOF && len(raw) == 0 {
				return nil, io.EOF
			}
			return nil, errProtocol
		}
		deadline := c.clock.Now()
		deadline.NotAfter += 15
		if len(raw) > MaxFrame || strictjson.Validate(raw, strictjson.Budget{Bytes: MaxFrame, Depth: 16, Entries: 10000}) != nil {
			c.Close()
			return nil, errProtocol
		}
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			c.Close()
			return nil, errProtocol
		}

		var envelope map[string]json.RawMessage
		if json.Unmarshal(raw, &envelope) != nil {
			c.Close()
			return nil, errProtocol
		}
		wireID, err := exactID(envelope["id"])
		if err != nil {
			c.Close()
			return nil, errProtocol
		}
		// Hide every caller ID from the SDK float decoder and private namespace.
		if wireID != "" {
			envelope["id"] = json.RawMessage(`"local"`)
			raw, _ = json.Marshal(envelope)
		}
		msg, err := jsonrpc.DecodeMessage(raw)
		if err != nil {
			c.Close()
			return nil, errProtocol
		}
		req, ok := msg.(*jsonrpc.Request)
		if !ok { // This tools-only server never initiates client requests.
			c.Close()
			return nil, errProtocol
		}
		if !req.ID.IsValid() {
			switch req.Method {
			case "notifications/cancelled":

				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(req.Params, &p) != nil {
					c.Close()
					return nil, errProtocol
				}
				id, e := exactID(p.RequestID)
				if e != nil || id == "" {
					c.Close()
					return nil, errProtocol
				}
				c.mu.Lock()
				for _, state := range c.pending {
					if state.wireID == id {
						state.cancel()
					}
				}
				c.mu.Unlock()
				continue
			case "notifications/initialized":
				c.mu.Lock()
				first := !c.initialized
				c.initialized = true
				c.mu.Unlock()
				if first {
					return req, nil
				}
				continue
			case "tools/call":
				c.Close()
				return nil, errProtocol
			default:
				continue // Unknown notifications have no reply or queued work.
			}
		}
	admit:
		c.mu.Lock()
		if c.closed || ctx.Err() != nil {
			c.mu.Unlock()
			return nil, io.EOF
		}
		duplicate := false
		var fence <-chan struct{}
		for id, state := range c.pending {
			duplicate = duplicate || state.wireID == wireID
			if state.wireID == wireID && id == c.writingID {
				fence = c.writeFence
			}
		}
		for id, wire := range c.busy {
			duplicate = duplicate || wire == wireID
			if wire == wireID && id == c.writingID {
				fence = c.writeFence
			}
		}
		// A peer can consume the complete newline before Write returns. Only
		// the response currently in Write may defer duplicate admission.
		if fence != nil {
			c.mu.Unlock()
			select {
			case <-fence:
				goto admit
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.ctx.Done():
				return nil, io.EOF
			}
		}
		if duplicate {
			c.mu.Unlock()
			c.Close()
			return nil, errProtocol
		}
		// Never reuse a private ID on counter exhaustion.
		if c.sequence == ^uint64(0) {
			c.mu.Unlock()
			c.Close()
			return nil, errProtocol
		}
		c.sequence++
		token := strconv.FormatUint(c.sequence, 10)
		req.ID, _ = jsonrpc.MakeID("local-" + token)
		if len(c.pending) >= pendingLimit {
			if len(c.busy) >= outputLimit+1 {
				c.mu.Unlock()
				c.Close()
				return nil, errBackpressure
			}
			c.busy[req.ID] = wireID
			c.mu.Unlock()
			if e := c.Write(ctx, &jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{Code: -32000, Message: "busy"}}); e != nil {
				return nil, e
			}
			continue
		}
		callCtx, cancel := context.WithCancel(c.ctx)
		c.pending[req.ID] = &frameState{ctx: callCtx, cancel: cancel, deadline: deadline, token: token, wireID: wireID}
		c.mu.Unlock()
		switch req.Method {
		case "initialize", "ping", "tools/list", "tools/call":
		default:
			if e := c.Write(ctx, &jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not supported"}}); e != nil {
				return nil, e
			}
			continue
		}
		// The SDK's new sessionless protocol bypasses legacy initialization
		// before middleware runs. This adapter has qualified only the legacy
		// single-message contract, so do not let caller metadata switch protocols.
		var params struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		}
		if json.Unmarshal(req.Params, &params) == nil {
			if _, exists := params.Meta["io.modelcontextprotocol/protocolVersion"]; exists {
				if e := c.Write(ctx, &jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "unsupported protocol"}}); e != nil {
					return nil, e
				}
				continue
			}
		}
		// Extra is constructed locally. JSON-RPC decoding never populates this field.
		req.Extra = &sdk.RequestExtra{Header: http.Header{carrierKey: []string{token}}}
		return req, nil
	}
}
func (c *connection) Write(ctx context.Context, msg jsonrpc.Message) error {
	// SDK diagnostics can include method names and invalid caller values.
	if r, ok := msg.(*jsonrpc.Response); ok && r.Error != nil {
		code := int64(jsonrpc.CodeInternalError)
		var wire *jsonrpc.Error
		if errors.As(r.Error, &wire) {
			code = wire.Code
		}
		message := "protocol_error"
		if code == -32000 {
			message = "busy"
		}
		msg = &jsonrpc.Response{ID: r.ID, Error: &jsonrpc.Error{Code: code, Message: message}}
	}
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil || len(data)+1 > MaxFrame {
		c.Close()
		return errProtocol
	}
	item := outbound{}
	if r, ok := msg.(*jsonrpc.Response); ok {
		item.id = r.ID
		c.mu.Lock()
		wireID := c.busy[r.ID]
		if state := c.pending[r.ID]; state != nil {
			state.responding = true
			wireID = state.wireID
		}
		if wireID != "" {
			var envelope map[string]json.RawMessage
			_ = json.Unmarshal(data, &envelope)
			envelope["id"] = json.RawMessage(wireID)
			data, err = json.Marshal(envelope)
		}
		c.mu.Unlock()
	}
	if err != nil || len(data)+1 > MaxFrame {
		c.Close()
		return errProtocol
	}
	item.bytes = append(data, '\n')
	select {
	case <-c.ctx.Done():
		return io.ErrClosedPipe
	default:
	}
	select {
	case c.output <- item:
		return nil
	default:
		c.Close()
		return errBackpressure
	}
}
func (c *connection) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case <-c.ctx.Done():
			return
		case item := <-c.output:
			data := item.bytes
			c.mu.Lock()
			c.writingID = item.id
			c.writeFence = make(chan struct{})
			c.mu.Unlock()
			for len(data) > 0 {
				n, e := c.io.Write(data)
				if e != nil || n <= 0 {
					c.Close()
					return
				}
				data = data[n:]
			}
			c.mu.Lock()
			if item.id.IsValid() {
				delete(c.busy, item.id)
				if s := c.pending[item.id]; s != nil {
					s.cancel()
					delete(c.pending, item.id)
				}
			}
			// Private IDs never repeat. Retire the old mapping and release its
			// fence atomically, before a reused wire ID gets a new private token.
			close(c.writeFence)
			c.writeFence = nil
			c.writingID = jsonrpc.ID{}
			c.mu.Unlock()
		}
	}
}

// A completed handler cannot leave a blocked output alive past its frame budget.
// This one connection watcher also cancels requests still waiting for SDK dispatch.
func (c *connection) watchDeadlines() {
	defer close(c.watchDone)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			now := c.clock.Now()
			stalled := false
			c.mu.Lock()
			for _, s := range c.pending {
				if !validRemaining(now, s.deadline) {
					s.cancel()
					stalled = stalled || s.responding
				}
			}
			c.mu.Unlock()
			if stalled {
				c.Close()
				return
			}
		}
	}
}

// exactID accepts lexical base-10 int64 integers and strings. Decimal/exponent
// forms (even 1.0/1e0), null and out-of-range integers close the connection.
// Absent IDs denote notifications. Normalize -0 and string escapes for matching.
func exactID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return "", errProtocol
		}
		canonical, _ := json.Marshal(value)
		return string(canonical), nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return "", errProtocol
	}
	return strconv.FormatInt(n, 10), nil
}
