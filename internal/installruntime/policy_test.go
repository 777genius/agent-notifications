package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUserPolicyRepairAndRemoval(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.PolicyEnabled = &enabled
	r.ExpectedGeneration = &l.Generation
	l, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	for path := range l.Files {
		if filepath.Base(path) == "agent-notifications.json" {
			t.Fatal("policy classified as disposable runtime asset", path)
		}
	}
	r.PolicyEnabled = nil
	r.ExpectedGeneration = nil
	for _, id := range []string{r.ConsumerID, "other"} {
		r.ConsumerID = id
		l, err = Commit(ctx, r)
		if err != nil || !l.Enabled {
			t.Fatal("lost intent", err)
		}
	}
	r.RemoveConsumer = true
	l, err = Commit(ctx, r)
	if err != nil || !l.Enabled {
		t.Fatal("unrelated removal revoked", err)
	}
	// A retained state namespace is not permission to resume sends on reinstall.
	state := filepath.Join(r.ControlRoot, "state", "retained-admission")
	if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("existing admission"), 0600); err != nil {
		t.Fatal(err)
	}
	r.ConsumerID = "codex"
	l, err = Commit(ctx, r)
	if err != nil || l.Enabled {
		t.Fatal("final uninstall did not revoke", err)
	}
	p, err := ReadUserPolicy(r.ControlRoot)
	if err != nil || p.Enabled {
		t.Fatal("policy not revoked", err)
	}
	id := l.ID
	r.RemoveConsumer = false
	l, err = Commit(ctx, r)
	if err != nil || l.Enabled || l.ID != id {
		t.Fatal("reinstall restored intent or replaced namespace", err)
	}
	r.PolicyEnabled = &enabled
	r.ExpectedGeneration = &l.Generation
	l, err = Commit(ctx, r)
	if err != nil || !l.Enabled || l.ID != id {
		t.Fatal("explicit re-enable failed or replaced namespace", err)
	}
	data, err := os.ReadFile(state)
	if err != nil || string(data) != "existing admission" {
		t.Fatal("reinstall/re-enable changed retained state", err)
	}
}
func TestUserPolicyInvalidAndMissingNoCreate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	p, err := ReadUserPolicy(root)
	if err != nil || p.Enabled {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read created root")
	}
	ctx, r := request(t)
	if _, err = Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	for _, data := range []string{`{`, `null`, `{}`, `{"schemaVersion":2,"enabled":true}`, `{"schemaVersion":1,"enabled":null}`} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Commit(ctx, r); err == nil {
			t.Fatal("invalid policy repaired")
		}
		if _, err = ReadInstalledSnapshot(r.ControlRoot); err == nil {
			t.Fatal("invalid policy admitted")
		}
		got, _ := os.ReadFile(path)
		if string(got) != data {
			t.Fatal("invalid policy overwritten")
		}
	}
}

func TestPolicyDisableRecoveryRevokesStaleRequest(t *testing.T) {
	for _, boundary := range []string{"transaction", "policy-promotion", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, r := request(t)
			ledger, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			enabled := true
			r.PolicyEnabled = &enabled
			r.ExpectedGeneration = &ledger.Generation
			ledger, err = Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := ReadPolicySnapshot(ctx, r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			enabled = false
			r.ExpectedGeneration = &ledger.Generation
			policyPath := filepath.Join(r.ControlRoot, "agent-notifications.json")
			if canonical, err := CanonicalPath(policyPath); err == nil {
				policyPath = canonical
			}
			r.Fault = func(phase string) error {
				if phase == boundary || (boundary == "policy-promotion" && phase == "promotion:"+policyPath) {
					return fmt.Errorf("crash")
				}
				return nil
			}
			if _, err = Commit(ctx, r); err == nil {
				t.Fatal("missing crash")
			}
			if _, release, err := AcquireInstalledLease(ctx, r.ControlRoot, snapshot.Installation); err == nil {
				release()
				t.Fatal("request admitted through recovery fence")
			}
			r.Fault = nil
			r.PolicyEnabled = nil
			r.ExpectedGeneration = nil
			if _, err = Commit(ctx, r); err != nil {
				t.Fatal(err)
			}
			next, err := ReadPolicySnapshot(ctx, r.ControlRoot)
			if err != nil || next.Policy.Enabled || next.Installation.Enabled {
				t.Fatal("repair lost recovered disable", err)
			}
			enabled = true
			r.ExpectedGeneration = &ledger.Generation
			r.PolicyEnabled = &enabled
			if _, err = Commit(ctx, r); err == nil {
				t.Fatal("stale enable overwrote disable")
			}
		})
	}
}
