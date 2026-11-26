//go:build darwin

package installruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Synthetic persisted dev numbers exercise real no-follow Darwin syscalls. No
// devices are mounted here; real volume boundaries are covered by macOS E2E.
func renumberID(id string) string {
	dev, ino, ok := splitObjectID(id)
	if !ok {
		return id
	}
	// Deterministic injective mapping keeps the original volume grouping.
	if strings.HasPrefix(dev, "-") {
		return "8" + strings.TrimPrefix(dev, "-") + ":" + ino
	}
	return "9" + dev + ":" + ino
}
func renumberRecord(n *NativeRecord) {
	if n == nil {
		return
	}
	n.DirectoryID = renumberID(n.DirectoryID)
	n.PreviousDirectoryID = renumberID(n.PreviousDirectoryID)
	for i := range n.Published {
		n.Published[i].DirectoryID = renumberID(n.Published[i].DirectoryID)
	}
}
func rewriteRenumberLedger(t *testing.T, root string) Ledger {
	t.Helper()
	l, err := readLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	renumberRecord(l.Native)
	if err = writeJSON(filepath.Join(root, "ownership.json"), l); err != nil {
		t.Fatal(err)
	}
	return l
}
func assertFreshNativeIDs(t *testing.T, l Ledger) {
	t.Helper()
	if l.Native == nil {
		return
	}
	check := func(path, id string) {
		if path == "" {
			return
		}
		current, err := nativeDirectoryID(path)
		if err != nil {
			t.Fatal(err)
		}
		if current != id {
			t.Fatalf("stale identity %s: %s want %s", path, id, current)
		}
	}
	check(l.Native.Path, l.Native.DirectoryID)
	check(l.Native.PreviousPath, l.Native.PreviousDirectoryID)
	for _, g := range l.Native.Published {
		check(g.Path, g.DirectoryID)
	}
}
func TestRenumberCommitRefreshAndReselect(t *testing.T) {
	ctx, r := request(t)
	original := nativeFixture(t)
	var err error
	r.Native, err = StageNative(ctx, r.ControlRoot, original)
	if err != nil {
		t.Fatal(err)
	}
	r.Files, err = NativeAlias(r.Native, r.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := first.Native.Path
	newer := nativeFixture(t)
	asset := filepath.Join(newer, "Contents", "MacOS", "terminal-notifier-modern")
	if err = os.WriteFile(asset, []byte("new inert callback"), 0755); err != nil {
		t.Fatal(err)
	}
	rewriteRenumberLedger(t, r.ControlRoot)
	r.Native, err = StageNative(ctx, r.ControlRoot, newer)
	if err != nil {
		t.Fatal(err)
	}
	r.Files, err = NativeAlias(r.Native, r.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	assertFreshNativeIDs(t, second)
	secondPath := second.Native.Path
	if second.Native.PreviousPath != firstPath {
		t.Fatal("previous callback path changed")
	}
	rewriteRenumberLedger(t, r.ControlRoot)
	if _, err = ReadInstalledSnapshot(r.ControlRoot); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(r.RuntimeRoot, "ClaudeNotifier.app")
	aliasBefore, err := os.Readlink(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = nil
	r.Files = nil
	unchanged, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	assertFreshNativeIDs(t, unchanged)
	aliasAfter, err := os.Readlink(aliasPath)
	if err != nil || aliasAfter != aliasBefore {
		t.Fatal("identity refresh changed alias", err)
	}
	if unchanged.Native.Path != secondPath || unchanged.Native.PreviousPath != firstPath {
		t.Fatal("refresh changed generation paths")
	}
	rewriteRenumberLedger(t, r.ControlRoot)
	r.Native, err = StageNative(ctx, r.ControlRoot, original)
	if err != nil {
		t.Fatal(err)
	}
	r.Files, err = NativeAlias(r.Native, r.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	assertFreshNativeIDs(t, selected)
	if selected.Native.Path != firstPath {
		t.Fatalf("reselect changed published path: %s want %s", selected.Native.Path, firstPath)
	}
	rewriteRenumberLedger(t, r.ControlRoot)
	r.Native = nil
	r.Files = nil
	r.RemoveConsumer = true
	r.PurgeNative = true
	purged, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if purged.Native != nil {
		t.Fatal("native not purged")
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("published callback remains: %s %v", path, err)
		}
	}
}

func rewriteRenumberTransaction(t *testing.T, root string) transaction {
	t.Helper()
	marker := filepath.Join(root, "transaction.json")
	tx, err := readTransactionFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	renumberRecord(tx.Before.Native)
	renumberRecord(tx.After.Native)
	if n := tx.Native; n != nil {
		renumberRecord(&n.Before)
		renumberRecord(&n.After)
		n.StagedID = renumberID(n.StagedID)
		for i := range n.Parents {
			n.Parents[i].Identity = renumberID(n.Parents[i].Identity)
		}
		for i := range n.PurgeTrees {
			for rel, e := range n.PurgeTrees[i].Entries {
				e.Directory = renumberID(e.Directory)
				e.ObjectID = renumberID(e.ObjectID)
				n.PurgeTrees[i].Entries[rel] = e
			}
		}
	}
	for i := range tx.Files {
		for j := range tx.Files[i].Parents {
			tx.Files[i].Parents[j].Identity = renumberID(tx.Files[i].Parents[j].Identity)
		}
	}
	if err = writeTransaction(marker, tx); err != nil {
		t.Fatal(err)
	}
	return tx
}

// These faults prove retry idempotence after a refreshed ledger was durably
// written while transaction.json still contains the old device numbers.
func TestRenumberRecoveryLedgerRetry(t *testing.T) {
	for _, mode := range []string{"forward", "forward-ledger", "rollback", "rollback-published", "purge", "purge-partial"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r := request(t)
			var err error
			r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Commit(ctx, r); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "purge") {
				r.Native = nil
				r.RemoveConsumer = true
				r.PurgeNative = true
			} else {
				source := nativeFixture(t)
				if err = os.WriteFile(filepath.Join(source, "new"), []byte("new inert data"), 0600); err != nil {
					t.Fatal(err)
				}
				r.Native, err = StageNative(ctx, r.ControlRoot, source)
				if err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(r.RuntimeRoot, "config")
			if !strings.HasPrefix(mode, "purge") {
				r.Files = []File{{Path: target, Data: []byte("new"), Mode: 0600}}
			}
			r.Fault = func(phase string) error {
				boundary := "transaction"
				if mode == "forward-ledger" {
					boundary = "ledger"
				}
				if mode == "rollback-published" {
					boundary = "native"
				}
				if mode == "purge-partial" && strings.HasPrefix(phase, "purge-entry:") {
					return fmt.Errorf("crash")
				}
				if mode != "purge-partial" && phase == boundary {
					return fmt.Errorf("crash")
				}
				return nil
			}
			if _, err = Commit(ctx, r); err == nil {
				t.Fatal("transaction fault missing")
			}
			tx := rewriteRenumberTransaction(t, r.ControlRoot)
			rewriteRenumberLedger(t, r.ControlRoot)
			marker := filepath.Join(r.ControlRoot, "transaction.json")
			ledgerFault := func(phase string) error {
				if phase == "ledger" {
					return fmt.Errorf("ledger crash")
				}
				return nil
			}
			if strings.HasPrefix(mode, "rollback") {
				r.RollbackPending = true
				r.Fault = ledgerFault
				if _, err = Commit(ctx, r); err == nil {
					t.Fatal("rollback ledger fault missing")
				}
				tx, err = readTransactionFile(marker)
				if err != nil {
					t.Fatal(err)
				}
			}
			journal, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				current, err := readLedger(r.ControlRoot)
				if err != nil {
					t.Fatal(err)
				}
				if err = recoverTransaction(ctx, r.ControlRoot, current, tx, ledgerFault); err == nil || err.Error() != "ledger crash" {
					t.Fatalf("retry %d: %v", attempt, err)
				}
				after, err := readLedger(r.ControlRoot)
				if err != nil {
					t.Fatal(err)
				}
				assertFreshNativeIDs(t, after)
				again, err := os.ReadFile(marker)
				if err != nil || !reflect.DeepEqual(journal, again) {
					t.Fatal("recovery rewrote journal before-images", err)
				}
			}
			result, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			assertFreshNativeIDs(t, result)
			disk, err := readLedger(r.ControlRoot)
			if err != nil || !reflect.DeepEqual(result, disk) {
				t.Fatal("returned ledger differs from durable ledger", err)
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("journal not retired", err)
			}
			if strings.HasPrefix(mode, "purge") && result.Native != nil {
				t.Fatal("purge recovery retained native")
			}
			if strings.HasPrefix(mode, "forward") {
				b, err := os.ReadFile(target)
				if err != nil || string(b) != "new" {
					t.Fatal("file promotion not recovered", err)
				}
			}
		})
	}
}

func TestRenumberJournalEqualityRejectsOtherChanges(t *testing.T) {
	ctx, r := request(t)
	var err error
	r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Ledger
	if err = json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	renumberRecord(snapshot.Native)
	if !ledgerMatchesJournal(l, snapshot, nil) {
		t.Fatal("refreshed after-image rejected")
	}
	snapshot.Enabled = !snapshot.Enabled
	if ledgerMatchesJournal(l, snapshot, nil) {
		t.Fatal("unrelated ledger edit accepted")
	}
}

func TestRenumberLedgerRefreshKeepsJournalAndMissingPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "published.app")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	current, err := nativeDirectoryID(path)
	if err != nil {
		t.Fatal(err)
	}
	stored := renumberID(current)
	missing := filepath.Join(root, "missing.app")
	journal := Ledger{Native: &NativeRecord{Path: path, DirectoryID: stored, PreviousPath: path, PreviousDirectoryID: stored, Published: []NativeGeneration{{Path: path, DirectoryID: stored}, {Path: missing, DirectoryID: "123:456"}}}}
	before, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := refreshLedgerIdentities(journal)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Native.DirectoryID != current || refreshed.Native.PreviousDirectoryID != current || refreshed.Native.Published[0].DirectoryID != current {
		t.Fatal("not all present identities refreshed")
	}
	if refreshed.Native.Published[1].Path != missing || refreshed.Native.Published[1].DirectoryID != "123:456" {
		t.Fatal("missing generation changed")
	}
	after, err := json.Marshal(journal)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("journal mutated during refresh", err)
	}
	if !ledgerMatchesJournal(refreshed, journal, nil) {
		t.Fatal("refresh rejected its own after-image")
	}
	for _, published := range [][]NativeGeneration{nil, {}} {
		l := Ledger{Native: &NativeRecord{Published: published}}
		refreshed, err := refreshLedgerIdentities(l)
		if err != nil || !reflect.DeepEqual(l, refreshed) {
			t.Fatal("nil/empty publication changed", err)
		}
	}
}

func TestRenumberRetiredInventoryRetry(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "active.app")
	retired := filepath.Join(root, ".candidate-retired")
	for _, path := range []string{active, retired} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	aid, err := nativeDirectoryID(active)
	if err != nil {
		t.Fatal(err)
	}
	rid, err := nativeDirectoryID(retired)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := capturePurgeTree(retired)
	if err != nil {
		t.Fatal(err)
	}
	journal := Ledger{Native: &NativeRecord{Path: active, DirectoryID: renumberID(aid), Published: []NativeGeneration{{Path: retired, DirectoryID: renumberID(rid)}}}}
	current, err := refreshLedgerIdentities(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(retired, retired+"-saved"); err != nil {
		t.Fatal(err)
	}
	if ledgerMatchesJournal(current, journal, nil) {
		t.Fatal("unrecorded disappearance normalized")
	}
	cleanup := &NativeChange{Retire: true, PurgeTrees: []PurgeTree{{Path: retired, Entries: entries}}}
	before, _ := json.Marshal(journal)
	if !ledgerMatchesJournal(current, journal, cleanup) {
		t.Fatal("refreshed/deleted private inventory rejected its after-image")
	}
	after, _ := json.Marshal(journal)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("historical identity alignment changed journal")
	}
	_, ino, _ := splitObjectID(rid)
	foreignDev := "777777"
	if foreignDev == objectDevice(rid) {
		foreignDev = "888888"
	}
	current.Native.Published[0].DirectoryID = foreignDev + ":" + ino
	if ledgerMatchesJournal(current, journal, cleanup) {
		t.Fatal("historical cross-volume identity accepted")
	}
	current.Native.Published[0].DirectoryID = "1:0"
	if ledgerMatchesJournal(current, journal, cleanup) {
		t.Fatal("historical inode substitution accepted")
	}
	current.Native.Published[0].DirectoryID = rid
	if err = os.Mkdir(retired, 0700); err != nil {
		t.Fatal(err)
	}
	if ledgerMatchesJournal(current, journal, cleanup) {
		t.Fatal("replaced retired directory accepted")
	}
}
