package installruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Channel revocation must not strand a journal when policy drift requires
// native validation. A refused request must leave bounded global disable usable.
func TestPolicyRevocationPreflight(t *testing.T) {
	for _, channel := range []string{"gemini", "opencode"} {
		for _, drift := range []string{"manual-enable", "deleted-policy", "none"} {
			for _, damage := range []string{"bytes", "missing", "healthy"} {
				t.Run(channel+"/"+drift+"/"+damage, func(t *testing.T) {
					ctx, r, before := replayContentFixture(t, true, false)
					// A missing policy means disabled: start enabled to create drift.
					enabled := drift != "manual-enable"
					r.PolicyEnabled, r.ExpectedGeneration = &enabled, &before.Generation
					before, err := Commit(ctx, r)
					if err != nil {
						t.Fatal(err)
					}
					r.PolicyEnabled = nil
					policyPath := filepath.Join(r.ControlRoot, "agent-notifications.json")
					switch drift {
					case "manual-enable":
						data, err := os.ReadFile(policyPath)
						if err != nil {
							t.Fatal(err)
						}
						var fields map[string]json.RawMessage
						if err := json.Unmarshal(data, &fields); err != nil {
							t.Fatal(err)
						}
						fields["enabled"] = json.RawMessage("true")
						data, err = json.Marshal(fields)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(policyPath, data, 0600); err != nil {
							t.Fatal(err)
						}
					case "deleted-policy":
						if err := os.Remove(policyPath); err != nil {
							t.Fatal(err)
						}
					}
					policy, err := ReadUserPolicy(r.ControlRoot)
					if err != nil || (policy.Enabled != before.Enabled) != (drift != "none") {
						t.Fatal("fixture did not establish the intended policy drift", err)
					}
					if damage != "healthy" {
						replayContentDamage(t, before.Native, damage)
					}
					snapshot, err := ReadRevocationSnapshot(ctx, r.ControlRoot)
					if err != nil {
						t.Fatal(err)
					}
					r.ExpectedGeneration, r.ExpectedPolicy = &snapshot.Generation, &snapshot.Preimage
					r.ConsumerID = channel + "-notifications"
					r.RevokeGemini, r.RevokeOpenCode = channel == "gemini", channel == "opencode"
					policyChannel := "geminiNotifications"
					if channel == "opencode" {
						policyChannel = "openCodeNotifications"
					}
					patch, err := json.Marshal(map[string]any{policyChannel: map[string]bool{"desktop": false, "webhook": false}})
					if err != nil {
						t.Fatal(err)
					}
					r.PolicyFields = map[string]json.RawMessage{"route": patch}
					images := map[string][]byte{}
					for _, name := range []string{filepath.Join(r.ControlRoot, "ownership.json"), filepath.Join(r.ControlRoot, "policy-generation.json"), filepath.Join(r.ControlRoot, "receipt.json"), filepath.Join(r.RuntimeRoot, "config.json")} {
						data, err := os.ReadFile(name)
						if err != nil {
							t.Fatal(err)
						}
						images[name] = data
					}
					if snapshot.Preimage.Exists {
						data, err := os.ReadFile(policyPath)
						if err != nil {
							t.Fatal(err)
						}
						images[policyPath] = data
					}
					after, revokeErr := Commit(ctx, r)
					refuse := drift != "none" && damage != "healthy"
					if refuse {
						if revokeErr == nil {
							t.Error("accepted channel revocation with policy drift and damaged native")
						}
						for path, data := range images {
							got, err := os.ReadFile(path)
							if err != nil || !bytes.Equal(got, data) {
								t.Errorf("refusal mutated %s: %v", path, err)
							}
						}
						gotPolicy, err := Fingerprint(policyPath)
						if err != nil || gotPolicy != snapshot.Preimage {
							t.Error("refusal changed policy bytes or existence", err)
						}
					} else {
						if revokeErr != nil {
							t.Fatal("healthy drift or bounded damaged-native control refused", revokeErr)
						}
						if after.Enabled != policy.Enabled || after.Generation != before.Generation+1 || !reflect.DeepEqual(after.Native, before.Native) {
							t.Fatal("channel revocation changed intent, generation or native ownership")
						}
						data, err := os.ReadFile(policyPath)
						if err != nil {
							t.Fatal(err)
						}
						var published struct {
							Route map[string]map[string]json.RawMessage `json:"route"`
						}
						if err := json.Unmarshal(data, &published); err != nil || string(published.Route[policyChannel]["desktop"]) != "false" || string(published.Route[policyChannel]["webhook"]) != "false" {
							t.Fatal("channel revocation did not publish both false fields", err)
						}
					}
					marker := filepath.Join(r.ControlRoot, "transaction.json")
					if _, err := os.Lstat(marker); !os.IsNotExist(err) {
						t.Errorf("channel revocation stranded a journal: %v (commit: %v)", err, revokeErr)
					}
					if !refuse {
						return
					}
					// Retry the real policy-only global disable without repairing native
					// or manually deleting a leaked marker. Old Commit returns ErrPolicyRecovery.
					off := false
					r.RevokeGemini, r.RevokeOpenCode = false, false
					r.PolicyFields, r.PolicyEnabled = nil, &off
					after, err = Commit(ctx, r)
					if err != nil {
						t.Fatalf("refused channel request blocked bounded global disable: %v", err)
					}
					if after.Enabled || after.Generation != before.Generation+1 || after.PolicyGeneration != before.PolicyGeneration+1 || !reflect.DeepEqual(after.Native, before.Native) {
						t.Fatal("global disable changed native ownership or did not publish one disabled generation")
					}
					policy, err = ReadUserPolicy(r.ControlRoot)
					if err != nil || policy.Enabled {
						t.Fatal("global disable did not publish disabled policy", err)
					}
					for _, path := range []string{filepath.Join(r.ControlRoot, "receipt.json"), filepath.Join(r.RuntimeRoot, "config.json")} {
						got, err := os.ReadFile(path)
						if err != nil || !bytes.Equal(got, images[path]) {
							t.Fatal("global disable changed unrelated assets", err)
						}
					}
					if _, err := os.Lstat(marker); !os.IsNotExist(err) {
						t.Fatal("global disable retained journal", err)
					}
				})
			}
		}
	}
}

// Regression: even a fresh CAS must not journal unrelated global intent drift
// through the Cursor exception, including when every delivery asset is healthy.
func TestCursorPolicyRevocationPreflight(t *testing.T) {
	for _, drift := range []string{"enabled", "disabled-already-false", "deleted", "route-container", "cursor-container", "component", "control"} {
		t.Run(drift, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			root := filepath.Join(t.TempDir(), "TEST-control")
			runtime := filepath.Join(filepath.Dir(root), "TEST-runtime")
			enabled := drift == "deleted" || drift == "disabled-already-false"
			l, err := Commit(ctx, Request{ControlRoot: root, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "sibling", Consumer: Consumer{Registration: "unrelated"}, Files: []File{{Path: filepath.Join(runtime, "shared"), Data: []byte("inert"), Mode: 0700}}})
			if err != nil {
				t.Fatal(err)
			}
			binding := map[string]any{"version": 1, "integration": "cursor", "installationID": "TEST-install", "bindingID": "TEST-cursor", "scopeID": "TEST-profile", "componentID": l.ID, "owner": l.Owner, "scopeRoot": filepath.Dir(root), "dataRoot": filepath.Dir(root), "controlRoot": root, "globalConfig": filepath.Join(root, "agent-notifications.json"), "runtimeRoot": runtime, "primary": "shared"}
			if drift == "component" {
				binding["componentID"] = "foreign"
			}
			if drift == "control" {
				binding["controlRoot"] = filepath.Join(root, "foreign")
			}
			raw, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			key := "portable:" + hex.EncodeToString(sum[:])
			c := Consumer{RuntimeRoot: runtime, Registration: string(raw), Commands: []string{filepath.Join(runtime, "shared")}}
			l, err = Commit(ctx, Request{ControlRoot: root, RuntimeRoot: runtime, Owner: l.Owner, ConsumerID: key, Consumer: c, ExpectedGeneration: &l.Generation, PolicyEnabled: &enabled, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"cursorNotifications":{"desktop":false,"webhook":false}}`)}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "agent-notifications.json")
			data := []byte(`{"schemaVersion":1,"enabled":true}`)
			switch drift {
			case "component", "control", "disabled-already-false":
				data = []byte(`{"schemaVersion":1,"enabled":false,"route":{"cursorNotifications":{"desktop":false,"webhook":false}}}`)
			case "deleted":
				err = os.Remove(path)
			case "route-container":
				data = []byte(`{"schemaVersion":1,"enabled":false,"route":null}`)
			case "cursor-container":
				data = []byte(`{"schemaVersion":1,"enabled":false,"route":{"cursorNotifications":null}}`)
			}
			if drift != "deleted" {
				err = os.WriteFile(path, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := ReadRevocationSnapshot(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, name := range []string{"ownership.json", "policy-generation.json", "agent-notifications.json"} {
				raw, err := os.ReadFile(filepath.Join(root, name))
				if err != nil && !(drift == "deleted" && os.IsNotExist(err)) {
					t.Fatal(err)
				}
				before[name] = raw
			}
			_, err = Commit(ctx, Request{ControlRoot: root, RuntimeRoot: runtime, Owner: l.Owner, ConsumerID: key, Consumer: c, RevokeCursor: true, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"cursorNotifications":{"desktop":false,"webhook":false}}`)}})
			if err == nil {
				t.Fatal("Cursor admitted unrelated global/container drift")
			}
			for name, raw := range before {
				got, err := os.ReadFile(filepath.Join(root, name))
				if drift == "deleted" && name == "agent-notifications.json" && os.IsNotExist(err) {
					continue
				}
				if err != nil || !bytes.Equal(raw, got) {
					t.Fatal("refusal changed durable state", name, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(root, "transaction.json")); !os.IsNotExist(err) {
				t.Fatal("refusal stranded journal", err)
			}
		})
	}
}
