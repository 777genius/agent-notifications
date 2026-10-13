package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/codexcommand"
)

func (f *orphanFixture) recoveryRequest() OrphanRecoveryRequest {
	return OrphanRecoveryRequest{InstallationID: f.before.ID, ExpectedGeneration: f.before.Generation, ConsumerID: f.consumerID, RuntimeRoot: f.runtime, Consumer: f.before.Consumers[f.consumerID]}
}

func assertOrphanAbsent(t *testing.T, f *orphanFixture) {
	t.Helper()
	for _, path := range append(append([]string(nil), f.orphanPaths...), f.registration) {
		id, err := Fingerprint(path)
		if err != nil || id.Exists {
			t.Errorf("recovery created absent payload %s: %v", path, err)
		}
	}
}

func assertOrphanDelta(t *testing.T, f *orphanFixture, got Ledger, rollback bool) {
	t.Helper()
	want := cloneOrphanLedger(f.before)
	if rollback {
		want.Generation += 2
		want.PolicyGeneration += 2
	} else {
		want.Generation++
		want.PolicyGeneration++
		delete(want.Consumers, f.consumerID)
		for _, path := range f.orphanPaths {
			delete(want.Files, path)
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("unexpected ownership delta\nwant %+v\ngot %+v", want, got)
	}
	for path, id := range f.before.Files {
		if !pathWithinRoot(f.runtime, path) {
			now, err := Fingerprint(path)
			if err != nil || now != id {
				t.Errorf("retained bytes/mode changed %s: %v", path, err)
			}
		}
	}
	assertOrphanAbsent(t, f)
}

func TestOrphanRecoveryExactCodex21Of49AndNoRecreation(t *testing.T) {
	for _, removeRoot := range []bool{true, false} {
		t.Run(fmt.Sprintf("missing-root=%t", removeRoot), func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, removeRoot)
			untracked := filepath.Join(f.runtime, "operator-untracked")
			if !removeRoot {
				if err := os.WriteFile(untracked, []byte("retain untracked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			images := orphanControlImages(t, f.control)
			req := f.recoveryRequest()
			preview, err := PreviewOrphanConsumer(f.control, req)
			if err != nil || !preview.Admissible || preview.SelectedCount != 21 || !reflect.DeepEqual(preview.SelectedPaths, f.orphanPaths) || !preview.NativeValidated {
				t.Fatalf("incident-shaped preview: %+v %v", preview, err)
			}
			assertOrphanImages(t, images)
			if len(f.before.Files) != 49 || len(req.Consumer.Commands) != 8 || req.Consumer.Registration != f.registration {
				t.Fatal("fixture lost actual Codex shape/counts")
			}
			got, err := RecoverOrphanConsumer(f.ctx, f.control, req)
			if err != nil {
				t.Fatal(err)
			}
			assertOrphanDelta(t, f, got, false)
			if _, err := os.Lstat(filepath.Join(f.control, "transaction.json")); !os.IsNotExist(err) {
				t.Fatal("marker remains", err)
			}
			policy, _ := os.ReadFile(filepath.Join(f.control, "agent-notifications.json"))
			if !bytes.Equal(policy, images[filepath.Join(f.control, "agent-notifications.json")]) {
				t.Fatal("raw policy bytes changed")
			}
			s, err := ReadInstalledSnapshot(f.control)
			if err != nil || !s.Enabled || s.Recovery || len(s.Ledger.Files) != 28 {
				t.Fatalf("recovered installation not healthy/enabled: %+v %v", s, err)
			}
			if removeRoot {
				if _, err := os.Lstat(f.runtime); !os.IsNotExist(err) {
					t.Fatal("missing runtime recreated", err)
				}
			} else {
				data, err := os.ReadFile(untracked)
				if err != nil || string(data) != "retain untracked" {
					t.Fatal("untracked payload changed", err)
				}
			}
			completed := orphanControlImages(t, f.control)
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, req); err == nil {
				t.Fatal("stale repeated request accepted")
			}
			assertOrphanImages(t, completed)
		})
	}
}

func TestOrphanRecoveryRejectsUnsafeAdmissionBeforeJournal(t *testing.T) {
	tests := map[string]func(*testing.T, *orphanFixture, *OrphanRecoveryRequest){
		"surviving-payload": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			orphanWrite(t, f.orphanPaths[0], "original surviving payload")
		},
		"modified-payload": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			orphanWrite(t, f.orphanPaths[1], "foreign modified payload")
		},
		"registration-present": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			orphanWrite(t, f.registration, "surviving sibling registration")
		},
		"retained-missing": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			if err := os.Remove(filepath.Join(f.primary, "asset-00")); err != nil {
				t.Fatal(err)
			}
		},
		"retained-modified": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			orphanWrite(t, filepath.Join(f.primary, "asset-00"), "foreign retained edit")
		},
		"wrong-installation": func(_ *testing.T, _ *orphanFixture, r *OrphanRecoveryRequest) {
			r.InstallationID = "another installation"
		},
		"wrong-generation": func(_ *testing.T, _ *orphanFixture, r *OrphanRecoveryRequest) { r.ExpectedGeneration++ },
		"wrong-consumer":   func(_ *testing.T, _ *orphanFixture, r *OrphanRecoveryRequest) { r.ConsumerID = "other" },
		"wrong-root": func(_ *testing.T, f *orphanFixture, r *OrphanRecoveryRequest) {
			r.RuntimeRoot = filepath.Join(f.root, "other")
			r.Consumer.RuntimeRoot = r.RuntimeRoot
		},
		"observed-record-changed": func(_ *testing.T, _ *orphanFixture, r *OrphanRecoveryRequest) { r.Consumer.Registration = "" },
		"owner-mismatch": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			f.before.Owner = "different installer"
			f.saveLedger(t)
		},
		"final-consumer": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			delete(f.before.Consumers, "retained")
			f.saveLedger(t)
		},
		"primary-root": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			f.before.RuntimeRoot = f.runtime
			f.saveLedger(t)
		},
		"ancestor-runtime": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			c := f.before.Consumers["retained"]
			c.RuntimeRoot = filepath.Dir(f.runtime)
			f.before.Consumers["retained"] = c
			f.saveLedger(t)
		},
		"nested-runtime": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			c := f.before.Consumers["retained"]
			c.RuntimeRoot = filepath.Join(f.runtime, "nested")
			f.before.Consumers["retained"] = c
			f.saveLedger(t)
		},
		"shared-registration": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			c := f.before.Consumers["retained"]
			c.Registration = f.registration
			f.before.Consumers["retained"] = c
			f.saveLedger(t)
		},
		"shared-command": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			c := f.before.Consumers["retained"]
			c.Commands = []string{f.orphanPaths[0]}
			f.before.Consumers["retained"] = c
			f.saveLedger(t)
		},
		"opaque-retained-registration": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			c := f.before.Consumers["retained"]
			c.Registration = `{"private":"binding"}`
			f.before.Consumers["retained"] = c
			f.saveLedger(t)
		},
		"external-registration-tracked": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			f.before.Files[f.registration] = identity([]byte("tracked external"), 0600)
			f.saveLedger(t)
		},
		"unsupported-registration": func(t *testing.T, f *orphanFixture, r *OrphanRecoveryRequest) {
			r.Consumer.Registration = `{"private":"binding"}`
			f.before.Consumers[r.ConsumerID] = r.Consumer
			f.saveLedger(t)
		},
		"private-selected": func(t *testing.T, f *orphanFixture, r *OrphanRecoveryRequest) {
			r.Consumer.OpenCode = &OpenCodeRegistration{}
			f.before.Consumers[r.ConsumerID] = r.Consumer
			f.saveLedger(t)
		},
		"identity-empty": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			f.before.Files[f.orphanPaths[0]] = Identity{}
			f.saveLedger(t)
		},
		"identity-malformed-hash": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			f.before.Files[f.orphanPaths[0]] = Identity{Exists: true, SHA256: "arbitrary", Mode: 0644}
			f.saveLedger(t)
		},
		"pending-reservation": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			f.before.PendingMutation = &PendingMutation{ID: "test", Owner: "existing-installer", IntentRef: filepath.Join(f.control, "intent")}
			f.saveLedger(t)
		},
		"pending-unrelated-journal": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			orphanWrite(t, filepath.Join(f.control, "transaction.json"), "unrelated marker must stay")
		},
		"policy-ledger-mismatch": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			if err := writeJSON(filepath.Join(f.control, "policy-generation.json"), runtimePolicy{f.before.PolicyGeneration + 1, true}); err != nil {
				t.Fatal(err)
			}
		},
		"missing-component-lock": func(t *testing.T, f *orphanFixture, _ *OrphanRecoveryRequest) {
			if err := os.Remove(filepath.Join(f.control, ".component-install.lock")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, false)
			req := f.recoveryRequest()
			mutate(t, f, &req)
			images := orphanControlImages(t, f.control)
			if _, err := PreviewOrphanConsumer(f.control, req); err == nil {
				t.Fatal("unsafe preview admitted")
			}
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, req); err == nil {
				t.Fatal("unsafe commit admitted")
			}
			assertOrphanImages(t, images)
		})
	}
}

func orphanWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *orphanFixture) saveLedger(t *testing.T) {
	t.Helper()
	if err := writeJSON(filepath.Join(f.control, "ownership.json"), f.before); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanCodexCodecRejectsAlteredOpaqueCommands(t *testing.T) {
	for _, damage := range []string{"event", "executable", "trailing-shell", "missing-command", "reordered", "wrong-id", "wrong-sibling"} {
		t.Run(damage, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, false)
			req := f.recoveryRequest()
			req.Consumer.Commands = append([]string(nil), req.Consumer.Commands...)
			switch damage {
			case "event":
				req.Consumer.Commands[0] = strings.Replace(req.Consumer.Commands[0], "PreToolUse", "UnknownEvent", 1)
			case "executable":
				req.Consumer.Commands[0] = strings.Replace(req.Consumer.Commands[0], "sh ", "evil ", 1)
			case "trailing-shell":
				req.Consumer.Commands[0] += "; touch /outside"
			case "missing-command":
				req.Consumer.Commands = req.Consumer.Commands[:7]
			case "reordered":
				req.Consumer.Commands[0], req.Consumer.Commands[1] = req.Consumer.Commands[1], req.Consumer.Commands[0]
			case "wrong-id":
				req.ConsumerID = "codex:wrong"
				delete(f.before.Consumers, f.consumerID)
			case "wrong-sibling":
				req.Consumer.Registration = filepath.Join(f.root, "elsewhere", "hooks.json")
			}
			f.before.Consumers[req.ConsumerID] = req.Consumer
			f.saveLedger(t)
			images := orphanControlImages(t, f.control)
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, req); err == nil {
				t.Fatal("unrecognized opaque command codec admitted")
			}
			assertOrphanImages(t, images)
		})
	}
}

func TestOrphanPlainConsumerAndComponentAwarePrefix(t *testing.T) {
	f := newOrphanFixture(t)
	plain := f.before.Consumers[f.consumerID]
	plain.Registration = ""
	plain.Commands = []string{f.orphanPaths[0]}
	delete(f.before.Consumers, f.consumerID)
	f.consumerID = "plain-installer"
	f.before.Consumers[f.consumerID] = plain
	// A shared string prefix does not grant ownership of the adjacent runtime.
	prefix := f.runtime + "-retained"
	path := filepath.Join(prefix, "asset")
	orphanWrite(t, path, "adjacent bytes")
	id, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	f.before.Files[path] = id
	f.before.Consumers["adjacent"] = Consumer{RuntimeRoot: prefix, Commands: []string{path}}
	f.saveLedger(t)
	f.abandon(t, true)
	got, err := RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest())
	if err != nil {
		t.Fatal(err)
	}
	assertOrphanDelta(t, f, got, false)
}

func TestOrphanPreviewAbsentControlCreatesNothing(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	missing := filepath.Join(f.root, "missing-control")
	if _, err := PreviewOrphanConsumer(missing, f.recoveryRequest()); err == nil {
		t.Fatal("missing control admitted")
	}
	if _, err := RecoverOrphanConsumer(f.ctx, missing, f.recoveryRequest()); err == nil {
		t.Fatal("missing control commit admitted")
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("preview/commit created fresh control", err)
	}
}

func TestOrphanCommitUsesComponentAndPolicyLocksAndPolicyCAS(t *testing.T) {
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		t.Run(name, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, true)
			req := f.recoveryRequest()
			unlock, err := LockExisting(f.ctx, filepath.Join(f.control, name))
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			ctx, cancel := context.WithTimeout(f.ctx, 40*time.Millisecond)
			defer cancel()
			images := orphanControlImages(t, f.control)
			// Preview must finish even while a mutation lock is held.
			if _, err := PreviewOrphanConsumer(f.control, req); err != nil {
				t.Fatal("preview acquired lock", err)
			}
			if _, err := RecoverOrphanConsumer(ctx, f.control, req); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("commit did not serialize on real lock: %v", err)
			}
			assertOrphanImages(t, images)
		})
	}
	f := newOrphanFixture(t)
	f.abandon(t, true)
	req := f.recoveryRequest()
	p, err := PreviewOrphanConsumer(f.control, req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.control, "agent-notifications.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, ' ')
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	images := orphanControlImages(t, f.control)
	if _, err := Commit(f.ctx, Request{ControlRoot: f.control, OrphanRecovery: &req, ExpectedPolicy: &p.ExpectedPolicy}); !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("stale raw policy CAS accepted: %v", err)
	}
	assertOrphanImages(t, images)
}

func TestOrphanRecoveryCannotCombineMutationModes(t *testing.T) {
	mutations := map[string]func(*Request){
		"files":   func(r *Request) { r.Files = []File{{Path: "/arbitrary", Remove: true}} },
		"native":  func(r *Request) { r.Native = &NativeChange{} },
		"prepare": func(r *Request) { r.Prepare = func() ([]File, error) { return nil, nil } },
		"recover": func(r *Request) { r.RecoverOnly = true }, "rollback": func(r *Request) { r.RollbackPending = true },
		"policy": func(r *Request) { v := false; r.PolicyEnabled = &v }, "remove": func(r *Request) { r.RemoveConsumer = true },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, true)
			req := f.recoveryRequest()
			r := Request{ControlRoot: f.control, OrphanRecovery: &req}
			mutate(&r)
			images := orphanControlImages(t, f.control)
			if _, err := Commit(f.ctx, r); err == nil {
				t.Fatal("combined mutation admitted")
			}
			assertOrphanImages(t, images)
		})
	}
}

func TestOrphanKnownWrapperAbsenceRequiredOutsideTrackedSet(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, false)
	wrapper := filepath.Join(f.runtime, "bin", "codex-hook-wrapper.cmd")
	delete(f.before.Files, wrapper)
	f.saveLedger(t)
	orphanWrite(t, wrapper, "untracked surviving known wrapper")
	images := orphanControlImages(t, f.control)
	if _, err := RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest()); err == nil {
		t.Fatal("surviving known generated wrapper admitted")
	}
	assertOrphanImages(t, images)
	if !reflect.DeepEqual(f.before.Consumers[f.consumerID].Commands, codexcommand.Commands(f.runtime)) {
		t.Fatal("fixture codec mismatch")
	}
}

func TestOrphanAdmissionCannotDiscardUnknownRecordedOwnership(t *testing.T) {
	for _, where := range []string{"ledger", "retained-consumer", "selected-consumer"} {
		t.Run(where, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.abandon(t, true)
			path := filepath.Join(f.control, "ownership.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if where == "ledger" {
				fields["UnknownManagedState"] = true
			} else {
				id := "retained"
				if where == "selected-consumer" {
					id = f.consumerID
				}
				fields["Consumers"].(map[string]any)[id].(map[string]any)["ExternalManagedSkill"] = true
			}
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			images := orphanControlImages(t, f.control)
			if _, err := PreviewOrphanConsumer(f.control, f.recoveryRequest()); err == nil {
				t.Fatal("preview silently discarded unknown ownership")
			}
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest()); err == nil {
				t.Fatal("cleanup silently discarded unknown ownership")
			}
			assertOrphanImages(t, images)
		})
	}
}

func TestOrphanMissingRegistrationAncestorsAreAbsenceWithoutRecreation(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	parent := filepath.Dir(f.runtime)
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	req := f.recoveryRequest()
	p, err := PreviewOrphanConsumer(f.control, req)
	if err != nil || !p.Admissible {
		t.Fatalf("missing registration ancestors refused: %+v %v", p, err)
	}
	got, err := RecoverOrphanConsumer(f.ctx, f.control, req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrphanDelta(t, f, got, false)
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Fatal("recovery recreated first absent ancestor", err)
	}
}
