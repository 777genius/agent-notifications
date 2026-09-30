//go:build darwin

package opencodeinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareNativeSpoolUsesSeparatePrivateNamespace(t *testing.T) {
	ctx, r, _ := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	spool, err := PrepareNativeSpool(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if spool != filepath.Join(r.ControlRoot, "opencode-native-spool") {
		t.Fatalf("unexpected spool path: %s", spool)
	}
	if _, err := os.Lstat(filepath.Join(r.ControlRoot, "state")); !os.IsNotExist(err) {
		t.Fatal("OpenCode spool occupied portable setup state namespace")
	}
	info, err := os.Lstat(spool)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("spool is not private: %v %v", info, err)
	}
	lock, err := os.Lstat(filepath.Join(spool, ".spool.lock"))
	if err != nil || !lock.Mode().IsRegular() || lock.Mode().Perm() != 0600 {
		t.Fatalf("permanent spool lock missing: %v %v", lock, err)
	}
	if again, err := PrepareNativeSpool(ctx, r.ControlRoot); err != nil || again != spool {
		t.Fatalf("idempotent private spool preparation: %s %v", again, err)
	}
	if err := os.Remove(filepath.Join(spool, ".spool.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spool); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if err := os.Symlink(foreign, spool); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareNativeSpool(ctx, r.ControlRoot); err == nil {
		t.Fatal("symlinked spool accepted")
	}
	if _, err := os.Lstat(filepath.Join(foreign, ".spool.lock")); !os.IsNotExist(err) {
		t.Fatal("spool lock created in foreign directory")
	}
}
