package installruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPolicyLeasePinsExactPolicyWithoutPortableEnablement(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, err := ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if s.Installation.Enabled {
		t.Fatal("fixture unexpectedly enabled portable delivery")
	}
	current, release, err := AcquirePolicyLease(ctx, r.ControlRoot, s)
	if err != nil {
		t.Fatalf("disabled portable delivery blocked a consumer-specific lease: %v", err)
	}
	if current.Preimage != s.Preimage || current.Installation.Ledger.Generation != s.Installation.Ledger.Generation {
		t.Fatal("lease did not retain the observed snapshot")
	}
	short, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	gen := s.Installation.Ledger.Generation
	_, err = Commit(short, Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: r.Owner, ConsumerID: r.ConsumerID, RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &gen, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"other":true}`)}})
	if err == nil {
		release()
		t.Fatal("policy mutation crossed the retained handoff lease")
	}
	release()
	if _, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: r.Owner, ConsumerID: r.ConsumerID, RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &gen, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"other":true}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, release, err := AcquirePolicyLease(ctx, r.ControlRoot, s); err == nil {
		release()
		t.Fatal("stale policy snapshot admitted")
	}
}
