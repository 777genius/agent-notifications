package installruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReservationWriterFloor is published with guarded start/change/cleanup of a
// PendingMutation. Ordinary installs keep WriterFloor. The floor never decreases.
const ReservationWriterFloor = 2

// CoordinatorLockName is the single ControlRoot process lock that serializes
// setup coordinators. The kernel lock inode is separate; death releases this
// lease, but a persisted reservation remains.
const CoordinatorLockName = ".setup-coordinator.lock"

// ErrReservationConflict is a pending mutation blocking an unmatched writer.
var ErrReservationConflict = errors.New("pending mutation reservation blocks this operation")

// PendingMutation is the kernel-owned reservation. The intent payload lives in
// a host-owned file; this package does not parse it.
type PendingMutation struct {
	ID        string
	Owner     string
	IntentRef string
}

func acceptedLedgerSchema(schema int) bool {
	return schema == ledgerSchemaV1 || schema == ledgerSchemaV2 || schema == ledgerSchemaV3
}

func acceptedTransactionSchema(schema int) bool {
	return schema == transactionSchemaV1 || schema == transactionSchemaV2 || schema == transactionSchemaV3
}

// legacyAcceptedLedgerSchema is the frozen v1 writer contract: schema 3 is
// unknown, so a former decoder refuses a reserved or previously reserved ledger.
func legacyAcceptedLedgerSchema(schema int) bool {
	return schema == ledgerSchemaV1 || schema == ledgerSchemaV2
}

// legacyAcceptedTransactionSchema is the frozen v1 writer contract used to prove
// that a former decoder rejects reservation journals before live effects.
func legacyAcceptedTransactionSchema(schema int) bool {
	return schema == transactionSchemaV1 || schema == transactionSchemaV2
}

// legacyWriterAcceptsFloor is the frozen v1 live-ledger check. Reservation
// publishes WriterFloor 2; a former kernel must refuse rather than ignore it.
func legacyWriterAcceptsFloor(floor int) bool {
	return floor <= WriterFloor
}

func coordinatorLockPath(root string) string {
	return filepath.Join(root, CoordinatorLockName)
}

// AcquireCoordinatorLease serializes confirmed cross-system mutation/resume.
// Read-only operations must not take it. The OS releases the lease on process
// death; file age is not ownership.
func AcquireCoordinatorLease(ctx context.Context, root string) (func(), error) {
	if root == "" {
		var err error
		root, err = ControlRoot()
		if err != nil {
			return nil, err
		}
	}
	return Lock(ctx, coordinatorLockPath(root))
}

// Recover replays a pending journal for this control root and returns.
// It does not refresh, install, add a consumer, or require a package.
func Recover(ctx context.Context, controlRoot string) (Ledger, error) {
	return Commit(ctx, Request{ControlRoot: controlRoot, RecoverOnly: true})
}

func reservationRequestInvalid(r Request) error {
	if r.RecoverOnly && (r.PolicyOnly || r.RollbackPending || r.ClearReservation || r.Reservation != nil || r.RefreshOnly || r.RemoveConsumer || r.PurgeNative || r.RetireNative || r.Native != nil || r.Prepare != nil || len(r.Files) != 0 || r.PolicyEnabled != nil || len(r.PolicyFields) != 0) {
		return fmt.Errorf("recovery-only request cannot mutate or combine other modes")
	}
	if r.ClearReservation && (r.Reservation == nil || r.Reservation.ID == "") {
		return fmt.Errorf("clearing a reservation requires the matching reservation identity")
	}
	if r.Reservation != nil && (r.Reservation.ID == "" || r.Reservation.Owner == "" || r.Reservation.IntentRef == "") {
		return fmt.Errorf("reservation requires id, owner and intent reference")
	}
	return nil
}

func reservationMutation(r Request) bool {
	return r.Reservation != nil || r.ClearReservation
}

func applyReservationProtocol(next *Ledger, r Request, current Ledger) {
	if next.WriterFloor < current.WriterFloor {
		next.WriterFloor = current.WriterFloor
	}
	if next.WriterFloor < WriterFloor {
		next.WriterFloor = WriterFloor
	}
	if reservationMutation(r) || current.PendingMutation != nil || current.WriterFloor >= ReservationWriterFloor || current.Schema == ledgerSchemaV3 {
		if next.WriterFloor < ReservationWriterFloor {
			next.WriterFloor = ReservationWriterFloor
		}
		next.Schema = ledgerSchemaV3
		return
	}
	if next.Schema < ledgerSchemaV2 {
		next.Schema = ledgerSchemaV2
	}
}

func transactionSchemaFor(next Ledger, r Request) int {
	if reservationMutation(r) || next.Schema == ledgerSchemaV3 || next.WriterFloor >= ReservationWriterFloor {
		return transactionSchemaV3
	}
	return transactionSchemaV2
}

func applyReservationState(next *Ledger, r Request, current Ledger) error {
	switch {
	case r.ClearReservation:
		if current.PendingMutation == nil || r.Reservation == nil || r.Reservation.ID != current.PendingMutation.ID || r.Reservation.Owner != current.PendingMutation.Owner || r.Reservation.IntentRef != current.PendingMutation.IntentRef {
			return fmt.Errorf("%w: reservation identity does not match pending mutation", ErrReservationConflict)
		}
		next.PendingMutation = nil
	case r.Reservation != nil:
		if current.PendingMutation != nil && current.PendingMutation.ID != r.Reservation.ID {
			return fmt.Errorf("%w: another reservation is pending", ErrReservationConflict)
		}
		cp := *r.Reservation
		next.PendingMutation = &cp
	default:
		if current.PendingMutation != nil {
			cp := *current.PendingMutation
			next.PendingMutation = &cp
		}
	}
	return nil
}

func reservationAllows(r Request, l Ledger) error {
	if l.PendingMutation == nil || r.RecoverOnly {
		return nil
	}
	if policyDisableOnly(r) {
		return nil
	}
	if r.Reservation != nil && r.Reservation.ID == l.PendingMutation.ID && r.Reservation.Owner == l.PendingMutation.Owner && r.Reservation.IntentRef == l.PendingMutation.IntentRef {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrReservationConflict, l.PendingMutation.ID)
}

func recoverOnlyNoJournal(root string) (Ledger, error) {
	empty := Ledger{Consumers: map[string]Consumer{}, Files: map[string]Identity{}}
	if _, err := os.Lstat(filepath.Join(root, "ownership.json")); os.IsNotExist(err) {
		return empty, nil
	}
	if err := privateDirectory(root); err != nil {
		return empty, nil
	}
	l, err := readLedger(root)
	if err != nil {
		return l, err
	}
	if err := checkPolicyGeneration(root, l); err != nil {
		return l, err
	}
	return l, nil
}
