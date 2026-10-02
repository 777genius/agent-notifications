//go:build linux || darwin

package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// shiftDevice keeps the inode and changes only the device number, which is
// what a reboot that renumbers the volume does to a recorded identity.
func shiftDevice(t *testing.T, id string) string {
	t.Helper()
	dev, ino, ok := strings.Cut(id, ":")
	n, err := strconv.ParseUint(dev, 10, 64)
	if !ok || err != nil {
		t.Fatalf("unexpected identity %q", id)
	}
	return fmt.Sprintf("%d:%s", n+1, ino)
}

// renumberNativeDevices rewrites the ledger as if it had been written before
// a reboot assigned the volume a different device number.
func renumberNativeDevices(t *testing.T, control string) Ledger {
	t.Helper()
	l, err := readLedger(control)
	if err != nil || l.Native == nil {
		t.Fatalf("ledger without native record: %v", err)
	}
	l.Native.DirectoryID = shiftDevice(t, l.Native.DirectoryID)
	if l.Native.PreviousDirectoryID != "" {
		l.Native.PreviousDirectoryID = shiftDevice(t, l.Native.PreviousDirectoryID)
	}
	for i := range l.Native.Published {
		l.Native.Published[i].DirectoryID = shiftDevice(t, l.Native.Published[i].DirectoryID)
	}
	if err := writeJSON(filepath.Join(control, "ownership.json"), l); err != nil {
		t.Fatal(err)
	}
	return l
}

func requireFreshNativeIdentities(t *testing.T, record *NativeRecord) {
	t.Helper()
	check := func(path, id string) {
		if path == "" {
			return
		}
		fresh, err := nativeDirectoryID(path)
		if err != nil {
			t.Fatal(err)
		}
		if fresh != "" && fresh != id {
			t.Fatalf("stale identity kept for %s: %s, now %s", filepath.Base(path), id, fresh)
		}
	}
	check(record.Path, record.DirectoryID)
	check(record.PreviousPath, record.PreviousDirectoryID)
	for _, gen := range record.Published {
		check(gen.Path, gen.DirectoryID)
	}
}

func installTwoNativeGenerations(t *testing.T) (context.Context, Request, *NativeChange, *NativeChange) {
	t.Helper()
	ctx, r := request(t)
	var changes []*NativeChange
	for i := 0; i < 2; i++ {
		change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		if _, err := Commit(ctx, r); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change)
	}
	r.Native = nil
	return ctx, r, changes[0], changes[1]
}

func TestRenumberedVolumeKeepsNativeManaged(t *testing.T) {
	t.Run("snapshot", func(t *testing.T) {
		_, r, _, _ := installTwoNativeGenerations(t)
		renumberNativeDevices(t, r.ControlRoot)
		if _, err := ReadInstalledSnapshot(r.ControlRoot); err != nil {
			t.Fatalf("renumbered volume broke the snapshot reader: %v", err)
		}
	})
	t.Run("commit rebinds", func(t *testing.T) {
		ctx, r, _, _ := installTwoNativeGenerations(t)
		renumberNativeDevices(t, r.ControlRoot)
		next, err := Commit(ctx, r)
		if err != nil {
			t.Fatalf("renumbered volume blocked a commit: %v", err)
		}
		requireFreshNativeIdentities(t, next.Native)
		stored, err := readLedger(r.ControlRoot)
		if err != nil {
			t.Fatal(err)
		}
		requireFreshNativeIdentities(t, stored.Native)
	})
	t.Run("upgrade to new bytes", func(t *testing.T) {
		ctx, r, _, second := installTwoNativeGenerations(t)
		renumberNativeDevices(t, r.ControlRoot)
		change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		next, err := Commit(ctx, r)
		if err != nil {
			t.Fatalf("renumbered volume blocked an upgrade: %v", err)
		}
		if next.Native.Path != change.After.Path || next.Native.PreviousPath != second.After.Path || len(next.Native.Published) != 3 {
			t.Fatalf("unexpected upgrade record: %+v", next.Native)
		}
		requireFreshNativeIdentities(t, next.Native)
	})
	t.Run("reinstall of an older generation", func(t *testing.T) {
		ctx, r, first, _ := installTwoNativeGenerations(t)
		renumberNativeDevices(t, r.ControlRoot)
		change, err := StageNative(ctx, r.ControlRoot, first.After.Path)
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		next, err := Commit(ctx, r)
		if err != nil {
			t.Fatalf("renumbered volume blocked reselecting a generation: %v", err)
		}
		if next.Native.Path != first.After.Path {
			t.Fatalf("older bytes did not reselect their generation: %+v", next.Native)
		}
		requireFreshNativeIdentities(t, next.Native)
	})
	t.Run("final uninstall purge", func(t *testing.T) {
		ctx, r, _, second := installTwoNativeGenerations(t)
		renumberNativeDevices(t, r.ControlRoot)
		r.RemoveConsumer, r.PurgeNative = true, true
		purged, err := Commit(ctx, r)
		if err != nil {
			t.Fatalf("renumbered volume blocked the purge: %v", err)
		}
		if purged.Native != nil {
			t.Fatal("purge kept the native record")
		}
		if _, err := os.Stat(second.After.Path); !os.IsNotExist(err) {
			t.Fatal("purge kept the active generation")
		}
	})
	t.Run("recovery after interruption", func(t *testing.T) {
		ctx, r, _, _ := installTwoNativeGenerations(t)
		before := renumberNativeDevices(t, r.ControlRoot)
		r.Fault = func(phase string) error {
			if phase == "transaction" {
				return fmt.Errorf("simulated interruption")
			}
			return nil
		}
		if _, err := Commit(ctx, r); err == nil {
			t.Fatal("interruption did not stop the commit")
		}
		if _, err := os.Lstat(filepath.Join(r.ControlRoot, "transaction.json")); err != nil {
			t.Fatalf("interrupted commit left no journal: %v", err)
		}
		if stored, err := readLedger(r.ControlRoot); err != nil || stored.Native.DirectoryID != before.Native.DirectoryID {
			t.Fatalf("ledger changed before recovery: %v", err)
		}
		recovered, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
		if err != nil {
			t.Fatalf("renumbered volume blocked recovery: %v", err)
		}
		requireFreshNativeIdentities(t, recovered.Native)
	})
}

func TestReplacedNativeDirectoryStillRefused(t *testing.T) {
	for _, renumbered := range []bool{false, true} {
		t.Run(fmt.Sprint("renumbered=", renumbered), func(t *testing.T) {
			ctx, r, _, second := installTwoNativeGenerations(t)
			if renumbered {
				renumberNativeDevices(t, r.ControlRoot)
			}
			live := second.After.Path
			if err := os.Rename(live, live+".moved"); err != nil {
				t.Fatal(err)
			}
			if err := os.CopyFS(live, os.DirFS(live+".moved")); err != nil {
				t.Fatal(err)
			}
			if _, err := Commit(ctx, r); err == nil || !strings.Contains(err.Error(), "native directory inode changed") {
				t.Fatalf("replaced native directory accepted: %v", err)
			}
			if _, err := ReadInstalledSnapshot(r.ControlRoot); err == nil {
				t.Fatal("snapshot reader accepted a replaced native directory")
			}
		})
	}
}

func TestRenameNativeAcceptsRenumberedSource(t *testing.T) {
	skipUnsupportedNative(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	from, to := filepath.Join(root, "from.app"), filepath.Join(root, "to.app")
	if err := os.Mkdir(from, 0700); err != nil {
		t.Fatal(err)
	}
	id, err := nativeDirectoryID(from)
	if err != nil || id == "" {
		t.Fatalf("source identity: %q %v", id, err)
	}
	anchors, err := pathAnchors(to, false)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := strings.Cut(id, ":")
	otherInode, _ := strconv.ParseUint(ino, 10, 64)
	if err := renameNative(from, to, anchors, fmt.Sprintf("%s:%d", dev, otherInode+1)); err == nil {
		t.Fatal("rename accepted a source with a different inode")
	}
	if err := renameNative(from, to, anchors, shiftDevice(t, id)); err != nil {
		t.Fatalf("rename refused a source recorded before the volume was renumbered: %v", err)
	}
	if _, err := os.Stat(to); err != nil {
		t.Fatal(err)
	}
}
