package piauthority

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// PreparationConsumer truthfully owns the acquired shared runtime before Pi's
// native package/portable consumer exists. It never authorizes Pi events.
const PreparationConsumer = "pi-runtime-preparation"

// RetirePreparation runs only after the real Pi consumer commit. Asset ownership
// and every sibling consumer remain intact; a failed setup can retry the same
// exact handoff instead of claiming an unrelated hooks registration.
func RetirePreparation(ctx context.Context, b portable.Binding, reservation *installruntime.PendingMutation, expected *installruntime.Identity) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		return err
	}
	if expected != nil && *expected != s.Preimage {
		return installruntime.ErrPolicyConflict
	}
	if b.Integration != portable.Pi || b.CheckSnapshot(s.Installation) != nil {
		return ErrDenied
	}
	consumer, exists := s.Installation.Ledger.Consumers[PreparationConsumer]
	if !exists {
		return nil
	}
	wanted := installruntime.Consumer{RuntimeRoot: b.RuntimeRoot}
	if runtime.GOOS == "windows" {
		executable, err := portable.ResolvePrimaryExecutable(s.Installation.Ledger, b.Primary)
		if err != nil {
			return err
		}
		wanted.Registration = filepath.Join(b.RuntimeRoot, "hooks", "hooks.json")
		wanted.Commands = []string{executable}
	}
	if !reflect.DeepEqual(consumer, wanted) {
		return ErrDenied
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: PreparationConsumer, RemoveConsumer: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, Reservation: reservation})
	return err
}
