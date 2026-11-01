package sharedrpc

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

// Client opens one authenticated connection per call. Dial must authenticate the
// daemon (AuthenticateClient) before returning; its failures precede RPC content.
// Dial may bootstrap/reconnect, but must never send or replay an RPC request.
type Client struct {
	Dial func(context.Context) (net.Conn, error)
}

var _ mcp.Backend = Client{}
var _ mcp.StatusPort = Client{}

func (c Client) Notify(ctx context.Context, p agentnotify.Payload, o origin.Context, deadline notification.Deadline) agentnotify.Receipt {
	if len(p.Title)+len(p.Body)+len(p.Category)+len(p.Navigation) > 16<<10 || (p.RequestID != nil && len(*p.RequestID) > 256) || !boundedOrigin(o) || len(deadline.BootID) > 256 {
		return rejected("invalid_arguments")
	}
	frame, err := marshalFrame(request{Version: 1, Method: "notify", Origin: &o, Payload: &p, Deadline: &deadline})
	if err != nil {
		return rejected("invalid_arguments")
	}
	r, attempted, err := c.call(ctx, frame)
	if err != nil {
		if attempted {
			return uncertain("shared_service_outcome_unknown")
		}
		return rejected("shared_service_unavailable")
	}
	if r.Error != "" && r.Receipt == nil && r.Status == nil {
		// The sole wire error is a server refusal before dispatch.
		if r.Error == "invalid_request" {
			return rejected("invalid_arguments")
		}
		return uncertain("shared_service_outcome_unknown")
	}
	if r.Receipt == nil || r.Status != nil || r.Error != "" || !validReceipt(*r.Receipt) {
		return uncertain("invalid_backend_result")
	}
	if r.Receipt.Status == "unknown" {
		r.Receipt.RetrySafe = false
	}
	return *r.Receipt
}

func (c Client) Status(ctx context.Context, o origin.Context) (mcp.Status, error) {
	if !boundedOrigin(o) {
		return mcp.Status{}, errors.New("shared_status_unavailable")
	}
	frame, err := marshalFrame(request{Version: 1, Method: "status", Origin: &o})
	if err != nil {
		return mcp.Status{}, errors.New("shared_status_unavailable")
	}
	r, _, err := c.call(ctx, frame)
	if err != nil || r.Error != "" || r.Receipt != nil || r.Status == nil || !validStatus(*r.Status) {
		return mcp.Status{}, errors.New("shared_status_unavailable")
	}
	return *r.Status, nil
}

func boundedOrigin(o origin.Context) bool {
	for _, value := range []string{o.Provider, o.Namespace, o.SessionID, o.AnonymousCaller, o.CallID, string(o.Provenance), string(o.Locality), string(o.Interface)} {
		if len(value) > 256 {
			return false
		}
	}
	return true
}

func (c Client) call(ctx context.Context, frame []byte) (response, bool, error) {
	if ctx == nil || ctx.Err() != nil || c.Dial == nil {
		return response{}, false, errProtocol
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := c.Dial(callCtx)
	if err != nil || conn == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return response{}, false, errProtocol
	}
	defer conn.Close()
	cleanup, err := boundConnection(callCtx, conn, 15*time.Second)
	if err != nil {
		return response{}, false, errProtocol
	}
	defer cleanup()
	// Even a Write returning zero bytes with an error is uncertain: wrappers and
	// transports need not prove that no bytes reached the peer. Never resend.
	if writeAll(conn, frame) != nil {
		return response{}, true, errProtocol
	}
	var r response
	if readFrame(conn, &r) != nil || r.Version != 1 {
		return response{}, true, errProtocol
	}
	return r, true, nil
}
