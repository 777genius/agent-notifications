package installruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func (f *orphanFixture) interrupt(t *testing.T, boundary string) transaction {
	t.Helper()
	req := f.recoveryRequest()
	crash := errors.New("test interruption at " + boundary)
	_, err := Commit(f.ctx, Request{ControlRoot: f.control, OrphanRecovery: &req, Fault: func(phase string) error {
		if phase == boundary {
			return crash
		}
		return nil
	}})
	if !errors.Is(err, crash) {
		t.Fatalf("did not reach %s seam: %v", boundary, err)
	}
	tx, err := readTransactionFile(filepath.Join(f.control, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	if tx.Schema != 5 || tx.OrphanRecovery == nil || len(tx.Files) != 0 || tx.Native != nil {
		t.Fatalf("unexpected decision capabilities: %+v", tx)
	}
	return tx
}

func TestOrphanReplayAndRollbackConvergeTwiceAtBothBoundaries(t *testing.T) {
	for _, boundary := range []string{"transaction", "ledger"} {
		for _, rollback := range []bool{false, true} {
			name := boundary + "/redo"
			if rollback {
				name = boundary + "/rollback"
			}
			t.Run(name, func(t *testing.T) {
				f := newOrphanFixture(t)
				f.abandon(t, true)
				policy, _ := os.ReadFile(filepath.Join(f.control, "agent-notifications.json"))
				tx := f.interrupt(t, boundary)
				if !reflect.DeepEqual(tx.Before, f.before) {
					t.Fatal("journal mutated original before-image")
				}
				// Starting a new orphan decision never auto-replays pending work.
				images := orphanControlImages(t, f.control)
				if _, err := RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest()); err == nil {
					t.Fatal("new decision replayed a pending transaction")
				}
				assertOrphanImages(t, images)
				var got Ledger
				var err error
				if rollback {
					got, err = Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true})
				} else {
					got, err = Recover(f.ctx, f.control)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertOrphanDelta(t, f, got, rollback)
				completed := orphanControlImages(t, f.control)
				if rollback {
					got, err = Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true})
				} else {
					got, err = Recover(f.ctx, f.control)
				}
				if err != nil {
					t.Fatal("repeated recovery", err)
				}
				assertOrphanDelta(t, f, got, rollback)
				assertOrphanImages(t, completed)
				raw, _ := os.ReadFile(filepath.Join(f.control, "agent-notifications.json"))
				if !bytes.Equal(raw, policy) {
					t.Fatal("raw policy changed across recovery")
				}
				s, healthErr := ReadInstalledSnapshot(f.control)
				if rollback {
					if healthErr == nil || s.Recovery {
						t.Fatal("rollback claimed restored missing assets healthy", healthErr)
					}
				} else if healthErr != nil || !s.Enabled {
					t.Fatal("redo did not restore original enablement", healthErr)
				}
				if _, err := os.Lstat(f.runtime); !os.IsNotExist(err) {
					t.Fatal("replay recreated runtime", err)
				}
			})
		}
	}
}

func TestOrphanRollbackLedgerFaultKeepsReverseDecisionRetryable(t *testing.T) {
	for _, boundary := range []string{"transaction", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, true)
			original := f.interrupt(t, boundary)
			crash := errors.New("reverse ledger interrupted")
			_, err := Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true, Fault: func(phase string) error {
				if phase == "ledger" {
					return crash
				}
				return nil
			}})
			if !errors.Is(err, crash) {
				t.Fatal("reverse ledger fault not reached", err)
			}
			reverse, err := readTransactionFile(filepath.Join(f.control, "transaction.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !reverse.Rollback || !reflect.DeepEqual(reverse.OrphanRecovery, original.OrphanRecovery) {
				t.Fatal("reverse decision lost source absence/ownership fence")
			}
			images := orphanControlImages(t, f.control)
			if _, err := Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true, Fault: func(phase string) error {
				if phase == "ledger" {
					return crash
				}
				return nil
			}}); !errors.Is(err, crash) {
				t.Fatal("repeat reverse fault not reached", err)
			}
			assertOrphanImages(t, images)
			got, err := Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true})
			if err != nil {
				t.Fatal(err)
			}
			assertOrphanDelta(t, f, got, true)
			got, err = Recover(f.ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			assertOrphanDelta(t, f, got, true)
		})
	}
}

func TestOrphanReplayRefusesReappearedPayloadRegistrationAndParent(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		for _, damage := range []string{"payload", "registration", "missing-component", "runtime-parent-substitution", "registration-parent-substitution", "policy-edit", "retained-edit", "ledger-edit", "policy-generation-edit"} {
			name := "redo/" + damage
			if rollback {
				name = "rollback/" + damage
			}
			t.Run(name, func(t *testing.T) {
				f := newOrphanFixture(t)
				f.abandon(t, true)
				f.interrupt(t, "transaction")
				var foreignPath string
				switch damage {
				case "payload":
					foreignPath = f.orphanPaths[0]
					orphanWrite(t, foreignPath, "foreign bytes")
				case "registration":
					foreignPath = f.registration
					orphanWrite(t, foreignPath, "foreign bytes")
				case "missing-component":
					if err := os.MkdirAll(f.runtime, 0700); err != nil {
						t.Fatal(err)
					}
				case "runtime-parent-substitution", "registration-parent-substitution":
					// Registration and runtime are siblings. Replace their still-present
					// parent while all recorded payloads remain absent.
					parent := filepath.Dir(f.runtime)
					if err := os.Rename(parent, parent+"-old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(parent, 0700); err != nil {
						t.Fatal(err)
					}
				case "policy-edit":
					foreignPath = filepath.Join(f.control, "agent-notifications.json")
					orphanWrite(t, foreignPath, `{"schemaVersion":1,"enabled":true,"foreign":"edited"}`)
				case "retained-edit":
					foreignPath = filepath.Join(f.primary, "asset-00")
					orphanWrite(t, foreignPath, "foreign bytes")
				case "ledger-edit":
					f.before.Owner = "other installer"
					f.saveLedger(t)
				case "policy-generation-edit":
					if err := writeJSON(filepath.Join(f.control, "policy-generation.json"), runtimePolicy{f.before.PolicyGeneration + 99, true}); err != nil {
						t.Fatal(err)
					}
				}
				images := orphanControlImages(t, f.control)
				var foreign []byte
				if foreignPath != "" {
					foreign, _ = os.ReadFile(foreignPath)
				}
				var err error
				if rollback {
					_, err = Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true})
				} else {
					_, err = Recover(f.ctx, f.control)
				}
				if err == nil {
					t.Fatal("replay admitted changed absence/retained state")
				}
				assertOrphanImages(t, images)
				if foreignPath != "" {
					got, _ := os.ReadFile(foreignPath)
					if !bytes.Equal(got, foreign) {
						t.Fatal("replay overwrote foreign bytes")
					}
				}
			})
		}
	}
}

func TestOrphanPendingRecoveryNeverRecreatesPermanentLocks(t *testing.T) {
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		t.Run(name, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, true)
			f.interrupt(t, "transaction")
			path := filepath.Join(f.control, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			images := orphanControlImages(t, f.control)
			if _, err := Recover(f.ctx, f.control); err == nil {
				t.Fatal("replayed without original permanent lock")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("recovery recreated lock", err)
			}
			if _, err := Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true}); err == nil {
				t.Fatal("rolled back without original permanent lock")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("rollback recreated lock", err)
			}
			assertOrphanImages(t, images)
		})
	}
}

func TestOrphanForeignLedgerEditAtPublicationSeamCannotEnablePolicy(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	req := f.recoveryRequest()
	_, err := Commit(f.ctx, Request{ControlRoot: f.control, OrphanRecovery: &req, Fault: func(phase string) error {
		if phase == "ledger" {
			l, err := readLedger(f.control)
			if err != nil {
				t.Fatal(err)
			}
			l.Owner = "foreign installer"
			if err := writeJSON(filepath.Join(f.control, "ownership.json"), l); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}})
	if err == nil {
		t.Fatal("foreign ledger edit at final publication accepted")
	}
	images := orphanControlImages(t, f.control)
	var policy runtimePolicy
	if err := json.Unmarshal(images[filepath.Join(f.control, "policy-generation.json")], &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Enabled {
		t.Fatal("foreign ledger enabled policy")
	}
	if _, err := Recover(f.ctx, f.control); err == nil {
		t.Fatal("foreign ledger replayed")
	}
	assertOrphanImages(t, images)
}

func TestOrphanReplayRejectsCopiedControlAndReplacedPermanentLock(t *testing.T) {
	for _, damage := range []string{"control", ".component-install.lock", "agent-notifications.json.lock"} {
		t.Run(damage, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, true)
			f.interrupt(t, "transaction")
			if damage == "control" {
				old := f.control + "-old"
				if err := os.Rename(f.control, old); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.control, 0700); err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(old)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.IsDir() {
						t.Fatalf("unexpected fixture control directory %s", entry.Name())
					}
					data, err := os.ReadFile(filepath.Join(old, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(f.control, entry.Name()), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				path := filepath.Join(f.control, damage)
				// Keep the original inode alive so inode reuse cannot obscure the test.
				if err := os.Rename(path, path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			images := orphanControlImages(t, f.control)
			if _, err := Recover(f.ctx, f.control); err == nil {
				t.Fatal("copied control/replacement permanent lock admitted")
			}
			assertOrphanImages(t, images)
		})
	}
}

func TestOrphanReappearanceAtLedgerSeamKeepsMarkerAndPolicyDisabled(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	req := f.recoveryRequest()
	_, err := Commit(f.ctx, Request{ControlRoot: f.control, OrphanRecovery: &req, Fault: func(phase string) error {
		if phase == "ledger" {
			orphanWrite(t, f.registration, "new sibling registration")
		}
		return nil
	}})
	if err == nil {
		t.Fatal("publication did not repeat registration absence after ledger seam")
	}
	if _, err := os.Lstat(filepath.Join(f.control, "transaction.json")); err != nil {
		t.Fatal("actionable marker discarded", err)
	}
	data, err := os.ReadFile(filepath.Join(f.control, "policy-generation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy runtimePolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Enabled {
		t.Fatal("reappeared registration enabled final policy")
	}
	images := orphanControlImages(t, f.control)
	if _, err := Recover(f.ctx, f.control); err == nil {
		t.Fatal("retry accepted reappeared registration")
	}
	assertOrphanImages(t, images)
}
