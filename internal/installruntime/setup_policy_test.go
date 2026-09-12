package installruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPolicyAtomicFields(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"enabled":false,"foreign":{"keep":true},"route":{"extension":42,"localRouting":false},"rates":{"extension":17}}`), 0600); err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.PolicyEnabled = &enabled
	r.ExpectedGeneration = &l.Generation
	r.RefreshOnly = true
	r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"localRouting":true}`), "rates": json.RawMessage(`{"burst":2}`)}
	next, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Policy.Enabled || !s.Installation.Enabled || next.Generation != l.Generation+1 {
		t.Fatal("intent/generation not committed together")
	}
	for key, want := range map[string]string{"foreign": `{"keep":true}`, "route": `{"extension":42,"localRouting":true}`, "rates": `{"burst":2,"extension":17}`} {
		var got, expected any
		_ = json.Unmarshal(s.Fields[key], &got)
		_ = json.Unmarshal([]byte(want), &expected)
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(expected)
		if string(a) != string(b) {
			t.Fatalf("%s: %s", key, a)
		}
	}
	before, _ := os.ReadFile(path)
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("stale update accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("stale update mutated policy")
	}
	r.ExpectedGeneration = &next.Generation
	r.PolicyFields = map[string]json.RawMessage{"foreign": json.RawMessage(`{}`)}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("unbounded mutation accepted")
	}
}

func TestSetupPolicyByteCASAndWriterFloor(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	r.PolicyEnabled = &enabled
	r.RefreshOnly = true
	r.ExpectedGeneration = &l.Generation
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	id, err := Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedPolicy = &id
	data := `{"schemaVersion":1,"enabled":false,"foreign":"manual"}`
	if err = os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("manual policy CAS bypassed")
	}
	got, _ := os.ReadFile(path)
	if string(got) != data {
		t.Fatal("manual bytes overwritten")
	}
	// Pure disable is allowed without native, but must never bypass writer floor.
	l.WriterFloor = WriterFloor + 1
	if err = writeJSON(filepath.Join(r.ControlRoot, "ownership.json"), l); err != nil {
		t.Fatal(err)
	}
	enabled = false
	r.ExpectedPolicy = nil
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("disable bypassed writer floor")
	}
	got, _ = os.ReadFile(path)
	if string(got) != data {
		t.Fatal("writer-floor refusal mutated policy")
	}
}
func TestSetupPolicyRejectsDuplicateAndOversizedJSON(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedGeneration = &l.Generation
	r.RefreshOnly = true
	for _, raw := range []string{`{"burst":2,"burst":3}`, `null`, `[]`, `{"foreign":"` + strings.Repeat("x", 65536) + `"}`} {
		r.PolicyFields = map[string]json.RawMessage{"rates": json.RawMessage(raw)}
		if _, err = Commit(ctx, r); err == nil {
			t.Fatal("accepted invalid patch", raw)
		}
	}
	if err = os.WriteFile(filepath.Join(r.ControlRoot, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":false,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("accepted ambiguous preimage")
	}
}

func TestSetupPolicySizeIncludesFinalNewline(t *testing.T) {
	fields := map[string]json.RawMessage{"schemaVersion": json.RawMessage(`1`), "enabled": json.RawMessage(`false`), "foreign": json.RawMessage(`""`)}
	empty, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fields["foreign"], _ = json.Marshal(strings.Repeat("x", 64*1024-len(empty)))
	if _, err = policyFile(t.TempDir(), false, fields, Identity{}); err == nil {
		t.Fatal("published policy would exceed the reader limit by its final newline")
	}
}

func TestSetupPolicyOnlyRejectsAssetsAndPreparationWrites(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PolicyOnly = true
	r.RefreshOnly = true
	r.ExpectedGeneration = &l.Generation
	asset := File{Path: filepath.Join(r.RuntimeRoot, "unexpected"), Data: []byte("not a policy"), Mode: 0600}
	r.Files = []File{asset}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("policy-only accepted asset")
	}
	r.Files = nil
	r.Prepare = func() ([]File, error) { return []File{asset}, nil }
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("policy-only accepted prepared asset")
	}
	if _, err = os.Stat(asset.Path); !os.IsNotExist(err) {
		t.Fatal("policy-only wrote asset")
	}
}

func TestSetupSnapshotPreimageIsBoundToParsedBytes(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.ControlRoot, "agent-notifications.json")
	versions := map[string]string{`"a"`: "{\n\"schemaVersion\":1,\"enabled\":false,\"foreign\":\"a\"\n}", `"b"`: `{"schemaVersion":1,"enabled":false,"foreign":"b"}`}
	if err := os.WriteFile(path, []byte(versions[`"a"`]), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 30; i++ {
			value := versions[`"a"`]
			if i%2 != 0 {
				value = versions[`"b"`]
			}
			temp := path + ".manual"
			if err := os.WriteFile(temp, []byte(value), 0600); err != nil {
				done <- err
				return
			}
			if err := os.Rename(temp, path); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 60; i++ {
		s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
		if err != nil {
			continue
		} // changing manual preimage fails closed
		value, ok := versions[string(s.Fields["foreign"])]
		if !ok {
			t.Fatal("unknown parsed version")
		}
		if s.Preimage != identity([]byte(value), 0600) {
			t.Fatal("snapshot preimage came from different bytes than its fields")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if s.Preimage != identity([]byte(versions[string(s.Fields["foreign"])]), 0600) {
		t.Fatal("final preimage mismatch")
	}
}
