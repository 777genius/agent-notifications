package installruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Both generations originate in ordinary installer commits. Keep the previous
// generation out of Published to exercise its explicit, legacy record fields.
func (f *orphanFixture) retainPreviousNative(t *testing.T) {
	t.Helper()
	f.retainNative(t)
	source := nativeFixture(t)
	t.Cleanup(func() {
		if _, err := os.Lstat(filepath.Join(source, "PROBED")); !os.IsNotExist(err) {
			t.Errorf("previous-generation fixture executed the native helper: %v", err)
		}
	})
	if err := os.WriteFile(filepath.Join(source, "second-generation"), []byte("new generation"), 0600); err != nil {
		t.Fatal(err)
	}
	change, err := StageNative(f.ctx, f.control, source)
	if err != nil {
		t.Fatal(err)
	}
	f.before, err = Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.primary, Owner: "existing-installer", ConsumerID: "retained", Native: change})
	if err != nil {
		t.Fatal(err)
	}
	n := f.before.Native
	if n.PreviousPath == "" || n.PreviousPath == n.Path || n.PreviousDirectoryID == "" || n.PreviousSHA256 == "" {
		t.Fatal("ordinary commit did not retain a complete previous generation")
	}
	n.Published = []NativeGeneration{nativeGenerationOf(*n)}
	f.saveLedger(t)
	if _, err := ReadInstalledSnapshot(f.control); err != nil {
		t.Fatal("previous-generation fixture unhealthy", err)
	}
}

var orphanPreviousDamageCases = []string{
	"missing-sha-inside", "missing-sha-outside", "outside-with-sha",
	"missing-identity", "invalid-identity", "invalid-sha", "missing-path",
	"digest-only", "identity-only", "changed-bytes", "duplicate-path-digest", "duplicate-path-identity",
}

func damageOrphanPreviousNative(t *testing.T, f *orphanFixture, damage string) {
	t.Helper()
	n := f.before.Native
	switch damage {
	case "missing-sha-inside":
		n.PreviousSHA256 = ""
	case "missing-sha-outside", "outside-with-sha":
		outside := filepath.Join(f.root, "outside-native")
		if err := os.Rename(n.PreviousPath, outside); err != nil {
			t.Fatal(err)
		}
		n.PreviousPath = outside
		if damage == "missing-sha-outside" {
			n.PreviousSHA256 = ""
		}
	case "missing-identity":
		n.PreviousDirectoryID = ""
	case "invalid-identity":
		n.PreviousDirectoryID = "not-an-object-id"
	case "invalid-sha":
		n.PreviousSHA256 = "not-a-tree-digest"
	case "missing-path":
		n.PreviousPath = ""
	case "digest-only":
		n.PreviousPath, n.PreviousDirectoryID = "", ""
	case "identity-only":
		n.PreviousPath, n.PreviousSHA256 = "", ""
	case "changed-bytes":
		if err := os.WriteFile(filepath.Join(n.PreviousPath, "foreign-bytes"), []byte("preserve these bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	case "duplicate-path-digest":
		// Import deduplicates this path against the authoritative active record.
		n.PreviousPath, n.PreviousDirectoryID = n.Path, n.DirectoryID
	case "duplicate-path-identity":
		n.PreviousPath, n.PreviousSHA256, n.PreviousDirectoryID = n.Path, n.SHA256, "1:42"
	default:
		t.Fatal("unknown previous-generation damage", damage)
	}
	for _, gen := range n.Published {
		if gen.Path == n.PreviousPath && n.PreviousPath != n.Path {
			t.Fatal("previous path accidentally qualified through Published")
		}
	}
}

// Capture actual byte/mode fingerprints, including TEST-owned outside trees.
func assertOrphanNativeTreesUnchanged(t *testing.T, f *orphanFixture) func() {
	t.Helper()
	paths := []string{f.primary, filepath.Join(f.control, "native"), filepath.Join(f.root, "outside-native")}
	images := make(map[string]string, len(paths))
	for _, path := range paths {
		hash, err := treeFingerprint(path)
		if err != nil {
			t.Fatal(err)
		}
		images[path] = hash
	}
	return func() {
		t.Helper()
		for path, want := range images {
			got, err := treeFingerprint(path)
			if err != nil || got != want {
				t.Errorf("retained/outside tree changed: %s: %v", path, err)
			}
		}
	}
}

func TestOrphanPreviousNativeAdmissionRejectsIncompleteEvidence(t *testing.T) {
	for _, damage := range orphanPreviousDamageCases {
		t.Run(damage, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.retainPreviousNative(t)
			f.abandon(t, true)
			damageOrphanPreviousNative(t, f, damage)
			f.saveLedger(t)
			images := orphanControlImages(t, f.control)
			assertTrees := assertOrphanNativeTreesUnchanged(t, f)
			if _, err := PreviewOrphanConsumer(f.control, f.recoveryRequest()); err == nil {
				t.Error("preview admitted unqualified previous generation")
			}
			assertOrphanImages(t, images)
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest()); err == nil {
				t.Error("recovery admitted unqualified previous generation before journaling")
			}
			assertOrphanImages(t, images)
			assertTrees()
		})
	}
}

func TestOrphanPreviousNativeChecksumValidPendingDecisionsRejectIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"transaction-redo", "transaction-rollback", "ledger-redo", "ledger-rollback", "reverse-redo", "reverse-rollback"} {
		for _, damage := range orphanPreviousDamageCases {
			t.Run(mode+"/"+damage, func(t *testing.T) {
				f := newOrphanFixture(t)
				f.retainPreviousNative(t)
				f.abandon(t, true)
				boundary := "transaction"
				if mode == "ledger-redo" || mode == "ledger-rollback" {
					boundary = "ledger"
				}
				tx := f.interrupt(t, boundary)
				if mode == "reverse-redo" || mode == "reverse-rollback" {
					crash := errors.New("reverse ledger seam")
					_, err := Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true, Fault: func(phase string) error {
						if phase == "ledger" {
							return crash
						}
						return nil
					}})
					if !errors.Is(err, crash) {
						t.Fatal("reverse seam not reached", err)
					}
					tx, err = readTransactionFile(filepath.Join(f.control, "transaction.json"))
					if err != nil || !tx.Rollback {
						t.Fatal("reverse decision missing", err)
					}
				}
				var err error
				f.before, err = readLedger(f.control)
				if err != nil {
					t.Fatal(err)
				}
				damageOrphanPreviousNative(t, f, damage)
				tx.Before.Native = cloneOrphanLedger(f.before).Native
				tx.After.Native = cloneOrphanLedger(f.before).Native
				f.saveLedger(t)
				if !reflect.DeepEqual(f.before, tx.Before) && !reflect.DeepEqual(f.before, tx.After) {
					t.Fatal("live ledger does not exactly match either journal image; would only exercise CAS")
				}
				// Write a checksum-valid schema5 envelope without using its validating writer.
				w := orphanTransactionWire{Schema: 5, Before: tx.Before, After: tx.After, ConfigPaths: tx.ConfigPaths, OrphanRecovery: tx.OrphanRecovery, Rollback: tx.Rollback}
				w.Files.Changes = []File{}
				body, err := json.Marshal(w)
				if err != nil {
					t.Fatal(err)
				}
				data := orphanEnvelope(t, body)
				if err := os.WriteFile(filepath.Join(f.control, "transaction.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				images := orphanControlImages(t, f.control)
				assertTrees := assertOrphanNativeTreesUnchanged(t, f)
				if err := validateOrphanTransaction(tx); err == nil {
					t.Error("independent validation accepted unqualified previous generation in both images")
				}
				if _, err := decodeTransaction(data); err == nil {
					t.Error("checksum-valid unqualified previous generation decoded")
				}
				if mode == "transaction-rollback" || mode == "ledger-rollback" || mode == "reverse-rollback" {
					_, err = Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true})
				} else {
					_, err = Recover(f.ctx, f.control)
				}
				if err == nil {
					t.Error("pending decision replayed unqualified previous generation")
				}
				assertOrphanImages(t, images)
				assertTrees()
			})
		}
	}
}

func TestOrphanPreviousNativeCompleteEvidencePreservesPresentAndAbsentHistory(t *testing.T) {
	for _, absent := range []bool{false, true} {
		for _, mode := range []string{"admission", "redo", "rollback"} {
			name := "present/" + mode
			if absent {
				name = "absent/" + mode
			}
			t.Run(name, func(t *testing.T) {
				f := newOrphanFixture(t)
				f.retainPreviousNative(t)
				if absent {
					if err := os.RemoveAll(f.before.Native.PreviousPath); err != nil {
						t.Fatal(err)
					}
				}
				f.abandon(t, true)
				images := orphanControlImages(t, f.control)
				assertTrees := assertOrphanNativeTreesUnchanged(t, f)
				if p, err := PreviewOrphanConsumer(f.control, f.recoveryRequest()); err != nil || !p.NativeValidated {
					t.Fatalf("complete previous-generation preview: %+v %v", p, err)
				}
				assertOrphanImages(t, images)
				var got Ledger
				var err error
				if mode == "admission" {
					got, err = RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest())
				} else {
					f.interrupt(t, "ledger")
					if mode == "rollback" {
						got, err = Commit(f.ctx, Request{ControlRoot: f.control, RollbackPending: true})
					} else {
						got, err = Recover(f.ctx, f.control)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				assertOrphanDelta(t, f, got, mode == "rollback")
				if !reflect.DeepEqual(got.Native, f.before.Native) {
					t.Fatal("recovery changed complete previous-generation evidence/identity")
				}
				assertTrees()
			})
		}
	}
}
