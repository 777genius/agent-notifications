// Package sharedrpc carries only Notify and Status between a lightweight MCP
// bridge and its authenticated, provider/profile/runtime-bound local service.
package sharedrpc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const maxFrame = 64 << 10

var errProtocol = errors.New("shared_protocol_error")

type request struct {
	Version  int                    `json:"version"`
	Method   string                 `json:"method"`
	Origin   *origin.Context        `json:"origin"`
	Payload  *agentnotify.Payload   `json:"payload,omitempty"`
	Deadline *notification.Deadline `json:"deadline,omitempty"`
}

type response struct {
	Version int                  `json:"version"`
	Receipt *agentnotify.Receipt `json:"receipt,omitempty"`
	Status  *mcp.Status          `json:"status,omitempty"`
	Error   string               `json:"error,omitempty"`
}

func marshalFrame(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || len(b) > maxFrame {
		return nil, errProtocol
	}
	frame := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(frame, uint32(len(b)))
	copy(frame[4:], b)
	return frame, nil
}

func readFrame(r io.Reader, v any) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return errProtocol
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > maxFrame {
		return errProtocol
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return errProtocol
	}
	if strictjson.Validate(b, strictjson.Budget{Bytes: maxFrame, Depth: 16, Entries: 64}) != nil {
		return errProtocol
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errProtocol
	}
	return nil
}

func remaining(now, deadline notification.Deadline) float64 {
	if now.BootID == "" || now.BootID != deadline.BootID || math.IsNaN(now.NotAfter) || math.IsInf(now.NotAfter, 0) || math.IsNaN(deadline.NotAfter) || math.IsInf(deadline.NotAfter, 0) {
		return 0
	}
	delta := deadline.NotAfter - now.NotAfter
	if delta <= 0 || delta > 15 {
		return 0
	}
	return delta
}

func rejected(reason string) agentnotify.Receipt {
	return agentnotify.Receipt{Status: "rejected", Reason: reason, RetrySafe: true}
}

func uncertain(reason string) agentnotify.Receipt {
	return agentnotify.Receipt{Status: "unknown", Reason: reason, RetrySafe: false}
}

func validReceipt(r agentnotify.Receipt) bool {
	if r.Status != "rejected" && r.Status != "submitted" && r.Status != "suppressed" && r.Status != "unknown" {
		return false
	}
	for _, s := range []string{r.TrackingID, string(r.KeyKind), r.Reason, r.Backend, r.Navigation.Capability, r.Navigation.Precision, r.Navigation.Scope, r.Navigation.Reason} {
		if !origin.Text(s, 256, false) {
			return false
		}
	}
	return r.RequestID == nil || origin.Text(*r.RequestID, 256, false)
}

func validStatus(s mcp.Status) bool {
	for _, value := range []string{s.Configuration, s.Capability, s.ContextReason, s.DedupScope, s.RateScope, s.Navigation.Capability, s.Navigation.Precision, s.Navigation.Scope, s.Navigation.Reason} {
		if !origin.Text(value, 128, false) {
			return false
		}
	}
	return s.Configuration != "" && s.Capability != "" && s.Navigation.Reason != ""
}
