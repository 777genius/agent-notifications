package copilotvscodeinstall

import (
	"context"
	"encoding/json"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

type RevokeSelection uint8

const (
	RevokeAll RevokeSelection = iota + 1
	RevokeNative
	RevokeManual
)

// RevokeChannels uses consent ownership, not delivery availability. No physical
// profile/helper/package lookup precedes the false commit. It retains the one
// consumer and all Files/Native records; cleanup/removal belongs to N2b.
func RevokeChannels(ctx context.Context, b portable.Binding, selection RevokeSelection) (installruntime.Ledger, error) {
	key, consumer, _, err := b.Registration()
	if err != nil || b.Integration != portable.CopilotVSCode {
		return installruntime.Ledger{}, ErrDenied
	}
	l, recovery, err := installruntime.ReadOwnership(b.ControlRoot)
	if err != nil {
		return l, err
	}
	if recovery {
		return l, installruntime.ErrPolicyRecovery
	}
	if !recorded(installruntime.PolicySnapshot{Installation: installruntime.InstalledSnapshot{Ledger: l}}, b) {
		return l, ErrDenied
	}
	var patch string
	switch selection {
	case RevokeAll:
		patch = `{"copilotVSCodeNotifications":{"desktop":false,"webhook":false,"manual":{"enabled":false}}}`
	case RevokeNative:
		patch = `{"copilotVSCodeNotifications":{"desktop":false,"webhook":false}}`
	case RevokeManual:
		patch = `{"copilotVSCodeNotifications":{"manual":{"enabled":false}}}`
	default:
		return l, ErrDenied
	}
	s, err := installruntime.ReadRevocationSnapshot(ctx, b.ControlRoot)
	if err != nil {
		return l, err
	}
	return installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer,
		PolicyOnly: true, RefreshOnly: true, RevokeCopilotVSCode: true,
		ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(patch)},
	})
}
