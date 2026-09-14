package installruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecoverOnlyReplaysJournalWithoutConsumer(t *testing.T) {
	ctx, r := request(t)
	target := filepath.Join(r.RuntimeRoot, "hook")
	r.Files = []File{{Path: target, Data: []byte("new"), Mode: 0755}}
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("fault not reached")
	}
	got, err := Recover(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := CanonicalPath(r.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 1 || got.Consumers["codex"].RuntimeRoot != wantRoot {
		t.Fatalf("recovered ledger: %+v want RuntimeRoot %s", got, wantRoot)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("promotion: %s %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(r.ControlRoot, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("journal retained after recover-only")
	}
	again, err := Recover(ctx, r.ControlRoot)
	if err != nil || again.ID != got.ID || again.Generation != got.Generation {
		t.Fatalf("repeat recover: %+v %v", again, err)
	}
	if len(again.Consumers) != len(got.Consumers) {
		t.Fatal("recover-only added a consumer")
	}
}

func TestRecoverOnlyMissingRootIsNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	got, err := Recover(ctx, filepath.Join(t.TempDir(), "missing"))
	if err != nil || got.ID != "" || got.Generation != 0 {
		t.Fatalf("missing recover: %+v %v", got, err)
	}
}

func TestReservationStartBlocksUnmatchedWriterAndAllowsMatch(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(r.ControlRoot, "portable-handoff.json")
	res := &PendingMutation{ID: "intent-1", Owner: r.Owner, IntentRef: intent}
	r.Files = []File{{Path: intent, Data: []byte(`{"version":1}` + "\n"), Mode: 0600}}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = res, true, &ledger.Generation
	held, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if held.PendingMutation == nil || held.PendingMutation.ID != res.ID || held.WriterFloor != ReservationWriterFloor || held.Schema != ledgerSchemaV3 {
		t.Fatalf("reservation not published: %+v", held)
	}
	data, err := os.ReadFile(filepath.Join(r.ControlRoot, "transaction.json"))
	if err == nil {
		t.Fatalf("journal left after success: %s", data)
	}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = nil, false, &held.Generation
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "extra"), Data: []byte("x"), Mode: 0755}}
	if _, err := Commit(ctx, r); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("unmatched writer: %v", err)
	}
	r.Reservation, r.RefreshOnly = res, true
	r.Files = nil
	matched, err := Commit(ctx, r)
	if err != nil || matched.PendingMutation == nil || matched.WriterFloor != ReservationWriterFloor {
		t.Fatalf("matched writer: %+v %v", matched, err)
	}
	enabled := false
	r.Reservation, r.RefreshOnly, r.PolicyEnabled, r.Files = nil, true, &enabled, nil
	r.ExpectedGeneration = &matched.Generation
	disabled, err := Commit(ctx, r)
	if err != nil || disabled.Enabled || disabled.PendingMutation == nil || disabled.WriterFloor != ReservationWriterFloor {
		t.Fatalf("policy disable: %+v %v", disabled, err)
	}
}

func TestReservationClearKeepsFloorAndOldWriterRejectsSchema(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(r.ControlRoot, "portable-handoff.json")
	res := &PendingMutation{ID: "intent-clear", Owner: r.Owner, IntentRef: intent}
	r.Files = []File{{Path: intent, Data: []byte(`{"version":1}` + "\n"), Mode: 0600}}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = res, true, &ledger.Generation
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	raw := mustRead(t, filepath.Join(r.ControlRoot, "transaction.json"))
	tx, err := decodeTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Schema != transactionSchemaV3 || !legacyAcceptedTransactionSchema(transactionSchemaV2) || legacyAcceptedTransactionSchema(tx.Schema) {
		t.Fatalf("legacy writer must reject reservation schema %d", tx.Schema)
	}
	r.Fault = nil
	held, err := Recover(ctx, r.ControlRoot)
	if err != nil || held.PendingMutation == nil || held.WriterFloor != ReservationWriterFloor {
		t.Fatalf("recover reservation: %+v %v", held, err)
	}
	r.Reservation, r.ClearReservation, r.RefreshOnly, r.Files = res, true, true, nil
	r.ExpectedGeneration = &held.Generation
	cleared, err := Commit(ctx, r)
	if err != nil || cleared.PendingMutation != nil || cleared.WriterFloor != ReservationWriterFloor || cleared.Schema != ledgerSchemaV3 {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	r.ClearReservation, r.Reservation, r.RefreshOnly = false, nil, true
	r.ExpectedGeneration = &cleared.Generation
	again, err := Commit(ctx, r)
	if err != nil || again.WriterFloor != ReservationWriterFloor || again.Schema != ledgerSchemaV3 {
		t.Fatalf("floor lowered: %+v %v", again, err)
	}
}

func TestReservationRollbackDoesNotLowerFloor(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(r.ControlRoot, "portable-handoff.json")
	res := &PendingMutation{ID: "intent-rollback", Owner: r.Owner, IntentRef: intent}
	r.Files = []File{{Path: intent, Data: []byte(`{"version":1}` + "\n"), Mode: 0600}}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = res, true, &ledger.Generation
	r.Fault = func(phase string) error {
		if phase == "ledger" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	r.Fault, r.RollbackPending, r.Reservation, r.RefreshOnly, r.Files, r.ExpectedGeneration = nil, true, nil, false, nil, nil
	rolled, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.WriterFloor != ReservationWriterFloor {
		t.Fatalf("rollback lowered floor: %+v", rolled)
	}
}

func TestCoordinatorLeaseSerializesResume(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	held, err := AcquireCoordinatorLease(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	blocked, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := AcquireCoordinatorLease(blocked, r.ControlRoot); err == nil {
		t.Fatal("second coordinator acquired lease")
	}
}

func TestConcurrentMatchedResumeIsSerialized(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(r.ControlRoot, "portable-handoff.json")
	res := &PendingMutation{ID: "intent-serial", Owner: r.Owner, IntentRef: intent}
	r.Files = []File{{Path: intent, Data: []byte(`{"version":1}` + "\n"), Mode: 0600}}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = res, true, &ledger.Generation
	held, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	gens := make([]uint64, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			leaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			release, err := AcquireCoordinatorLease(leaseCtx, r.ControlRoot)
			if err != nil {
				errs[i] = err
				return
			}
			defer release()
			snap, err := readLedger(r.ControlRoot)
			if err != nil {
				errs[i] = err
				return
			}
			req := r
			req.Reservation, req.RefreshOnly, req.Files = res, true, nil
			req.ExpectedGeneration = &snap.Generation
			got, err := Commit(leaseCtx, req)
			errs[i] = err
			if err == nil {
				gens[i] = got.Generation
			}
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("serialized resume: %v %v", errs[0], errs[1])
	}
	if gens[0] == gens[1] || gens[0] == held.Generation && gens[1] == held.Generation {
		t.Fatalf("generation not advanced under lease: %v starting %d", gens, held.Generation)
	}
}

func TestNoopDoesNotCreateReservation(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Files, r.RefreshOnly, r.ExpectedGeneration = nil, true, &ledger.Generation
	got, err := Commit(ctx, r)
	if err != nil || got.PendingMutation != nil || got.WriterFloor != WriterFloor || got.Schema != ledgerSchemaV2 {
		t.Fatalf("noop created reservation: %+v %v", got, err)
	}
}

func TestRecoverOnlyRejectsCombinedMutation(t *testing.T) {
	ctx, r := request(t)
	r.RecoverOnly = true
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("x"), Mode: 0755}}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("combined recover-only accepted")
	}
}

func TestOldWriterRejectsReservationFloorAndLedgerSchema(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(r.ControlRoot, "portable-handoff.json")
	res := &PendingMutation{ID: "intent-floor", Owner: r.Owner, IntentRef: intent}
	r.Files = []File{{Path: intent, Data: []byte(`{"version":1}` + "\n"), Mode: 0600}}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = res, true, &ledger.Generation
	held, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if legacyWriterAcceptsFloor(held.WriterFloor) || legacyAcceptedLedgerSchema(held.Schema) {
		t.Fatalf("frozen v1 writer accepted reserved ledger floor=%d schema=%d", held.WriterFloor, held.Schema)
	}
}

func TestPolicyOnlyRefusesPendingJournalDuringReservation(t *testing.T) {
	ctx, r := request(t)
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "hook"), Data: []byte("hook"), Mode: 0755}}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(r.ControlRoot, "portable-handoff.json")
	res := &PendingMutation{ID: "intent-policy-journal", Owner: r.Owner, IntentRef: intent}
	r.Files = []File{{Path: intent, Data: []byte(`{"version":1}` + "\n"), Mode: 0600}}
	r.Reservation, r.RefreshOnly, r.ExpectedGeneration = res, true, &ledger.Generation
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	enabled := false
	r.Fault, r.Reservation, r.RefreshOnly, r.PolicyOnly, r.PolicyEnabled, r.Files = nil, nil, true, true, &enabled, nil
	if _, err := Commit(ctx, r); !errors.Is(err, ErrPolicyRecovery) {
		t.Fatalf("policy-only during pending journal: %v", err)
	}
}

func TestKernelSourceDoesNotImportHostAdapters(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "internal/agentnotify/") || strings.Contains(string(body), "plugin-kit-ai") {
			t.Fatalf("%s imports host adapter or UAP types", entry.Name())
		}
	}
}

func TestReservationJSONRoundTrip(t *testing.T) {
	want := Ledger{Schema: ledgerSchemaV3, ID: "id", Generation: 1, PolicyGeneration: 1, WriterFloor: ReservationWriterFloor, Consumers: map[string]Consumer{}, Files: map[string]Identity{}, PendingMutation: &PendingMutation{ID: "a", Owner: "o", IntentRef: "/intent"}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Ledger
	if err := json.Unmarshal(raw, &got); err != nil || got.PendingMutation == nil || got.PendingMutation.ID != "a" {
		t.Fatalf("json: %+v %v", got, err)
	}
}
