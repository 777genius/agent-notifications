//go:build darwin

package installruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These fixtures never execute native code, mount volumes, or scan resources.
func reviewManagedNative(t *testing.T, channel string) (context.Context, Request, Ledger) {
	t.Helper()
	ctx, r := request(t)
	if channel != "global" {
		r.ConsumerID = channel + "-notifications"
	}
	r.Consumer.Registration = filepath.Join(r.ControlRoot, "receipt.json")
	var err error
	r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	r.Files = []File{{Path: r.Consumer.Registration, Data: []byte("inert receipt"), Mode: 0600}}
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// Commit canonicalizes runtime roots (for example /var -> /private/var on macOS).
	r.RuntimeRoot = l.Consumers[r.ConsumerID].RuntimeRoot
	enabled := true
	r.Native, r.Files = nil, nil
	r.RefreshOnly, r.PolicyOnly = true, true
	r.ExpectedGeneration, r.PolicyEnabled = &l.Generation, &enabled
	r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":true},"openCodeNotifications":{"desktop":true,"webhook":true},"foreign":{"keep":true}}`)}
	l, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PolicyEnabled, r.PolicyFields = nil, nil
	return ctx, r, l
}

func reviewRevoke(t *testing.T, ctx context.Context, r Request, channel string) Request {
	t.Helper()
	s, err := ReadRevocationSnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedGeneration, r.ExpectedPolicy = &s.Generation, &s.Preimage
	switch channel {
	case "global":
		off := false
		r.PolicyEnabled = &off
	case "gemini":
		r.RevokeGemini = true
		r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)}
	case "opencode":
		r.RevokeOpenCode = true
		r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":false,"webhook":false}}`)}
	}
	return r
}

func reviewDamageNative(t *testing.T, path, damage string) func() {
	t.Helper()
	saved := path + "-saved"
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if damage == "symlink" {
		if err := os.Symlink(saved, path); err != nil {
			t.Fatal(err)
		}
	} else {
		// Identical contents, different inode: consent must not depend on a digest.
		tree, err := openNativeRoot(saved)
		if err != nil {
			t.Fatal(err)
		}
		defer tree.Close()
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		id, err := nativeDirectoryID(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := copyOpenedNativeTree(tree, path, id); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(saved, path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoveryReviewDamagedNativeRevocation(t *testing.T) {
	for _, channel := range []string{"global", "gemini", "opencode"} {
		for _, damage := range []string{"inode", "symlink"} {
			for _, boundary := range []string{"none", "transaction", "policy", "ledger"} {
				t.Run(channel+"/"+damage+"/"+boundary, func(t *testing.T) {
					ctx, r, before := reviewManagedNative(t, channel)
					restore := reviewDamageNative(t, before.Native.Path, damage)
					r = reviewRevoke(t, ctx, r, channel)
					crash := errors.New("review crash")
					r.Fault = func(phase string) error {
						if phase == boundary || boundary == "policy" && strings.HasPrefix(phase, "promotion:") {
							return crash
						}
						return nil
					}
					result, err := Commit(ctx, r)
					marker := filepath.Join(r.ControlRoot, "transaction.json")
					if boundary != "none" {
						if !errors.Is(err, crash) {
							t.Fatalf("revocation failed before requested crash: %v", err)
						}
						journal, err := os.ReadFile(marker)
						if err != nil {
							t.Fatal(err)
						}
						tx, err := readTransactionFile(marker)
						if err != nil {
							t.Fatal(err)
						}
						current, err := readLedger(r.ControlRoot)
						if err != nil {
							t.Fatal(err)
						}
						if err := recoverTransaction(ctx, r.ControlRoot, current, tx, func(phase string) error {
							if phase == "ledger" {
								return crash
							}
							return nil
						}); !errors.Is(err, crash) {
							t.Fatalf("policy replay depends on native availability: %v", err)
						}
						unchanged, err := os.ReadFile(marker)
						if err != nil || !reflect.DeepEqual(journal, unchanged) {
							t.Fatal("replay rewrote journal", err)
						}
						result, err = Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
						if err != nil {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatalf("bounded revocation depends on native availability: %v", err)
					}
					expected := before
					expected.Generation++
					expected.PolicyGeneration++
					if channel == "global" {
						expected.Enabled = false
					}
					if !reflect.DeepEqual(result, expected) {
						t.Fatal("revocation changed unverified native ownership or other ledger fields")
					}
					if _, err := os.Lstat(marker); !os.IsNotExist(err) {
						t.Fatal("replay marker remains", err)
					}
					restore()
					s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
					if err != nil {
						t.Fatal(err)
					}
					var route map[string]map[string]bool
					if err := json.Unmarshal(s.Fields["route"], &route); err != nil {
						t.Fatal(err)
					}
					if channel == "global" {
						if s.Policy.Enabled {
							t.Fatal("restoration restored consent")
						}
					} else {
						key := "geminiNotifications"
						if channel == "opencode" {
							key = "openCodeNotifications"
						}
						if route[key]["desktop"] || route[key]["webhook"] || !route["foreign"]["keep"] {
							t.Fatal("revocation lost or foreign policy changed")
						}
					}
				})
			}
		}
	}
}

// Independently exercise replay's exception: the journal is recorded while
// native is healthy, then the inode/symlink substitution happens during downtime.
func TestRecoveryReviewPolicyReplayAfterNativeDamage(t *testing.T) {
	for _, channel := range []string{"global", "gemini", "opencode"} {
		for _, damage := range []string{"inode", "symlink"} {
			t.Run(channel+"/"+damage, func(t *testing.T) {
				ctx, r, before := reviewManagedNative(t, channel)
				r = reviewRevoke(t, ctx, r, channel)
				crash := errors.New("journal crash")
				r.Fault = func(phase string) error {
					if phase == "transaction" {
						return crash
					}
					return nil
				}
				if _, err := Commit(ctx, r); !errors.Is(err, crash) {
					t.Fatal("journal fixture", err)
				}
				reviewDamageNative(t, before.Native.Path, damage)
				result, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
				if err != nil {
					t.Fatalf("bounded policy replay requires native availability: %v", err)
				}
				expected := before
				expected.Generation++
				expected.PolicyGeneration++
				if channel == "global" {
					expected.Enabled = false
				}
				if !reflect.DeepEqual(result, expected) {
					t.Fatal("policy replay adopted unverified native ownership")
				}
			})
		}
	}
}

// Native damage must not block the same bounded disable when the normal writer
// protocol upgrades a legacy ledger, or retains an existing pending reservation.
func TestRecoveryReviewDisableProtocolFloors(t *testing.T) {
	for _, mode := range []string{"legacy", "reservation", "policy-drift"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r, before := reviewManagedNative(t, "global")
			if mode == "legacy" {
				before.Schema = ledgerSchemaV1
				before.WriterFloor = 0
			} else if mode == "policy-drift" {
				// A manual policy edit can disagree with the runtime fence.
				// An explicit CAS-bound global disable still only revokes.
				before.Enabled = false
				if err := writeJSON(filepath.Join(r.ControlRoot, "policy-generation.json"), runtimePolicy{before.PolicyGeneration, false}); err != nil {
					t.Fatal(err)
				}
			} else {
				before.Schema = ledgerSchemaV3
				before.WriterFloor = ReservationWriterFloor
				before.PendingMutation = &PendingMutation{ID: "inert reservation", Owner: r.Owner, IntentRef: filepath.Join(r.ControlRoot, "inert-intent")}
			}
			if err := writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), before); err != nil {
				t.Fatal(err)
			}
			reviewDamageNative(t, before.Native.Path, "inode")
			r = reviewRevoke(t, ctx, r, "global")
			result, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			expected := before
			applyReservationProtocol(&expected, Request{}, before)
			expected.Generation++
			expected.PolicyGeneration++
			expected.Enabled = false
			if !reflect.DeepEqual(result, expected) {
				t.Fatal("disable changed native/reservation or downgraded floors")
			}
		})
	}
}

func TestRecoveryReviewRevocationFences(t *testing.T) {
	for _, channel := range []string{"global", "gemini", "opencode"} {
		t.Run(channel, func(t *testing.T) {
			ctx, r, before := reviewManagedNative(t, channel)
			reviewDamageNative(t, before.Native.Path, "inode")
			r = reviewRevoke(t, ctx, r, channel)
			for name, change := range map[string]func(*Request){
				"owner":        func(r *Request) { r.Owner = "foreign" },
				"registration": func(r *Request) { r.RuntimeRoot = filepath.Join(r.RuntimeRoot, "foreign") },
				"generation":   func(r *Request) { g := *r.ExpectedGeneration - 1; r.ExpectedGeneration = &g },
				"policy-cas":   func(r *Request) { r.ExpectedPolicy = &Identity{Exists: true, SHA256: "foreign"} },
				"new-admission": func(r *Request) {
					r.PolicyEnabled = nil
					r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":true}}`)}
				},
				"asset-write": func(r *Request) {
					r.PolicyOnly = false
					r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "foreign"), Data: []byte("foreign"), Mode: 0600}}
				},
			} {
				t.Run(name, func(t *testing.T) {
					bad := r
					change(&bad)
					if _, err := Commit(ctx, bad); err == nil {
						t.Fatal("broader mutation admitted")
					}
					l, err := readLedger(r.ControlRoot)
					if err != nil || !reflect.DeepEqual(l, before) {
						t.Fatal("refusal changed ledger", err)
					}
					got, err := Fingerprint(filepath.Join(r.ControlRoot, "agent-notifications.json"))
					if err != nil || got != *r.ExpectedPolicy {
						t.Fatal("refusal changed policy", err)
					}
				})
			}
			before.WriterFloor = SupportedWriterFloor + 1
			if err := writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), before); err != nil {
				t.Fatal(err)
			}
			if _, err := Commit(ctx, r); err == nil {
				t.Fatal("writer floor bypassed")
			}
		})
	}
}

// Write an old A journal, recover on B until the final purge-entry deletion,
// then model a subsequent C boot by changing all surviving ledger IDs together.
// Disk inodes never change. The journal and its before-images remain untouched.
func TestRecoveryReviewRetirementSecondRenumber(t *testing.T) {
	for _, mode := range []string{"same-boot", "second-renumber", "foreign-device", "inconsistent-map", "merged-group", "inode", "missing-manifest", "manifest-device", "manifest-entry-device", "replacement", "survivor", "no-survivor", "alias", "manifest-path", "manifest-escape", "inventory-field", "policy-field"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r, l := reviewManagedNative(t, "global")
			// The legacy candidate is inert and unpublished as a callback. Use the
			// existing drain-check fixture seam; no OS process/resource scan is run.
			oldDrain := nativeDrainCheck
			nativeDrainCheck = func(context.Context, string) error { return nil }
			t.Cleanup(func() { nativeDrainCheck = oldDrain })
			candidate := filepath.Join(filepath.Dir(l.Native.Path), ".candidate-retired.app")
			if mode == "replacement" {
				if err := os.Mkdir(candidate+"-replacement", 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(candidate, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(candidate, "inert"), []byte("retired data"), 0600); err != nil {
				t.Fatal(err)
			}
			rid, err := nativeDirectoryID(candidate)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := treeFingerprint(candidate)
			if err != nil {
				t.Fatal(err)
			}
			l.Native.DecoderFloor = 1
			l.DecoderFloor = 1
			l.Native.PreviousPath, l.Native.PreviousDirectoryID, l.Native.PreviousSHA256 = candidate, rid, digest
			l.Native.Published = append(l.Native.Published, NativeGeneration{Path: candidate, DirectoryID: rid, SHA256: digest, DecoderFloor: 1, InstalledTreeSHA256: installedNativeHash(digest, nil)})
			if err := writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), l); err != nil {
				t.Fatal(err)
			}
			r.Native = nil
			r.Files = nil
			r.PolicyOnly = false
			r.RefreshOnly = false
			r.RetireNative = true
			r.ExpectedGeneration = &l.Generation
			crash := errors.New("retirement crash")
			r.Fault = func(phase string) error {
				if phase == "transaction" {
					return crash
				}
				return nil
			}
			if _, err := Commit(ctx, r); !errors.Is(err, crash) {
				t.Fatal("retirement fixture", err)
			}
			tx := rewriteRenumberTransaction(t, r.ControlRoot) // A
			rewriteRenumberLedger(t, r.ControlRoot)
			marker := filepath.Join(r.ControlRoot, "transaction.json")
			journal, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			current, err := readLedger(r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			err = recoverTransaction(ctx, r.ControlRoot, current, tx, func(phase string) error {
				if phase == "purge-entry:"+candidate {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatal("did not crash after candidate deletion", err)
			}
			if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
				t.Fatal("candidate not deleted", err)
			}
			current, err = readLedger(r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			} // B
			if mode != "same-boot" {
				renumberRecord(current.Native)
			} // B historical, disk is C
			if mode != "same-boot" { // Distinct from A; both historical mappings need live evidence.
				renumberRecord(current.Native)
			}
			index := len(current.Native.Published) - 1
			switch mode {
			case "foreign-device":
				_, ino, _ := splitObjectID(current.Native.Published[index].DirectoryID)
				current.Native.Published[index].DirectoryID = "777777:" + ino
			case "inconsistent-map":
				current.Native.Published[0].DirectoryID = renumberID(current.Native.Published[0].DirectoryID)
			case "merged-group":
				i := len(tx.Native.Parents) - 1
				tx.Native.Parents[i].Identity = renumberID(tx.Native.Parents[i].Identity)
			case "inode":
				current.Native.Published[index].DirectoryID = objectDevice(current.Native.Published[index].DirectoryID) + ":0"
			case "missing-manifest":
				tx.Native.PurgeTrees = nil
			case "manifest-device":
				e := tx.Native.PurgeTrees[0].Entries["."]
				_, ino, _ := splitObjectID(e.Directory)
				e.Directory = "777777:" + ino
				tx.Native.PurgeTrees[0].Entries["."] = e
			case "manifest-entry-device":
				e := tx.Native.PurgeTrees[0].Entries["inert"]
				_, ino, _ := splitObjectID(e.ObjectID)
				e.ObjectID = "777777:" + ino
				tx.Native.PurgeTrees[0].Entries["inert"] = e
			case "replacement":
				if err := os.Rename(candidate+"-replacement", candidate); err != nil {
					t.Fatal(err)
				}
			case "survivor":
				reviewDamageNative(t, l.Native.Path, "inode")
			case "no-survivor":
				if err := os.Rename(l.Native.Path, l.Native.Path+"-saved"); err != nil {
					t.Fatal(err)
				}
			case "manifest-path":
				tx.Native.PurgeTrees[0].Path += "-foreign"
			case "manifest-escape":
				tx.Native.PurgeTrees[0].Entries["../escaped"] = tx.Native.PurgeTrees[0].Entries["inert"]
			case "inventory-field":
				current.Native.Published[index].SHA256 = "foreign"
			case "alias":
				parent := filepath.Dir(candidate)
				if err := os.Rename(parent, parent+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parent+"-saved", parent); err != nil {
					t.Fatal(err)
				}
			case "policy-field":
				current.Enabled = !current.Enabled
			}
			if err := writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), current); err != nil {
				t.Fatal(err)
			}
			currentBefore, _ := json.Marshal(current)
			snapshotBefore, _ := json.Marshal(tx)
			err = recoverTransaction(ctx, r.ControlRoot, current, tx, nil)
			positive := mode == "same-boot" || mode == "second-renumber"
			if positive && err != nil {
				t.Fatalf("valid retirement replay refused: %v", err)
			}
			if !positive && err == nil {
				t.Fatal("unsafe retirement replay accepted")
			}
			currentAfter, _ := json.Marshal(current)
			snapshotAfter, _ := json.Marshal(tx)
			if !reflect.DeepEqual(currentBefore, currentAfter) || !reflect.DeepEqual(snapshotBefore, snapshotAfter) {
				t.Fatal("replay mutated supplied snapshots")
			}
			if positive {
				result, err := readLedger(r.ControlRoot)
				if err != nil {
					t.Fatal(err)
				}
				if result.Generation != tx.After.Generation || result.Native.PreviousPath != "" {
					t.Fatal("retirement not completed")
				}
				if _, err := os.Lstat(marker); !os.IsNotExist(err) {
					t.Fatal("marker not retired", err)
				}
			} else {
				unchanged, err := os.ReadFile(marker)
				if err != nil || !reflect.DeepEqual(journal, unchanged) {
					t.Fatal("refused recovery changed journal", err)
				}
				disk, err := readLedger(r.ControlRoot)
				if err != nil || !reflect.DeepEqual(current, disk) {
					t.Fatal("refused recovery wrote ledger", err)
				}
			}
		})
	}
}

func TestRecoveryReviewUnboundedPolicyReplay(t *testing.T) {
	for _, mutation := range []string{"enable", "sibling", "rates", "asset", "consumer", "native", "floor", "generation", "rollback", "foreign-policy", "precision"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, r, l := reviewManagedNative(t, "gemini")
			if mutation == "precision" {
				path := filepath.Join(r.ControlRoot, "agent-notifications.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				fields["foreignCounter"] = json.RawMessage("9007199254740992")
				if err := writeJSON(path, fields); err != nil {
					t.Fatal(err)
				}
			}
			r = reviewRevoke(t, ctx, r, "gemini")
			r.Fault = func(phase string) error {
				if phase == "transaction" {
					return fmt.Errorf("crash")
				}
				return nil
			}
			if _, err := Commit(ctx, r); err == nil {
				t.Fatal("crash missing")
			}
			tx, err := readTransactionFile(filepath.Join(r.ControlRoot, "transaction.json"))
			if err != nil {
				t.Fatal(err)
			}
			reviewDamageNative(t, l.Native.Path, "inode")
			switch mutation {
			case "enable", "sibling", "rates", "precision":
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(tx.Files[0].Data, &fields); err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "rates":
					fields["rates"] = json.RawMessage(`{"burst":1}`)
				case "precision":
					fields["foreignCounter"] = json.RawMessage("9007199254740993")
				case "enable":
					fields["route"] = json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":true},"openCodeNotifications":{"desktop":true,"webhook":true},"foreign":{"keep":true}}`)
				case "sibling":
					fields["route"] = json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false},"openCodeNotifications":{"desktop":false,"webhook":false},"foreign":{"keep":true}}`)
				}
				tx.Files[0].Data, _ = json.Marshal(fields)
			case "asset":
				tx.Files = append(tx.Files, File{Path: filepath.Join(r.RuntimeRoot, "foreign"), Data: []byte("foreign"), Mode: 0600})
			case "consumer":
				tx.After.Consumers["foreign"] = Consumer{RuntimeRoot: r.RuntimeRoot}
			case "native":
				n := *tx.After.Native
				n.DirectoryID = "1:0"
				tx.After.Native = &n
			case "floor":
				tx.After.WriterFloor = SupportedWriterFloor + 1
			case "generation":
				tx.After.Generation++
			case "rollback":
				tx.Rollback = true
			case "foreign-policy":
				if err := os.WriteFile(tx.Files[0].Path, []byte(`{"schemaVersion":1,"enabled":true,"foreign":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			policyBefore, err := Fingerprint(tx.Files[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := recoverTransaction(ctx, r.ControlRoot, l, tx, nil); err == nil {
				t.Fatal("unbounded replay accepted damaged native")
			}
			got, err := Fingerprint(tx.Files[0].Path)
			if err != nil || got != policyBefore {
				t.Fatal("refused replay changed policy", err)
			}
			disk, err := readLedger(r.ControlRoot)
			if err != nil || !reflect.DeepEqual(disk, l) {
				t.Fatal("refused replay changed ledger", err)
			}
		})
	}
}
