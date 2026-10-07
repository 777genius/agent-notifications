package installruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupLeaseDoesNotEnableDelivery(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, err := ReadInstalledSnapshot(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	current, release, err := AcquireSetupLease(ctx, r.ControlRoot, s)
	if err != nil {
		t.Fatal(err)
	}
	if current.Enabled {
		t.Fatal("setup enabled policy")
	}
	release()
	if _, release, err := AcquireInstalledLease(ctx, r.ControlRoot, s); err == nil {
		release()
		t.Fatal("disabled delivery admitted")
	}
	stale := s
	stale.Ledger.Generation++
	if _, release, err := AcquireSetupLease(ctx, r.ControlRoot, stale); err == nil {
		release()
		t.Fatal("stale setup admitted")
	}
	lock := filepath.Join(r.ControlRoot, ".component-install.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, release, err := AcquireSetupLease(ctx, r.ControlRoot, s); err == nil {
		release()
		t.Fatal("lost lock accepted")
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatal("lock recreated")
	}
}
