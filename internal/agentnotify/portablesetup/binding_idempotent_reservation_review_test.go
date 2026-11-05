//go:build linux || darwin

package portablesetup

import (
	"errors"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestIdempotentBindingHonorsSelectedPendingUpdate(t *testing.T) {
	for _, action := range []string{"install", "update", "repair"} {
		t.Run(action, func(t *testing.T) {
			b, ledger := bindingFixture(t)
			svc := Service{}
			if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
				t.Fatal(err)
			}
			initial, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			req := Request{Binding: b, ExpectedGeneration: initial.Ledger.Generation,
				TreeDigest: "TEST-selected-tree", HelperDigest: "TEST-selected-helper"}
			confirmed, res, err := svc.publishIntent(testCtx(t), req, initial.Ledger.Generation, action, "confirmed", []string{"agent-notify"})
			if err != nil || res == nil {
				t.Fatalf("publish confirmed %s intent: %+v %v", action, res, err)
			}
			req.ExpectedGeneration = confirmed.Generation
			for _, provided := range []bool{false, true} {
				if provided {
					req.Reservation = res
				}
				if _, err := svc.CommitBinding(testCtx(t), req); err != nil {
					t.Fatalf("idempotent binding must admit selected %s intent (provided=%t): %v", action, provided, err)
				}
			}
			after, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
			if err != nil || after.Ledger.Generation != confirmed.Generation ||
				after.Ledger.PendingMutation == nil || *after.Ledger.PendingMutation != *res {
				t.Fatalf("selected update mutated reservation: %+v %v", after.Ledger.PendingMutation, err)
			}
			fake := *res
			fake.ID += "-foreign"
			req.Reservation = &fake
			if _, err := svc.CommitBinding(testCtx(t), req); !errors.Is(err, ErrConcurrentChange) {
				t.Fatalf("foreign handoff reservation accepted: %v", err)
			}
		})
	}
}

func TestIdempotentBindingRefusesForeignPendingActionsAndIdentity(t *testing.T) {
	for _, kind := range []string{"uninstall", "other-installation", "other-binding"} {
		t.Run(kind, func(t *testing.T) {
			b, ledger := bindingFixture(t)
			svc := Service{}
			if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
				t.Fatal(err)
			}
			first, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			req := Request{Binding: b, ExpectedGeneration: first.Ledger.Generation}
			action := "update"
			target := IntentTarget{Client: string(b.Integration), InstallationID: b.InstallationID, BindingID: b.BindingID, Units: []string{"agent-notify"}}
			switch kind {
			case "uninstall":
				action = "uninstall"
			case "other-installation":
				target.InstallationID = "TEST-foreign-installation"
			case "other-binding":
				target.BindingID = "TEST-foreign-binding"
			}
			req.IntentTargets = []IntentTarget{target}
			confirmed, res, err := svc.publishIntent(testCtx(t), req, first.Ledger.Generation, action, "confirmed", []string{"agent-notify"})
			if err != nil || res == nil {
				t.Fatalf("publish conflicting intent: %+v %v", res, err)
			}
			req.ExpectedGeneration = confirmed.Generation
			if _, err := svc.CommitBinding(testCtx(t), req); !errors.Is(err, ErrIntentConflict) {
				t.Fatalf("%s intent admitted unrelated binding: %v", kind, err)
			}
			snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
			if err != nil || snap.Ledger.Generation != confirmed.Generation ||
				snap.Ledger.PendingMutation == nil || *snap.Ledger.PendingMutation != *res {
				t.Fatalf("rejected operation mutated ledger: %+v %v", snap.Ledger.PendingMutation, err)
			}
		})
	}
}
