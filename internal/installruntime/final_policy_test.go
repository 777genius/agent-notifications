package installruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPolicyFieldsCannotRebaseOntoUnobservedEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent-notifications.json")
	original := []byte(`{"schemaVersion":1,"enabled":false,"route":"original"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	_, fields, err := readUserPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	edit := []byte(`{"schemaVersion":1,"enabled":false,"route":"manual edit"}`)
	if err = os.WriteFile(path, edit, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := policyFile(root, true, fields, before)
	if err == nil {
		err = safePublish(file, true)
	}
	if err == nil {
		t.Fatal("stale policy fields rebased onto a newer preimage")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(edit) {
		t.Fatal("manual policy edit lost", err)
	}
}

func TestPolicyRecoveryPreservesForeignEdit(t *testing.T) {
	ctx, r := request(t)
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.PolicyEnabled, r.ExpectedGeneration = &enabled, &ledger.Generation
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	edit := []byte(`{"schemaVersion":1,"enabled":false,"route":"foreign"}`)
	if err = os.WriteFile(path, edit, 0600); err != nil {
		t.Fatal(err)
	}
	r.Fault, r.PolicyEnabled, r.ExpectedGeneration = nil, nil, nil
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("recovery overwrote policy edit")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(edit) {
		t.Fatal("foreign policy lost", err)
	}
}

// Prediction must agree with the real writer, including formatting and modes;
// an encoding mirror would not detect a divergence in Commit.
func TestPredictedPolicyIdentityMatchesCommittedBytes(t *testing.T) {
	for _, data := range []string{"", `{"schemaVersion":1,"enabled":true,"foreign":{"large":9007199254740993},"route":{"sibling":{"enabled":false},"cursorNotifications":{"desktop":true,"future":{"keep":1}}}}`} {
		t.Run(fmt.Sprintf("existing=%t", data != ""), func(t *testing.T) {
			ctx, r := request(t)
			ledger, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.ControlRoot, "agent-notifications.json")
			if data != "" {
				if err := os.WriteFile(path, []byte(data), 0640); err != nil {
					t.Fatal(err)
				}
			}
			before, err := Fingerprint(path)
			if err != nil {
				t.Fatal(err)
			}
			patch := map[string]json.RawMessage{"route": json.RawMessage(`{"cursorNotifications":{"desktop":false,"webhook":false}}`)}
			frozen := string(patch["route"])
			unchanged, err := PredictPolicyIdentity(r.ControlRoot, before, nil)
			if err != nil || unchanged != before {
				t.Fatalf("empty prediction: %+v %v", unchanged, err)
			}
			after, err := PredictPolicyIdentity(r.ControlRoot, before, patch)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := Fingerprint(path)
			if err != nil || actual != before || string(patch["route"]) != frozen {
				t.Fatal("prediction mutated inputs", err)
			}
			r.ExpectedGeneration, r.ExpectedPolicy, r.PolicyFields = &ledger.Generation, &before, patch
			committed, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			actual, err = Fingerprint(path)
			if err != nil || actual != after || committed.Generation != ledger.Generation+1 {
				t.Fatalf("predicted=%+v actual=%+v err=%v", after, actual, err)
			}
			policy, fields, err := readUserPolicy(r.ControlRoot)
			if err != nil || policy.Enabled != (data != "") {
				t.Fatal("shared intent changed", err)
			}
			if data != "" {
				compact := func(raw json.RawMessage) string {
					var b bytes.Buffer
					if err := json.Compact(&b, raw); err != nil {
						t.Fatal(err)
					}
					return b.String()
				}
				var route map[string]json.RawMessage
				var cursor map[string]json.RawMessage
				if json.Unmarshal(fields["route"], &route) != nil || json.Unmarshal(route["cursorNotifications"], &cursor) != nil || compact(cursor["future"]) != `{"keep":1}` || compact(route["sibling"]) != `{"enabled":false}` || compact(fields["foreign"]) != `{"large":9007199254740993}` {
					t.Fatal("foreign nested members lost", fields)
				}
			}
			// A manual opt-out after our owned false commit cannot become a baseline
			// for the next coordinated CAS, even with the same ledger generation.
			edit := []byte(`{"schemaVersion":1,"enabled":false,"manual":"opt-out"}`)
			if err := os.WriteFile(path, edit, 0600); err != nil {
				t.Fatal(err)
			}
			r.ExpectedGeneration, r.ExpectedPolicy = &committed.Generation, &after
			if _, err := PredictPolicyIdentity(r.ControlRoot, after, patch); !errors.Is(err, ErrPolicyConflict) {
				t.Fatalf("rebased prediction: %v", err)
			}
			if _, err := Commit(ctx, r); !errors.Is(err, ErrPolicyConflict) {
				t.Fatalf("rebased commit: %v", err)
			}
			got, err := os.ReadFile(path)
			l, ledgerErr := readLedger(r.ControlRoot)
			if err != nil || ledgerErr != nil || string(got) != string(edit) || !reflect.DeepEqual(l, committed) {
				t.Fatal("foreign edit or ledger changed", err, ledgerErr)
			}
		})
	}
}

func TestPolicyPredictionRefusesUnobservedAndMalformedInputs(t *testing.T) {
	ctx, r := request(t)
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	before, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	patch := map[string]json.RawMessage{"route": json.RawMessage(`{"cursorNotifications":{"desktop":false,"webhook":false}}`)}
	edit := []byte(`{"schemaVersion":1,"enabled":false,"foreign":"precommit"}`)
	if err := os.WriteFile(path, edit, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PredictPolicyIdentity(r.ControlRoot, before, patch); !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("unobserved edit predicted: %v", err)
	}
	r.ExpectedGeneration, r.ExpectedPolicy, r.PolicyFields = &ledger.Generation, &before, patch
	if _, err := Commit(ctx, r); !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("unobserved edit committed: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(edit) {
		t.Fatal("unobserved precommit edit changed", err)
	}
	for _, raw := range []string{`{`, `{"schemaVersion":1,"enabled":false,"route":{"cursorNotifications":null}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		current, err := Fingerprint(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := PredictPolicyIdentity(r.ControlRoot, current, patch); err == nil {
			t.Fatal("malformed prediction succeeded")
		}
		got, _ := os.ReadFile(path)
		if string(got) != raw {
			t.Fatal("malformed input rewritten")
		}
	}
	l, err := readLedger(r.ControlRoot)
	if err != nil || !reflect.DeepEqual(l, ledger) {
		t.Fatal("refusal mutated ledger", err)
	}
}
