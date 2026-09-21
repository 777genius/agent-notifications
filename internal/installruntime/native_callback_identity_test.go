package installruntime

import (
	"context"
	"os"
	"testing"
)

func TestRetireNativePreservesPublishedPredecessor(t *testing.T) {
	ctx, r := request(t)
	var paths []string
	for i := 0; i < 2; i++ {
		change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		ledger, err := Commit(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, change.After.Path)
		r.Native = nil
		gen := ledger.Generation
		r.ExpectedGeneration = &gen
	}
	if paths[0] == paths[1] {
		t.Fatal("update reused callback identity")
	}
	called := 0
	nativeDrainCheck = func(ctx context.Context, predecessor string) error {
		called++
		return verifyNativeDrain(ctx, predecessor)
	}
	t.Cleanup(func() { nativeDrainCheck = verifyNativeDrain })
	r.RetireNative = true
	r.Native = nil
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatal("published generation was process-retired")
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatal("published predecessor disappeared")
	}
	if _, err := os.Stat(paths[1]); err != nil {
		t.Fatal("active generation disappeared")
	}
	if ledger.Native == nil || ledger.Native.Path != paths[1] || len(ledger.Native.Published) != 2 {
		t.Fatalf("retirement mutated published inventory: %+v", ledger.Native)
	}
}
