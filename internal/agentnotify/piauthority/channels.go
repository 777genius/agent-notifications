package piauthority

import (
	"context"
	"encoding/json"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const ChannelKey = "piNotifications"

type Choices struct {
	Desktop  *bool  `json:"desktop,omitempty"`
	Webhook  *bool  `json:"webhook,omitempty"`
	Preimage string `json:"preimage,omitempty"`
}

func channelChoices(route json.RawMessage) (desktop, webhook bool, exists bool, err error) {
	var fields map[string]json.RawMessage
	if len(route) > 0 && (json.Unmarshal(route, &fields) != nil || fields == nil) {
		return false, false, false, ErrDenied
	}
	raw, exists := fields[ChannelKey]
	if !exists {
		return false, false, false, nil
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil || value["desktop"] == nil || value["webhook"] == nil {
		return false, false, true, ErrDenied
	}
	var desktopValue, webhookValue *bool
	if json.Unmarshal(value["desktop"], &desktopValue) != nil || desktopValue == nil || json.Unmarshal(value["webhook"], &webhookValue) != nil || webhookValue == nil {
		return false, false, true, ErrDenied
	}
	for key := range value {
		if key != "desktop" && key != "webhook" {
			return false, false, true, ErrDenied
		}
	}
	return *desktopValue, *webhookValue, true, nil
}

// CommitChoices uses the existing policy transaction while setup owns its
// reservation. An omitted choice preserves the complete existing decision.
func CommitChoices(ctx context.Context, b portable.Binding, choices Choices, reservation *installruntime.PendingMutation, expected *installruntime.Identity) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		return err
	}
	if expected != nil && s.Preimage != *expected {
		return installruntime.ErrPolicyConflict
	}
	if err := b.CheckSnapshot(s.Installation); err != nil {
		return err
	}
	if err := installruntime.CheckObserverChannelPreimage(s.Fields["route"], ChannelKey, choices.Preimage); err != nil {
		return err
	}
	desktop, webhook, exists, err := channelChoices(s.Fields["route"])
	if err != nil {
		return err
	}
	if !exists {
		desktop = true
		webhook = false
	}
	if choices.Desktop != nil {
		desktop = *choices.Desktop
	}
	if choices.Webhook != nil {
		webhook = *choices.Webhook
	}
	if exists && choices.Desktop == nil && choices.Webhook == nil {
		return nil
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return err
	}
	route, err := json.Marshal(map[string]any{ChannelKey: map[string]bool{"desktop": desktop, "webhook": webhook}})
	if err != nil {
		return err
	}
	fields := map[string]json.RawMessage{"route": route}
	after, err := installruntime.PredictPolicyIdentity(b.ControlRoot, s.Preimage, fields)
	if err != nil {
		return err
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: key, Consumer: consumer, RefreshOnly: true, PolicyOnly: reservation == nil, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: fields, Reservation: reservation})
	if err == nil && expected != nil {
		*expected = after
	}
	return err
}
