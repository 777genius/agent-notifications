//go:build darwin

package installruntime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Real Darwin no-follow syscalls with simulated persisted device renumbering.
// This test does not claim an actual remount or run native capabilities.
func TestOrphanDarwinRenumberedNativeRefreshAndRecovery(t *testing.T) {
	for _, boundary := range []string{"transaction", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.retainPreviousNative(t)
			original := cloneOrphanLedger(f.before)
			renumberRecord(f.before.Native)
			f.saveLedger(t)
			f.abandon(t, true)
			images := orphanControlImages(t, f.control)
			p, err := PreviewOrphanConsumer(f.control, f.recoveryRequest())
			if err != nil || !p.NativeValidated || !p.NativeIdentityRefresh {
				t.Fatalf("device-only preview: %+v %v", p, err)
			}
			assertOrphanImages(t, images)
			f.interrupt(t, boundary)
			got, err := Recover(f.ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Native, original.Native) {
				t.Fatal("identity refresh altered native paths/bytes/evidence")
			}
			assertFreshNativeIDs(t, got)
			if _, err := ReadInstalledSnapshot(f.control); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(f.runtime); !os.IsNotExist(err) {
				t.Fatal("runtime recreated", err)
			}
		})
	}
}

func TestOrphanDarwinPreviousNativeDamageCannotRefreshIdentity(t *testing.T) {
	for _, damage := range []string{"missing-sha-inside", "missing-sha-outside", "missing-identity", "changed-bytes", "duplicate-path-digest"} {
		t.Run(damage, func(t *testing.T) {
			f := newOrphanFixture(t)
			f.retainPreviousNative(t)
			renumberRecord(f.before.Native)
			damageOrphanPreviousNative(t, f, damage)
			f.saveLedger(t)
			f.abandon(t, true)
			images := orphanControlImages(t, f.control)
			assertTrees := assertOrphanNativeTreesUnchanged(t, f)
			if _, err := PreviewOrphanConsumer(f.control, f.recoveryRequest()); err == nil {
				t.Error("unqualified previous generation admitted for device refresh")
			}
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, f.recoveryRequest()); err == nil {
				t.Error("unqualified previous generation refreshed without its original bytes")
			}
			assertOrphanImages(t, images)
			assertTrees()
		})
	}
}

func TestOrphanDarwinAbsenceChainsRejectContradictoryDeviceGroups(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	tx := f.interrupt(t, "transaction")
	// Each chain is individually inode-valid, but one lies about the shared
	// device group. A global mapping must reject the split/collapse.
	for i := range tx.OrphanRecovery.RuntimeAbsences[0].Parents {
		a := &tx.OrphanRecovery.RuntimeAbsences[0].Parents[i]
		a.Identity = renumberID(a.Identity)
	}
	if err := writeTransaction(filepath.Join(f.control, "transaction.json"), tx); err != nil {
		t.Fatal(err)
	}
	images := orphanControlImages(t, f.control)
	if _, err := Recover(f.ctx, f.control); err == nil {
		t.Fatal("contradictory absence device mapping accepted")
	}
	assertOrphanImages(t, images)
}
