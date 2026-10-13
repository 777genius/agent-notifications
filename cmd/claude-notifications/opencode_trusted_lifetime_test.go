package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This is a held-file revocation fixture, not a native parent-image grant.
func TestCompositionGuardKeepsOriginBudgetAndStickyCancellation(t *testing.T) {
	for _, mode := range []string{"originBefore", "suppliedBefore", "originDuring", "suppliedDuring", "imageChanged"} {
		t.Run(mode, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "TEST-held-image"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.WriteString("TEST-library-fixture"); err != nil {
				t.Fatal(err)
			}
			origin, cancelOrigin := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
			defer cancelOrigin()
			supplied, cancelSupplied := context.WithCancel(context.Background())
			defer cancelSupplied()
			hash, err := runtimeFileHash(origin, file)
			if err != nil {
				t.Fatal(err)
			}
			image := runtimeLiveImage{SHA256: hash}
			reads, releases := 0, 0
			lease := &runtimeImageLease{origin: origin, image: image, release: func() { releases++; _ = file.Close() }}
			defer lease.Close()
			lease.read = func(ctx context.Context) (runtimeLiveImage, error) {
				reads++
				original, _ := origin.Deadline()
				deadline, ok := ctx.Deadline()
				if !ok || !deadline.Equal(original) {
					t.Fatal("read gained fresh origin budget")
				}
				if mode == "originDuring" {
					cancelOrigin()
				}
				if mode == "suppliedDuring" {
					cancelSupplied()
					<-ctx.Done()
				}
				next, err := runtimeFileHash(ctx, file)
				if mode == "imageChanged" {
					next = "changed-file"
				}
				return runtimeLiveImage{SHA256: next}, err
			}
			invocation, cancelInvocation := context.WithCancel(origin)
			defer cancelInvocation()
			guard := runtimeLifetimeGuard{lease: lease, cancel: cancelInvocation}
			if mode == "originBefore" {
				cancelOrigin()
			}
			if mode == "suppliedBefore" {
				cancelSupplied()
			}
			if guard.check(supplied) || invocation.Err() == nil {
				t.Fatal("cancelled/changed held image authorized")
			}
			settledReads := reads
			if guard.check(context.Background()) || reads != settledReads {
				t.Fatal("fresh context revived old lease")
			}
			if releases != 0 {
				t.Fatal("revocation released held resource before settlement")
			}
			lease.Close()
			lease.Close()
			if releases != 1 {
				t.Fatal("held resource did not settle once")
			}
		})
	}
}
