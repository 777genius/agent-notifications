package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/777genius/agent-notifications/internal/qodersource"
	"io"
)

// QoderSource deliberately ignores transcript_path: no Qoder transcript schema
// is assumed by the notification pipeline.
type QoderSource struct{}

func (QoderSource) Decode(ctx context.Context, event string, input io.Reader) (Event, error) {
	raw, err := io.ReadAll(io.LimitReader(input, qodersource.MaxPayloadBytes+1))
	if err != nil {
		return Event{}, err
	}
	if len(raw) > qodersource.MaxPayloadBytes {
		return Event{}, fmt.Errorf("qoder payload exceeds limit")
	}
	d, err := qodersource.Decode(ctx, event, raw)
	if err != nil {
		return Event{}, err
	}
	ev := Event{Product: ProductQoder, PayloadEventName: "Stop", Session: SessionContext{SessionID: d.SessionID, CWD: d.CWD}, Payload: StopPayload{AssistantMessage: d.LastAssistantMessage, Continuation: d.StopHookActive}, Raw: append(json.RawMessage(nil), raw...)}
	return ev, ValidateEvent(ev)
}
