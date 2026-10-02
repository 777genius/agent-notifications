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

// renumberJournal rewrites every device number recorded in the pending journal
// through shift, as if the volumes were renumbered by a reboot after the
// interruption. The ledgers inside the journal are shifted with the same rule.
func renumberJournal(t *testing.T, control string, shift func(dev uint64, at int) uint64) {
	t.Helper()
	marker := filepath.Join(control, "transaction.json")
	tx, err := readTransactionFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	at := 0
	move := func(id string) string {
		if id == "" {
			return id
		}
		dev, ino, ok := strings.Cut(id, ":")
		n, err := strconv.ParseUint(dev, 10, 64)
		if !ok || err != nil {
			t.Fatalf("unexpected identity %q", id)
		}
		at++
		return fmt.Sprintf("%d:%s", shift(n, at), ino)
	}
	moveRecord := func(record *NativeRecord) {
		if record == nil {
			return
		}
		record.DirectoryID = move(record.DirectoryID)
		record.PreviousDirectoryID = move(record.PreviousDirectoryID)
		for i := range record.Published {
			record.Published[i].DirectoryID = move(record.Published[i].DirectoryID)
		}
	}
	for i := range tx.Files {
		for j := range tx.Files[i].Parents {
			tx.Files[i].Parents[j].Identity = move(tx.Files[i].Parents[j].Identity)
		}
	}
	moveRecord(tx.Before.Native)
	moveRecord(tx.After.Native)
	if tx.Native != nil {
		for j := range tx.Native.Parents {
			tx.Native.Parents[j].Identity = move(tx.Native.Parents[j].Identity)
		}
		tx.Native.StagedID = move(tx.Native.StagedID)
		moveRecord(&tx.Native.Before)
		moveRecord(&tx.Native.After)
		for k := range tx.Native.PurgeTrees {
			for rel, entry := range tx.Native.PurgeTrees[k].Entries {
				entry.ObjectID = move(entry.ObjectID)
				entry.Directory = move(entry.Directory)
				tx.Native.PurgeTrees[k].Entries[rel] = entry
			}
		}
	}
	if err := writeTransaction(marker, tx); err != nil {
		t.Fatal(err)
	}
}

func shiftAll(dev uint64, _ int) uint64 { return dev + 1 }

func interrupt(boundary string) func(string) error {
	return func(phase string) error {
		if phase == boundary {
			return fmt.Errorf("simulated interruption")
		}
		return nil
	}
}

func interruptedFileCommit(t *testing.T) (context.Context, Request, string) {
	t.Helper()
	ctx, r := request(t)
	target := filepath.Join(r.RuntimeRoot, "hook")
	r.Files = []File{{Path: target, Data: []byte("new"), Mode: 0755}}
	r.Fault = interrupt("transaction")
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("interruption did not stop the commit")
	}
	return ctx, r, target
}

func TestRenumberedVolumeRecoversInterruptedJournal(t *testing.T) {
	t.Run("file publication", func(t *testing.T) {
		ctx, r, target := interruptedFileCommit(t)
		renumberJournal(t, r.ControlRoot, shiftAll)
		if _, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true}); err != nil {
			t.Fatalf("renumbered volume blocked recovery: %v", err)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "new" {
			t.Fatalf("recovery did not publish: %q %v", data, err)
		}
	})
	t.Run("native publication", func(t *testing.T) {
		ctx, r, _, _ := installTwoNativeGenerations(t)
		change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		r.Fault = interrupt("transaction")
		if _, err := Commit(ctx, r); err == nil {
			t.Fatal("interruption did not stop the commit")
		}
		renumberNativeDevices(t, r.ControlRoot)
		renumberJournal(t, r.ControlRoot, shiftAll)
		recovered, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
		if err != nil {
			t.Fatalf("renumbered volume blocked native recovery: %v", err)
		}
		if recovered.Native.Path != change.After.Path {
			t.Fatalf("recovery did not select the new generation: %+v", recovered.Native)
		}
		if _, err := os.Stat(change.After.Path); err != nil {
			t.Fatal("recovery did not publish the new generation")
		}
	})
	t.Run("purge cleanup", func(t *testing.T) {
		ctx, r, _, second := installTwoNativeGenerations(t)
		r.RemoveConsumer, r.PurgeNative = true, true
		r.Fault = interrupt("ledger")
		if _, err := Commit(ctx, r); err == nil {
			t.Fatal("interruption did not stop the purge")
		}
		renumberJournal(t, r.ControlRoot, shiftAll)
		if _, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true}); err != nil {
			t.Fatalf("renumbered volume blocked purge recovery: %v", err)
		}
		entries, err := os.ReadDir(filepath.Dir(second.After.Path))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "generation-") || strings.Contains(entry.Name(), ".purged-") {
				t.Fatalf("purge recovery left %s", entry.Name())
			}
		}
	})
}

func TestRenumberedJournalStillRefusesChangedObjects(t *testing.T) {
	t.Run("substituted parent", func(t *testing.T) {
		ctx, r, _ := interruptedFileCommit(t)
		marker := filepath.Join(r.ControlRoot, "transaction.json")
		renumberJournal(t, r.ControlRoot, shiftAll)
		tx, err := readTransactionFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		last := &tx.Files[0].Parents[len(tx.Files[0].Parents)-1]
		dev, ino, _ := strings.Cut(last.Identity, ":")
		n, _ := strconv.ParseUint(ino, 10, 64)
		last.Identity = fmt.Sprintf("%s:%d", dev, n+1)
		if err := writeTransaction(marker, tx); err != nil {
			t.Fatal(err)
		}
		if _, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true}); err == nil {
			t.Fatal("recovery accepted a parent with a different inode")
		}
	})
	t.Run("inconsistent renumbering", func(t *testing.T) {
		ctx, r, _ := interruptedFileCommit(t)
		renumberJournal(t, r.ControlRoot, func(dev uint64, at int) uint64 { return dev + uint64(at) })
		if _, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true}); err == nil {
			t.Fatal("recovery accepted one volume renumbered to several devices")
		}
	})
}
