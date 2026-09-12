package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Backend is constructed once by composition and must honor cancellation.
type Backend interface {
	Notify(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt
}

// Status contains only bounded configuration and capability diagnostics, no history.
type Status struct {
	Enabled       bool   `json:"enabled"`
	Configuration string `json:"configuration"`
	Capability    string `json:"capability"`
	ContextReason string `json:"context_reason"`
	// DedupScope and RateScope describe identity buckets, not caller trust.
	// The runtime-wide rate cap also applies to every bucket.
	DedupScope string `json:"dedup_scope"`
	RateScope  string `json:"rate_scope"`
}

// StatusPort must only read configuration; no migration, probes or maintenance.
type StatusPort interface {
	Status(context.Context) (Status, error)
}
type Options struct {
	Backend Backend
	Status  StatusPort
	Clock   agentnotify.Clock
	// AdapterKind is operator configured: codex or claude. Never model supplied.
	AdapterKind string
	// CloseResources is called once after every SDK handler has joined.
	CloseResources func() error
}

// Run owns the supplied I/O. Close must unblock both Read and Write. Caller
// cancellation (including a signal-derived context), EOF and framing failure
// cancel all request contexts, join SDK handlers, then close composition resources.
// Ports must return on cancellation; Go cannot forcibly stop an arbitrary port.
func Run(ctx context.Context, owned io.ReadWriteCloser, o Options) error {
	if ctx == nil || owned == nil || o.Backend == nil || o.Status == nil || o.Clock == nil || (o.AdapterKind != "codex" && o.AdapterKind != "claude") {
		return errors.New("invalid_options")
	}
	c := newConnection(ctx, owned, o.Clock)
	defer func() { c.Close(); <-c.writerDone; <-c.watchDone }()
	s := sdk.NewServer(&sdk.Implementation{Name: "agent-notifications", Version: "phase5"}, &sdk.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (result sdk.Result, err error) {
			if method == "initialize" {
				// SDK v1.7 applies ProtocolVersionSupporter only to discovery,
				// not legacy initialize negotiation. Select our supported fallback
				// before the SDK stores the session parameters.
				init := req.(*sdk.ServerRequest[*sdk.InitializeParams])
				if !c.SupportsProtocolVersion(init.Params.ProtocolVersion) {
					init.Params.ProtocolVersion = "2025-11-25"
				}
			}
			// This adapter has no resources, subscriptions, prompts or server-initiated
			// requests. Reject discovery before its SDK handler mutates initialize state.
			switch method {
			case "initialize", "ping", "tools/list", "tools/call", "notifications/initialized":
			default:
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not supported"}
			}
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			state := c.state(req.GetExtra())
			if state == nil {
				return nil, errors.New("missing_frame_context")
			}
			callCtx, cancel := context.WithCancel(state.ctx)
			stop := context.AfterFunc(ctx, cancel)
			defer func() {
				stop()
				cancel()
				if recover() != nil {
					result = toolResult(agentnotify.Receipt{Status: "unknown", Reason: "internal_error", RetrySafe: false})
					err = nil
				}
			}()
			now := o.Clock.Now()
			if !validRemaining(now, state.deadline) {
				return toolResult(rejected("deadline_expired")), nil
			}
			// The joined connection watcher cancels the original per-frame context,
			// including time spent queued before this middleware.

			return next(callCtx, method, req)
		}
	})
	s.AddTool(&sdk.Tool{Name: "notify", Description: "Submit an explicit notification. Unknown outcomes are not safe to retry automatically.", InputSchema: notifySchema(), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		p, ok := decodePayload(req.Params.Arguments)
		if !ok {
			return toolResult(rejected("invalid_arguments")), nil
		}
		orig, reason := requestOrigin(o.AdapterKind, req.Params.Meta)
		if reason != "available" && p.Navigation != notification.None {
			return toolResult(rejected(reason)), nil
		}
		state := c.state(req.Extra)
		if state == nil || ctx.Err() != nil || !validRemaining(o.Clock.Now(), state.deadline) {
			return toolResult(rejected("deadline_expired")), nil
		}
		r := o.Backend.Notify(ctx, p, orig, state.deadline)
		if r.Status == "unknown" {
			r.RetrySafe = false
		}
		return toolResult(r), nil
	})
	s.AddTool(&sdk.Tool{Name: "notification_status", Description: "Read notification configuration and capability without sending.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var args map[string]json.RawMessage
		if len(req.Params.Arguments) != 0 && (json.Unmarshal(req.Params.Arguments, &args) != nil || args == nil || len(args) != 0) {
			return toolResult(rejected("invalid_arguments")), nil
		}
		status, e := o.Status.Status(ctx)
		if e != nil {
			return toolResult(rejected("configuration_unavailable")), nil
		}
		if !origin.Text(status.Configuration, 128, true) || !origin.Text(status.Capability, 128, true) {
			return toolResult(rejected("invalid_status")), nil
		}
		orig, reason := requestOrigin(o.AdapterKind, req.Params.Meta)
		status.ContextReason = reason
		status.DedupScope = requestScope(orig)
		status.RateScope = status.DedupScope
		raw, _ := json.Marshal(status)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}, StructuredContent: status}, nil
	})
	session, err := s.Connect(c.ctx, c, nil)
	if err == nil {
		err = session.Wait()
	}
	c.Close()
	<-c.writerDone
	<-c.watchDone
	if o.CloseResources != nil {
		if e := o.CloseResources(); e != nil {
			return errors.New("resource_close_failed")
		}
	}
	if err != nil && ctx.Err() == nil {
		return errors.New("connection_closed")
	}
	return ctx.Err()
}
func (c *connection) state(extra *sdk.RequestExtra) *frameState {
	if extra == nil {
		return nil
	}
	token := extra.Header[carrierKey]
	if len(token) != 1 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.pending {
		if s.token == token[0] {
			return s
		}
	}
	return nil
}
func validRemaining(now, deadline notification.Deadline) bool {
	return now.BootID != "" && now.BootID == deadline.BootID && !math.IsNaN(now.NotAfter) && !math.IsInf(now.NotAfter, 0) && deadline.NotAfter > now.NotAfter && deadline.NotAfter-now.NotAfter <= 15
}
func rejected(reason string) agentnotify.Receipt {
	return agentnotify.Receipt{Status: "rejected", Reason: reason, RetrySafe: true}
}
func toolResult(r agentnotify.Receipt) *sdk.CallToolResult {
	// Bound port results before JSON allocation. Invalid port output is an
	// uncertain outcome, never an invitation to repeat a possible submission.
	valid := r.Status == "rejected" || r.Status == "submitted" || r.Status == "suppressed" || r.Status == "unknown"
	for _, s := range []string{r.TrackingID, string(r.KeyKind), r.Reason, r.Backend, r.Navigation.Capability, r.Navigation.Precision, r.Navigation.Scope, r.Navigation.Reason} {
		valid = valid && origin.Text(s, 256, false)
	}
	if r.RequestID != nil {
		valid = valid && origin.Text(*r.RequestID, 256, false)
	}
	if !valid {
		r = agentnotify.Receipt{Status: "unknown", Reason: "invalid_backend_result", RetrySafe: false}
	}
	raw, _ := json.Marshal(r)
	return &sdk.CallToolResult{IsError: r.Status == "rejected", Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}, StructuredContent: r}
}
func requestOrigin(kind string, meta sdk.Meta) (origin.Context, string) {
	o := origin.Context{Provider: kind, Namespace: "mcp", AnonymousCaller: "shared_mcp", Provenance: origin.ClientMetadata, Locality: origin.LocalityUnknown, Interface: origin.InterfaceUnknown}
	if kind != "codex" {
		return o, "session_unavailable"
	}
	value, exists := meta["threadId"]
	if !exists {
		return o, "session_required"
	}
	id, ok := value.(string)
	if !ok || !origin.Text(id, 256, true) {
		return o, "invalid_session"
	}
	o.SessionID = id
	// JSON-RPC IDs, progressToken, callId and Claude toolUseId have no
	// separately qualified durable identity contract and are deliberately unused.
	return o, "available"
}

// requestScope uses the same origin validation as informational notify. A
// missing session can still use the shared anonymous bucket; this grants no
// navigation or policy permission and exposes no raw identity.
func requestScope(o origin.Context) string {
	if o.Validate(notification.None) != nil {
		return "unavailable"
	}
	if o.SessionID != "" {
		return "session"
	}
	return "shared_anonymous"
}
func decodePayload(raw []byte) (agentnotify.Payload, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return agentnotify.Payload{}, false
	}
	for _, key := range []string{"title", "body", "category"} {
		if _, ok := fields[key]; !ok {
			return agentnotify.Payload{}, false
		}
	}
	total := 0
	for k, v := range fields {
		switch k {
		case "title", "body", "category", "request_id", "navigation":
		default:
			return agentnotify.Payload{}, false
		}
		var value string
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, &value) != nil {
			return agentnotify.Payload{}, false
		}
		total += len(k) + len(value)
	}
	if total > 16*1024 {
		return agentnotify.Payload{}, false
	}
	var p agentnotify.Payload
	if json.Unmarshal(raw, &p) != nil {
		return p, false
	}
	if p.Navigation == "" {
		if _, exists := fields["navigation"]; exists {
			return p, false
		}
		p.Navigation = notification.Required
	}
	if p.Category != "info" && p.Category != "attention" && p.Category != "progress" {
		return p, false
	}
	if p.Navigation != notification.Required && p.Navigation != notification.BestEffort && p.Navigation != notification.None {
		return p, false
	}
	return p, true
}
func notifySchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"title", "body", "category"}, "properties": map[string]any{"title": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"}, "category": map[string]any{"type": "string", "enum": []string{"info", "attention", "progress"}}, "request_id": map[string]any{"type": "string"}, "navigation": map[string]any{"type": "string", "enum": []string{"required", "best_effort", "none"}, "default": "required"}}}
}
