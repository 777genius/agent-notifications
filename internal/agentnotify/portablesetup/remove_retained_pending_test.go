//go:build linux || darwin

package portablesetup

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestMaterializerRetainedRemoveRetryRequiresSettledState(t *testing.T) {
	for _, pending := range []string{"finalization", "kernel-transaction", "uap-journal", "settled"} {
		t.Run(pending, func(t *testing.T) {
			mat, req, root := registryMaterializerFixture(t)
			req.Integration = portable.Claude
			mat.Roots.ClaudeRunner = listingRunner{configRoot: req.ClientConfigRoot}
			if _, err := mat.Install(testCtx(t), req); err != nil {
				t.Fatal(err)
			}
			req.OperationID = "TEST-retained-remove-" + pending
			if pending == "finalization" {
				// UAP removal commits before reverse discovery refuses to restore
				// an unowned command, leaving the real host reservation pending.
				req.Discovery = Discovery{ConfigPath: filepath.Join(root, "TEST-reverse.json")}
			}
			err := mat.Remove(testCtx(t), req)
			if pending == "finalization" {
				var result ResultError
				if !errors.As(err, &result) || !result.Finalization || result.Result.Outcome != uapinstaller.OutcomeCompleted {
					t.Fatalf("expected committed removal with finalization failure: %v", err)
				}
				ledger, recovery, readErr := installruntime.ReadOwnership(req.Identity.ControlRoot)
				if readErr != nil || recovery || ledger.PendingMutation == nil {
					t.Fatalf("expected durable host reservation: %+v recovery=%v err=%v", ledger.PendingMutation, recovery, readErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			state, err := mat.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			installed, ok := findInstallation(state, req.Identity.InstallationID)
			if !ok || !installed.DataRetained || len(installed.Clients) != 0 {
				t.Fatalf("public removal did not retain an empty installation: %+v", installed)
			}
			switch pending {
			case "kernel-transaction":
				_, err := installruntime.Commit(testCtx(t), installruntime.Request{
					ControlRoot: req.Identity.ControlRoot, RuntimeRoot: req.Identity.RuntimeRoot,
					Owner: req.Identity.Owner, ConsumerID: "TEST-retained-pending",
					Files: []installruntime.File{{Path: filepath.Join(req.Identity.RuntimeRoot, "TEST-pending"), Data: []byte("pending"), Mode: 0700}},
					Fault: func(phase string) error {
						if phase == "transaction" {
							return errors.New("TEST-interrupted-kernel-commit")
						}
						return nil
					},
				})
				_, recovery, readErr := installruntime.ReadOwnership(req.Identity.ControlRoot)
				if err == nil || readErr != nil || !recovery {
					t.Fatalf("kernel fault did not leave a journal: commit=%v recovery=%v read=%v", err, recovery, readErr)
				}
			case "uap-journal":
				plantUAPPendingJournal(t, mat.Roots.OperationsDir, mat.Roots.ManagedRoot, "TEST-retained-uap-pending")
			}
			// A group retry cannot bypass the same settled-state checks
			// used by a single-client retry, regardless of target order.
			group := []MaterializeRequest{req, req}
			group[0].Integration, group[1].Integration = portable.Claude, portable.Codex
			beforeGroup := registryFixtureFiles(t, root)
			for _, keep := range []bool{false, true} {
				group[0].KeepReservation, group[1].KeepReservation = keep, keep
				for _, reversed := range []bool{false, true} {
					selected := append([]MaterializeRequest(nil), group...)
					if reversed {
						selected[0], selected[1] = selected[1], selected[0]
					}
					removed, groupErr := mat.RemoveGroup(testCtx(t), selected)
					if pending == "settled" {
						if groupErr != nil || len(removed) != 2 || !removed[0].AlreadyAbsent || !removed[1].AlreadyAbsent {
							t.Fatalf("settled group retry failed (keep=%v, reversed=%v): %+v %v", keep, reversed, removed, groupErr)
						}
					} else if len(removed) != 0 || !errors.Is(groupErr, uapinstaller.ErrRecoveryRequired) {
						t.Fatalf("group bypassed %s (keep=%v, reversed=%v): %+v %v", pending, keep, reversed, removed, groupErr)
					}
				}
			}
			if !reflect.DeepEqual(beforeGroup, registryFixtureFiles(t, root)) {
				t.Fatal("retained group retry changed persisted files")
			}
			if pending == "settled" {
				// Settled removal does not require the obsolete source or client.
				req.PackageRoot, req.ClientExecutable, req.ClientConfigRoot = "", "", ""
			}
			before := registryFixtureFiles(t, root)
			for _, keep := range []bool{false, true} {
				req.KeepReservation = keep
				err = mat.Remove(testCtx(t), req)
				if pending == "settled" || pending == "finalization" && keep {
					if err != nil {
						t.Fatalf("settled or caller-finalized retry failed (keep=%v): %v", keep, err)
					}
				} else if !errors.Is(err, uapinstaller.ErrRecoveryRequired) {
					t.Fatalf("retained retry must refuse pending %s without recovery (keep=%v): %v", pending, keep, err)
				}
				if after := registryFixtureFiles(t, root); !reflect.DeepEqual(before, after) {
					t.Fatal("retained retry changed persisted files")
				}
			}
			if pending == "finalization" {
				// Delegation cannot claim a different client's pending intent.
				req.Integration = portable.Codex
				if err := mat.Remove(testCtx(t), req); !errors.Is(err, uapinstaller.ErrRecoveryRequired) {
					t.Fatalf("unrelated caller-finalized retry was admitted: %v", err)
				}
				if after := registryFixtureFiles(t, root); !reflect.DeepEqual(before, after) {
					t.Fatal("unrelated retained retry changed persisted files")
				}
			}
		})
	}
}
