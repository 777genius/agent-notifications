package installruntime_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func localPolicyFixture(t *testing.T) (string, string, installruntime.Ledger) {
	t.Helper()
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, runtime := filepath.Join(base, "control"), filepath.Join(base, "runtime")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := installruntime.Commit(localTestContext(t), installruntime.Request{
		ControlRoot: root, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "sibling",
		Consumer: installruntime.Consumer{Registration: "sibling-record"},
		Files:    []installruntime.File{{Path: filepath.Join(runtime, "shared"), Data: []byte("original"), Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err = installruntime.CanonicalPath(root)
	if err != nil {
		t.Fatal(err)
	}
	// Commit canonicalizes RuntimeRoot, but the binding bytes, key and command
	// are immutable. Build them from the committed name, never a temp alias.
	return root, ledger.RuntimeRoot, ledger
}

func patchLocalPolicy(t *testing.T, root, runtime, route string) installruntime.Ledger {
	t.Helper()
	s, err := installruntime.ReadRevocationSnapshot(localTestContext(t), root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := installruntime.Commit(localTestContext(t), installruntime.Request{
		ControlRoot: root, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "sibling",
		RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(route)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// RED: replacing a selected Local leaf erases foreign nested members or manual consent.
func TestCopilotPolicyPreservesNestedConsent(t *testing.T) {
	root, runtime, _ := localPolicyFixture(t)
	patchLocalPolicy(t, root, runtime, `{"sibling":{"desktop":true},"copilotVSCodeNotifications":{"desktop":false,"webhook":true,"unknown":{"keep":7},"manual":{"enabled":true,"foreign":[1,2]}}}`)
	before, err := os.ReadFile(filepath.Join(root, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	patchLocalPolicy(t, root, runtime, `{"copilotVSCodeNotifications":{"desktop":true}}`)
	after, err := os.ReadFile(filepath.Join(root, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal(before, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	want["route"].(map[string]any)["copilotVSCodeNotifications"].(map[string]any)["desktop"] = true
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("selected toggle lost existing consent/foreign fields: want %s, got %s", before, after)
	}
}

func localTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func registeredLocalFixture(t *testing.T) (string, string, portable.Binding, installruntime.Ledger) {
	t.Helper()
	root, runtime, l := localPolicyFixture(t)
	b := portable.Binding{Version: 1, Integration: portable.CopilotVSCode, InstallationID: "TEST-installation", BindingID: "TEST-local", ScopeID: "TEST-profile", ComponentID: l.ID, Owner: l.Owner,
		ScopeRoot: filepath.Dir(root), DataRoot: filepath.Dir(root), ControlRoot: root, GlobalConfig: filepath.Join(root, "agent-notifications.json"), RuntimeRoot: runtime, Primary: "shared"}
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	l, err = installruntime.Commit(localTestContext(t), installruntime.Request{ControlRoot: root, RuntimeRoot: runtime, Owner: l.Owner, ConsumerID: key, Consumer: consumer, ExpectedGeneration: &l.Generation, PolicyEnabled: &enabled,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"sibling":{"desktop":true},"copilotVSCodeNotifications":{"desktop":true,"webhook":true,"manual":{"enabled":true,"foreign":[1,2]},"future":{"keep":7}}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if l.RuntimeRoot != b.RuntimeRoot || !reflect.DeepEqual(l.Consumers[key], consumer) {
		t.Fatalf("committed portable record differs from canonical fixture: want %+v, got %+v", consumer, l.Consumers[key])
	}
	return root, runtime, b, l
}

func treeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += link
		} else if !entry.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += string(raw)
		}
		result[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// RED: exact false cannot persist through damaged shared files/runtime, changes
// foreign ownership, or restoration/omitted repeat choices resurrect consent.
func TestCopilotRevokeSurvivesSharedDamage(t *testing.T) {
	damages := []string{"missing-shared", "replaced-shared", "missing-runtime", "symlink-runtime", "missing-native-tree", "missing-native-native-only", "missing-native-manual-only"}
	if runtime.GOOS == "windows" {
		// native_path_other.go refuses staging before any candidate is allocated.
		damages = append(damages[:4], "unsupported-native-staging")
	}
	for _, damage := range damages {
		t.Run(damage, func(t *testing.T) {
			root, runtime, b, before := registeredLocalFixture(t)
			original := filepath.Join(runtime, "shared")
			missingNative := strings.HasPrefix(damage, "missing-native-")
			if missingNative || damage == "unsupported-native-staging" {
				source := filepath.Join(filepath.Dir(root), "TEST-retained.app")
				if err := os.MkdirAll(filepath.Join(source, "Contents", "MacOS"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "Contents", "MacOS", "inert"), []byte("never executed"), 0600); err != nil {
					t.Fatal(err)
				}
				if damage == "unsupported-native-staging" {
					if err := os.Mkdir(filepath.Join(root, "native"), 0700); err != nil {
						t.Fatal(err)
					}
					untouched := treeBytes(t, filepath.Dir(root))
					candidate, err := installruntime.StageRetainedNative(localTestContext(t), root, source)
					if err == nil || err.Error() != "native bundle staging requires a supported native platform" || candidate != nil {
						t.Fatalf("unsupported native staging did not refuse: candidate=%+v err=%v", candidate, err)
					}
					if !reflect.DeepEqual(untouched, treeBytes(t, filepath.Dir(root))) {
						t.Fatal("unsupported staging changed source, ownership or runtime")
					}
					return
				}
				candidate, err := installruntime.StageRetainedNative(localTestContext(t), root, source)
				if err != nil {
					t.Fatal(err)
				}
				key, _, _, err := b.Registration()
				if err != nil {
					t.Fatal(err)
				}
				before, err = installruntime.Commit(localTestContext(t), installruntime.Request{ControlRoot: root, RuntimeRoot: runtime, Owner: b.Owner, ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &before.Generation, Native: candidate})
				if err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "missing-shared":
				if err := os.Remove(original); err != nil {
					t.Fatal(err)
				}
			case "missing-native-tree", "missing-native-native-only", "missing-native-manual-only":
				if err := os.Rename(before.Native.Path, before.Native.Path+"-retained"); err != nil {
					t.Fatal(err)
				}
			case "replaced-shared":
				if err := os.WriteFile(original, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Rename(runtime, runtime+"-retained"); err != nil {
					t.Fatal(err)
				}
				if damage == "symlink-runtime" {
					if err := os.Symlink(runtime+"-retained", runtime); err != nil {
						t.Fatal(err)
					}
				}
			}
			selection := copilotvscodeinstall.RevokeAll
			switch damage {
			case "missing-native-native-only":
				selection = copilotvscodeinstall.RevokeNative
			case "missing-native-manual-only":
				selection = copilotvscodeinstall.RevokeManual
			}
			after, err := copilotvscodeinstall.RevokeChannels(localTestContext(t), b, selection)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Consumers, after.Consumers) || !reflect.DeepEqual(before.Files, after.Files) || !reflect.DeepEqual(before.Native, after.Native) || before.Enabled != after.Enabled {
				t.Fatal("revocation altered foreign/shared ownership or enabled")
			}
			raw, err := os.ReadFile(filepath.Join(root, "agent-notifications.json"))
			if err != nil {
				t.Fatal(err)
			}
			var policy map[string]any
			if err := json.Unmarshal(raw, &policy); err != nil {
				t.Fatal(err)
			}
			route := policy["route"].(map[string]any)
			local := route["copilotVSCodeNotifications"].(map[string]any)
			if local["desktop"] != (selection == copilotvscodeinstall.RevokeManual) || local["webhook"] != local["desktop"] || local["manual"].(map[string]any)["enabled"] != (selection == copilotvscodeinstall.RevokeNative) || route["sibling"].(map[string]any)["desktop"] != true || local["future"].(map[string]any)["keep"] != float64(7) || len(local["manual"].(map[string]any)["foreign"].([]any)) != 2 {
				t.Fatal("incorrect leaf revocation/preservation")
			}
			// Restore the owned bytes/root; denial must survive repair and omitted choices.
			if missingNative {
				if err := os.Rename(before.Native.Path+"-retained", before.Native.Path); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "symlink-runtime" {
				if err := os.Remove(runtime); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "missing-runtime" || damage == "symlink-runtime" {
				if err := os.Rename(runtime+"-retained", runtime); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(original, []byte("original"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := installruntime.ReadPolicySnapshot(localTestContext(t), root)
			if err != nil {
				t.Fatal(err)
			}
			consent, err := copilotvscodeinstall.ReadConsent(snapshot, b)
			if err != nil {
				t.Fatal(err)
			}
			patch, err := copilotvscodeinstall.PolicyPatch(b, copilotvscodeinstall.Choices{}, &consent)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(patch["route"], []byte(fmt.Sprintf(`"desktop":%t`, selection == copilotvscodeinstall.RevokeManual))) || !bytes.Contains(patch["route"], []byte(fmt.Sprintf(`"enabled":%t`, selection == copilotvscodeinstall.RevokeNative))) {
				t.Fatal("omitted repeat revived consent")
			}
			gate := copilotvscodeinstall.Gate{Binding: b}
			if _, err := gate.ConsumerBinding(localTestContext(t)); err == nil {
				t.Fatal("configuration minted physical proof")
			}
		})
	}
}

// RED: malformed modes/CAS/container state reach Prepare, journal or any file
// mutation; ordinary enables bypass a damaged sibling's fingerprint check.
func TestCopilotInvalidRevokeHasNoEffects(t *testing.T) {
	root, runtime, b, _ := registeredLocalFixture(t)
	s, err := installruntime.ReadRevocationSnapshot(localTestContext(t), root)
	if err != nil {
		t.Fatal(err)
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	valid := installruntime.Request{ControlRoot: root, RuntimeRoot: runtime, Owner: b.Owner, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, RevokeCopilotVSCode: true, ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"webhook":false}}`)}}
	mutations := map[string]func(*installruntime.Request){
		"true": func(r *installruntime.Request) {
			r.PolicyFields["route"] = json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":true,"webhook":false}}`)
		},
		"null": func(r *installruntime.Request) {
			r.PolicyFields["route"] = json.RawMessage(`{"copilotVSCodeNotifications":{"manual":{"enabled":null}}}`)
		},
		"extra": func(r *installruntime.Request) {
			r.PolicyFields["route"] = json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"webhook":false,"foreign":false}}`)
		},
		"one-native-leaf": func(r *installruntime.Request) {
			r.PolicyFields["route"] = json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false}}`)
		},
		"asset": func(r *installruntime.Request) {
			r.Files = []installruntime.File{{Path: filepath.Join(runtime, "new"), Data: []byte("new"), Mode: 0600}}
		},
		"prepare": func(r *installruntime.Request) {
			r.Prepare = func() ([]installruntime.File, error) { t.Error("invalid request invoked Prepare"); return nil, nil }
		},
		"other-revoke": func(r *installruntime.Request) { r.RevokeGemini = true },
		"policy-mode":  func(r *installruntime.Request) { r.PolicyOnly = false },
		"refresh-mode": func(r *installruntime.Request) { r.RefreshOnly = false },
		"recover":      func(r *installruntime.Request) { r.RecoverOnly = true },
		"rollback":     func(r *installruntime.Request) { r.RollbackPending = true },
		"remove":       func(r *installruntime.Request) { r.RemoveConsumer = true },
		"reservation": func(r *installruntime.Request) {
			r.Reservation = &installruntime.PendingMutation{ID: "other", Owner: b.Owner, IntentRef: filepath.Join(root, "intent")}
		},
		"shared-policy":    func(r *installruntime.Request) { enabled := false; r.PolicyEnabled = &enabled },
		"stale-generation": func(r *installruntime.Request) { gen := s.Generation - 1; r.ExpectedGeneration = &gen },
		"stale-preimage":   func(r *installruntime.Request) { pre := s.Preimage; pre.SHA256 = "stale"; r.ExpectedPolicy = &pre },
		"wrong-record":     func(r *installruntime.Request) { r.Consumer.Commands = []string{filepath.Join(runtime, "different")} },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			req := valid
			req.PolicyFields = map[string]json.RawMessage{"route": valid.PolicyFields["route"]}
			change(&req)
			before := treeBytes(t, filepath.Dir(root))
			if _, err := installruntime.Commit(localTestContext(t), req); err == nil {
				t.Fatal("invalid revoke accepted")
			}
			if !reflect.DeepEqual(before, treeBytes(t, filepath.Dir(root))) {
				t.Fatal("invalid/CAS request changed filesystem")
			}
		})
	}
	if err := os.WriteFile(filepath.Join(runtime, "shared"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	ordinary := valid
	ordinary.RevokeCopilotVSCode = false
	before := treeBytes(t, filepath.Dir(root))
	if _, err := installruntime.Commit(localTestContext(t), ordinary); err == nil {
		t.Fatal("ordinary policy mutation bypassed shared damage")
	}
	if !reflect.DeepEqual(before, treeBytes(t, filepath.Dir(root))) {
		t.Fatal("ordinary refused mutation changed filesystem")
	}
}

func TestCopilotMalformedContainersAndJournalStayUntouched(t *testing.T) {
	for _, malformed := range []string{"local-null", "manual-null", "ledger", "generation", "journal"} {
		t.Run(malformed, func(t *testing.T) {
			root, _, b, _ := registeredLocalFixture(t)
			switch malformed {
			case "local-null", "manual-null":
				raw, err := os.ReadFile(filepath.Join(root, "agent-notifications.json"))
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				route := fields["route"].(map[string]any)
				if malformed == "local-null" {
					route["copilotVSCodeNotifications"] = nil
				} else {
					route["copilotVSCodeNotifications"].(map[string]any)["manual"] = nil
				}
				raw, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "agent-notifications.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			default:
				file := map[string]string{"ledger": "ownership.json", "generation": "policy-generation.json", "journal": "transaction.json"}[malformed]
				if err := os.WriteFile(filepath.Join(root, file), []byte(`{"broken":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := treeBytes(t, filepath.Dir(root))
			if _, err := copilotvscodeinstall.RevokeChannels(localTestContext(t), b, copilotvscodeinstall.RevokeNative); err == nil {
				t.Fatal("malformed state reconstructed/recovered")
			}
			if !reflect.DeepEqual(before, treeBytes(t, filepath.Dir(root))) {
				t.Fatal("malformed revoke mutated state")
			}
		})
	}
}

// RED: native/manual revoke borrows the other's consent; omitted repeats/fresh
// binding inherit unowned true intent, or a zero proof port authorizes effects.
func TestCopilotSelectionsRemainIndependent(t *testing.T) {
	for _, selection := range []copilotvscodeinstall.RevokeSelection{copilotvscodeinstall.RevokeNative, copilotvscodeinstall.RevokeManual} {
		t.Run(fmt.Sprint(selection), func(t *testing.T) {
			root, _, b, _ := registeredLocalFixture(t)
			if _, err := copilotvscodeinstall.RevokeChannels(localTestContext(t), b, selection); err != nil {
				t.Fatal(err)
			}
			snapshot, err := installruntime.ReadPolicySnapshot(localTestContext(t), root)
			if err != nil {
				t.Fatal(err)
			}
			consent, err := copilotvscodeinstall.ReadConsent(snapshot, b)
			if err != nil {
				t.Fatal(err)
			}
			patch, err := copilotvscodeinstall.PolicyPatch(b, copilotvscodeinstall.Choices{}, &consent)
			if err != nil {
				t.Fatal(err)
			}
			var route map[string]struct {
				Desktop, Webhook bool
				Manual           struct{ Enabled bool }
			}
			if err := json.Unmarshal(patch["route"], &route); err != nil {
				t.Fatal(err)
			}
			got := route["copilotVSCodeNotifications"]
			if got.Desktop != (selection == copilotvscodeinstall.RevokeManual) || got.Webhook != got.Desktop || got.Manual.Enabled != (selection == copilotvscodeinstall.RevokeNative) {
				t.Fatal("native/manual consent was coupled")
			}
			fresh, err := copilotvscodeinstall.PolicyPatch(b, copilotvscodeinstall.Choices{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(fresh["route"], []byte("true")) {
				t.Fatal("fresh omitted choices borrowed consent")
			}
			different := b
			different.BindingID = "different"
			if _, err := copilotvscodeinstall.PolicyPatch(different, copilotvscodeinstall.Choices{}, &consent); err == nil {
				t.Fatal("different binding inherited saved consent")
			}
		})
	}
}

type unavailableLocalProof struct{ calls *int }

func (p *unavailableLocalProof) CheckLocal(context.Context, portable.Binding, installruntime.InstalledSnapshot) (copilotvscodeinstall.PhysicalProof, error) {
	*p.calls++ // A typed nil represents an unavailable physical adapter.
	return copilotvscodeinstall.PhysicalProof{}, nil
}

func TestCopilotRecordedConsentCannotMintPhysicalProof(t *testing.T) {
	root, _, b, l := registeredLocalFixture(t)
	s, err := installruntime.ReadInstalledSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CheckSnapshot(s); err != nil {
		t.Fatalf("valid registration prerequisite: %v", err)
	}
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	binding := copilotvscodeevent.Binding{InstallationID: b.InstallationID, BindingID: key, ProfileIdentity: b.ScopeID, Product: "copilot-vscode", Generation: l.Generation}
	calls := 0
	zero := &unavailableLocalProof{calls: &calls}
	var typedNil *unavailableLocalProof
	for _, proof := range []copilotvscodeinstall.ProofPort{nil, zero, typedNil} {
		gate := copilotvscodeinstall.Gate{Binding: b, Proof: proof}
		if channels := gate.Channels(localTestContext(t), binding); channels.Desktop || channels.Webhook {
			t.Fatal("recorded/configured true consent minted physical authority")
		}
		if gate.Recheck(localTestContext(t), binding, copilotvscodeevent.DesktopChannel) || gate.Recheck(localTestContext(t), binding, copilotvscodeevent.WebhookChannel) {
			t.Fatal("effect recheck granted missing/unqualified proof")
		}
		if _, err := gate.ConsumerBinding(localTestContext(t)); err == nil {
			t.Fatal("missing/unqualified physical proof authorized N1 binding")
		}
	}
	if calls == 0 {
		t.Fatal("zero physical proof never reached the authority boundary")
	}
}

// RED: an admitted false-only journal must survive equivalent control-root
// spellings despite damaged native assets, while a foreign binding still refuses.
func TestCopilotRevocationRecoveryControlRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native retained staging requires a supported native platform")
	}
	for _, name := range []string{"trailing-slash", "darwin-alias", "wrong-control"} {
		t.Run(name, func(t *testing.T) {
			root, runtimeRoot, b, before := registeredLocalFixture(t)
			recoveryRoot := root + string(filepath.Separator)
			if name == "darwin-alias" {
				if runtime.GOOS != "darwin" || !strings.HasPrefix(root, "/private/var/") {
					t.Skip("fixture has no supported Darwin /var alias")
				}
				recoveryRoot = strings.TrimPrefix(root, "/private")
			}
			source := filepath.Join(filepath.Dir(root), "TEST-retained.app")
			if err := os.MkdirAll(filepath.Join(source, "Contents", "MacOS"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "Contents", "MacOS", "inert"), []byte("never executed"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate, err := installruntime.StageRetainedNative(localTestContext(t), root, source)
			if err != nil {
				t.Fatal(err)
			}
			key, consumer, _, err := b.Registration()
			if err != nil {
				t.Fatal(err)
			}
			before, err = installruntime.Commit(localTestContext(t), installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: b.Owner, ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &before.Generation, Native: candidate})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(before.Native.Path, before.Native.Path+"-retained"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := installruntime.ReadRevocationSnapshot(localTestContext(t), root)
			if err != nil {
				t.Fatal(err)
			}
			crash := fmt.Errorf("transaction crash")
			revoke := installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: b.Owner, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, RevokeCopilotVSCode: true, ExpectedGeneration: &snapshot.Generation, ExpectedPolicy: &snapshot.Preimage,
				PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"webhook":false}}`)},
				Fault: func(phase string) error {
					if phase == "transaction" {
						return crash
					}
					return nil
				}}
			if name != "wrong-control" {
				alternate := revoke
				alternate.ControlRoot, alternate.Fault = recoveryRoot, nil
				untouched := treeBytes(t, filepath.Dir(root))
				if _, err := installruntime.Commit(localTestContext(t), alternate); err == nil {
					t.Fatal("initial revoke admitted a different binding spelling")
				}
				if !reflect.DeepEqual(untouched, treeBytes(t, filepath.Dir(root))) {
					t.Fatal("refused initial revoke changed control state")
				}
			}
			_, err = installruntime.Commit(localTestContext(t), revoke)
			if err != crash {
				t.Fatalf("expected durable transaction interruption, got %v", err)
			}
			marker := filepath.Join(root, "transaction.json")
			if name == "wrong-control" {
				// Keep registration, journal and live ledger mutually consistent:
				// only the binding's control identity is foreign.
				b.ControlRoot = filepath.Join(filepath.Dir(root), "TEST-other-control")
				b.GlobalConfig = filepath.Join(b.ControlRoot, "agent-notifications.json")
				wrongKey, wrongConsumer, _, err := b.Registration()
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					SHA256      string
					Transaction json.RawMessage
				}
				data, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &envelope); err != nil {
					t.Fatal(err)
				}
				var journal map[string]json.RawMessage
				if err := json.Unmarshal(envelope.Transaction, &journal); err != nil {
					t.Fatal(err)
				}
				for _, state := range []string{"Before", "After"} {
					var ledger installruntime.Ledger
					if err := json.Unmarshal(journal[state], &ledger); err != nil {
						t.Fatal(err)
					}
					delete(ledger.Consumers, key)
					ledger.Consumers[wrongKey] = wrongConsumer
					journal[state], err = json.Marshal(ledger)
					if err != nil {
						t.Fatal(err)
					}
					if state == "Before" {
						if err := os.WriteFile(filepath.Join(root, "ownership.json"), journal[state], 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				envelope.Transaction, err = json.Marshal(journal)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(envelope.Transaction)
				envelope.SHA256 = hex.EncodeToString(sum[:])
				data, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, data, 0600); err != nil {
					t.Fatal(err)
				}
				untouched := treeBytes(t, filepath.Dir(root))
				if _, err := installruntime.Commit(localTestContext(t), installruntime.Request{ControlRoot: recoveryRoot, RecoverOnly: true}); err == nil {
					t.Fatal("recovery accepted a foreign control binding")
				}
				if !reflect.DeepEqual(untouched, treeBytes(t, filepath.Dir(root))) {
					t.Fatal("refused recovery changed control state")
				}
				return
			}
			after, err := installruntime.Commit(localTestContext(t), installruntime.Request{ControlRoot: recoveryRoot, RecoverOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Native, after.Native) {
				t.Fatal("recovery changed retained native record")
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatalf("recovery retained journal: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(root, "agent-notifications.json"))
			if err != nil {
				t.Fatal(err)
			}
			var policy struct {
				Enabled bool
				Route   struct {
					Local struct {
						Desktop, Webhook bool
						Manual           struct{ Enabled bool }
					} `json:"copilotVSCodeNotifications"`
				}
			}
			if err := json.Unmarshal(data, &policy); err != nil {
				t.Fatal(err)
			}
			if !policy.Enabled || policy.Route.Local.Desktop || policy.Route.Local.Webhook || !policy.Route.Local.Manual.Enabled {
				t.Fatal("recovery failed to persist only native false policy")
			}
		})
	}
}
