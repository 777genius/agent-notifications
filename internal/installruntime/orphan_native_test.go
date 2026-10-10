package installruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func (f *orphanFixture) retainNative(t *testing.T) {
	t.Helper()
	source := nativeFixture(t)
	probe := filepath.Join(source, "PROBED")
	t.Cleanup(func() {
		if _, err := os.Lstat(probe); !os.IsNotExist(err) {
			t.Errorf("orphan validation/replay executed the retained native helper: %v", err)
		}
	})
	change, err := StageNative(f.ctx, f.control, source)
	if err != nil {
		t.Fatal(err)
	}
	// Retain a concrete installer-authenticated record; no caller replacement
	// digest or attestation is supplied to the orphan API.
	f.before, err = Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.primary, Owner: "existing-installer", ConsumerID: "retained", Native: change})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(source, "PROBED")); !os.IsNotExist(err) {
		t.Fatal("unknown native fixture was probed", err)
	}
}

func TestOrphanRetainedNativeBytesAndIdentityStayUnchangedWithoutProbe(t *testing.T) {
	f := newOrphanFixture(t)
	f.retainNative(t)
	f.abandon(t, true)
	p, err := PreviewOrphanConsumer(f.control, f.recoveryRequest())
	if err != nil || !p.NativeValidated || p.NativeIdentityRefresh {
		t.Fatalf("native preview: %+v %v", p, err)
	}
	f.interrupt(t, "ledger")
	got, err := Recover(f.ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	assertOrphanDelta(t, f, got, false)
	if !reflect.DeepEqual(got.Native, f.before.Native) {
		t.Fatal("orphan recovery promoted/replaced native record")
	}
	hash, err := treeFingerprint(got.Native.Path)
	if err != nil || hash != f.before.Native.SHA256 {
		t.Fatal("native bytes changed or helper ran", err)
	}
}

func TestOrphanAdmissionAndReplayRefuseNativeDamageWithoutExecution(t *testing.T) {
	for _, replay := range []bool{false, true} {
		for _, damage := range []string{"bytes", "inode", "symlink", "missing", "attestation", "floor", "escaped-path"} {
			name := "admission/" + damage
			if replay {
				name = "replay/" + damage
			}
			t.Run(name, func(t *testing.T) {
				f := newOrphanFixture(t)
				f.retainNative(t)
				f.abandon(t, true)
				if replay {
					f.interrupt(t, "transaction")
				}
				if damage == "attestation" || damage == "floor" || damage == "escaped-path" {
					if damage == "attestation" {
						f.before.Native.Attestation = []byte("changed attestation")
					}
					if damage == "floor" {
						f.before.DecoderFloor++
					}
					if damage == "escaped-path" {
						f.before.Native.Path = filepath.Join(f.root, "outside-native")
					}
					f.saveLedger(t)
				} else {
					replayContentDamage(t, f.before.Native, damage)
					if damage == "bytes" {
						// The changed helper would leave evidence if anyone tried to
						// requalify it by execution rather than rejecting its hash.
						probe := filepath.Join(f.root, "changed-native-PROBED")
						asset := filepath.Join(f.before.Native.Path, "Contents", "MacOS", "terminal-notifier-modern")
						if err := os.WriteFile(asset, []byte("#!/bin/sh\nprintf probed > '"+probe+"'\n"), 0755); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							if _, err := os.Lstat(probe); !os.IsNotExist(err) {
								t.Errorf("changed native helper executed: %v", err)
							}
						})
					}
				}
				images := orphanControlImages(t, f.control)
				var err error
				if replay {
					_, err = Recover(f.ctx, f.control)
				} else {
					_, err = RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest())
				}
				if err == nil {
					t.Fatal("changed/unknown native admitted")
				}
				assertOrphanImages(t, images)
				if _, err := os.Lstat(filepath.Join(f.before.Native.Path, "PROBED")); !os.IsNotExist(err) {
					t.Fatal("changed native helper executed", err)
				}
			})
		}
	}
}

func TestOrphanChecksumValidDecisionCannotAlterNativeMetadata(t *testing.T) {
	f := newOrphanFixture(t)
	f.retainNative(t)
	f.abandon(t, true)
	tx := f.interrupt(t, "transaction")
	for _, damage := range []string{"attestation", "bytes-hash", "directory-id", "path", "missing-generation-id"} {
		t.Run(damage, func(t *testing.T) {
			copy := tx
			copy.After = cloneOrphanLedger(tx.After)
			switch damage {
			case "attestation":
				copy.After.Native.Attestation = []byte("untrusted evidence")
			case "bytes-hash":
				copy.After.Native.SHA256 = identity([]byte("other hash"), 0600).SHA256
			case "directory-id":
				copy.After.Native.DirectoryID = "1:42"
			case "path":
				copy.After.Native.Path = filepath.Join(f.root, "elsewhere")
			case "missing-generation-id":
				// Missing historical identities must remain exact even on Darwin.
				missing := filepath.Join(f.control, "native", "missing-old.app")
				old := NativeGeneration{Path: missing, DirectoryID: "1:42", SHA256: identity([]byte("old"), 0600).SHA256}
				old.InstalledTreeSHA256 = installedNativeHash(old.SHA256, nil)
				copy.Before = cloneOrphanLedger(tx.Before)
				copy.Before.Native.Published = append(copy.Before.Native.Published, old)
				copy.After.Native.Published = append(copy.After.Native.Published, old)
				copy.After.Native.Published[len(copy.After.Native.Published)-1].DirectoryID = "2:42"
			}
			// Bypass the writer validator to model a checksummed malformed record.
			w := orphanTransactionWire{Schema: 5, Before: copy.Before, After: copy.After, ConfigPaths: copy.ConfigPaths, OrphanRecovery: copy.OrphanRecovery}
			w.Files.Changes = []File{}
			body, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeTransaction(orphanEnvelope(t, body)); err == nil {
				t.Fatal("arbitrary native metadata delta decoded")
			}
		})
	}
}
