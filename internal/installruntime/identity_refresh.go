package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Refresh a detached ledger, never journal records or their before-images.
// Missing generations keep their durable identity (not an invitation to adopt
// a replacement). Paths and all non-identity fields remain immutable.
func refreshLedgerIdentities(l Ledger) (Ledger, error) {
	if !persistedDevRelaxed || l.Native == nil {
		return l, nil
	}
	record := *l.Native
	record.Published = append([]NativeGeneration(nil), record.Published...)
	// Keep nil vs empty slices significant for journal equality.
	if l.Native.Published != nil && record.Published == nil {
		record.Published = []NativeGeneration{}
	}
	l.Native = &record
	refresh := func(path string, id *string) error {
		if path == "" || *id == "" {
			return nil
		}
		current, parentDev, err := nativeDirectoryIdentity(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if current == "" {
			return nil
		}
		if !MatchPersistedDirectory(*id, current, parentDev) {
			return fmt.Errorf("native directory inode changed: %s", path)
		}
		*id = current
		return nil
	}
	if err := refresh(record.Path, &record.DirectoryID); err != nil {
		return l, err
	}
	if err := refresh(record.PreviousPath, &record.PreviousDirectoryID); err != nil {
		return l, err
	}
	for i := range record.Published {
		if err := refresh(record.Published[i].Path, &record.Published[i].DirectoryID); err != nil {
			return l, err
		}
	}
	return l, nil
}

// Recovery can crash after writing a refreshed ledger but before retiring its
// unchanged journal. Compare detached, disk-validated normal forms so that the
// transaction accepts its own durable after-image on every retry. All other
// ledger fields still require exact equality.
func ledgerMatchesJournal(current, snapshot Ledger, cleanup *NativeChange) bool {
	if reflect.DeepEqual(current, snapshot) {
		return true
	}
	if !persistedDevRelaxed {
		return false
	}
	current, err := refreshLedgerIdentities(current)
	if err != nil {
		return false
	}
	snapshot, err = refreshLedgerIdentities(snapshot)
	if err != nil {
		return false
	}
	if cleanup != nil && cleanup.Retire {
		alignRetiredIdentities(current.Native, snapshot.Native, cleanup.PurgeTrees)
	}
	return reflect.DeepEqual(current, snapshot)
}

// A legacy private candidate can also appear in the retained inventory. Recovery
// may refresh its ID, delete it, then crash before retiring the journal. There is
// no live inode left to normalize on retry. Reconcile only this historical field,
// using the durable deletion manifest, an absent leaf, the same inode and the
// current physical parent's device. An unrecorded disappearance cannot qualify.
func alignRetiredIdentities(current, snapshot *NativeRecord, trees []PurgeTree) {
	if current == nil || snapshot == nil || len(current.Published) != len(snapshot.Published) {
		return
	}
	for i, want := range snapshot.Published {
		got := current.Published[i]
		if want.Path != got.Path || want.DirectoryID == got.DirectoryID || !strings.HasPrefix(filepath.Base(want.Path), ".candidate-") {
			continue
		}
		for _, tree := range trees {
			entry, ok := tree.Entries["."]
			if !ok || tree.Path != want.Path || entry.Directory == "" {
				continue
			}
			if _, err := os.Lstat(want.Path); !os.IsNotExist(err) {
				continue
			}
			path, err := platformAnchorPath(want.Path)
			if err != nil {
				continue
			}
			anchors, err := pathAnchors(path, false)
			if err != nil || len(anchors) == 0 || anchors[len(anchors)-1].Path != filepath.Dir(path) {
				continue
			}
			parentDev := objectDevice(anchors[len(anchors)-1].Identity)
			if matchPersistedObject(want.DirectoryID, got.DirectoryID, parentDev) && matchPersistedObject(entry.Directory, got.DirectoryID, parentDev) {
				snapshot.Published[i].DirectoryID = got.DirectoryID
			}
		}
	}
}
