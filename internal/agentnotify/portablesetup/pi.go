package portablesetup

import (
	"context"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// commitPiBinding is the existing consumer/policy/runtime handoff for both a
// changed package callback and an unchanged package's explicit channel choice.
func (m Materializer) commitPiBinding(ctx context.Context, req MaterializeRequest, b portable.Binding, reservation *installruntime.PendingMutation) (uint64, error) {
	action := string(packageOperation(req))
	if req.HandoffAction != "" {
		action = req.HandoffAction
	}
	selected, err := m.Kernel.matchingReservation(Request{Binding: b, Reservation: reservation, SourceDigest: req.SourceDigest, TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion}, action)
	if err != nil {
		return 0, err
	}
	s, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		return 0, err
	}
	if _, err := m.Kernel.CommitBinding(ctx, Request{Binding: b, ExpectedGeneration: s.Ledger.Generation, Reservation: selected}); err != nil {
		return 0, err
	}
	if err := piauthority.RetirePreparation(ctx, b, selected, m.Kernel.ExpectedPolicy); err != nil {
		return 0, err
	}
	if err := piauthority.CommitChoices(ctx, b, m.PiChoices, selected, m.Kernel.ExpectedPolicy); err != nil {
		return 0, err
	}
	if err := piauthority.Publish(ctx, b, m.Roots.StateFile); err != nil {
		return 0, err
	}
	s, err = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	return s.Ledger.Generation, err
}
