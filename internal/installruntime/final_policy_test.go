package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
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
