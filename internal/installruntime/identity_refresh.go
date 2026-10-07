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
	// Capture correspondence from ORIGINAL persisted IDs while live inodes can
	// still prove it. Normalization would erase the intermediate boot's device.
	var currentDevices, snapshotDevices deviceCorrespondence
	if cleanup != nil && cleanup.Retire {
		var ok bool
		currentDevices, ok = nativeDeviceCorrespondence(current.Native)
		if !ok {
			return false
		}
		snapshotDevices, ok = nativeDeviceCorrespondence(snapshot.Native)
		if !ok || !snapshotDevices.addAnchors(cleanup.Parents, cleanup.After.Path) {
			return false
		}
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
		alignRetiredIdentities(current.Native, snapshot.Native, cleanup.PurgeTrees, currentDevices, snapshotDevices)
	}
	return reflect.DeepEqual(current, snapshot)
}

// A correspondence is a bijection: both splits and merges of volume groups
// are refused. Its keys must come from surviving no-follow native observations
// or persisted parent anchors, never from the deleted inventory being reconciled.
type deviceCorrespondence map[string]string

func (m deviceCorrespondence) add(stored, observed string) bool {
	sd, si, sok := splitObjectID(stored)
	od, oi, ook := splitObjectID(observed)
	if !sok || !ook || si != oi {
		return false
	}
	if mapped, ok := m[sd]; ok && mapped != od {
		return false
	}
	for historical, mapped := range m {
		if mapped == od && historical != sd {
			return false
		}
	}
	m[sd] = od
	return true
}

func (m deviceCorrespondence) addAnchors(stored []PathAnchor, path string) bool {
	if len(stored) == 0 {
		return true
	}
	observed, err := pathAnchors(path, false)
	if err != nil || !matchPersistedAnchorChain(stored, observed) {
		return false
	}
	for i, anchor := range stored {
		if !m.add(anchor.Identity, observed[i].Identity) {
			return false
		}
	}
	return true
}

func nativeDeviceCorrespondence(record *NativeRecord) (deviceCorrespondence, bool) {
	m := deviceCorrespondence{}
	if record == nil {
		return m, true
	}
	observe := func(path, id string) bool {
		if path == "" || id == "" {
			return true
		}
		current, parentDev, err := nativeDirectoryIdentity(path)
		if os.IsNotExist(err) || err == nil && current == "" {
			return true
		}
		return err == nil && MatchPersistedDirectory(id, current, parentDev) && m.add(id, current)
	}
	if !observe(record.Path, record.DirectoryID) || !observe(record.PreviousPath, record.PreviousDirectoryID) {
		return nil, false
	}
	for _, gen := range record.Published {
		if !observe(gen.Path, gen.DirectoryID) {
			return nil, false
		}
	}
	return m, true
}

// A legacy candidate can remain in Published after retirement. If deletion
// completed, there is no inode left to refresh. Only manifest-proven absence
// and device groups independently proven by survivors may align its IDs.
func alignRetiredIdentities(current, snapshot *NativeRecord, trees []PurgeTree, currentDevices, snapshotDevices deviceCorrespondence) {
	if current == nil || snapshot == nil || len(current.Published) != len(snapshot.Published) {
		return
	}
	for i, want := range snapshot.Published {
		got := current.Published[i]
		if want.Path != got.Path || want.DirectoryID == got.DirectoryID || !strings.HasPrefix(filepath.Base(want.Path), ".candidate-") {
			continue
		}
		wd, wi, wok := splitObjectID(want.DirectoryID)
		gd, gi, gok := splitObjectID(got.DirectoryID)
		if !wok || !gok || wi != gi {
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
		if parentDev == "" || currentDevices[gd] != parentDev || snapshotDevices[wd] != parentDev {
			continue
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			continue
		}
		for _, tree := range trees {
			entry, ok := tree.Entries["."]
			_, ino, valid := splitObjectID(entry.Directory)
			if !ok || !valid || ino != wi || tree.Path != want.Path {
				continue
			}
			// The manifest may belong to either persisted boot. Every entry's
			// device must have independent correspondence to this parent volume.
			valid = true
			for rel, entry := range tree.Entries {
				if filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					valid = false
					break
				}
				id := entry.Directory
				if id != "" {
					if entry.ObjectID != "" || entry.File != (Identity{}) {
						valid = false
						break
					}
				} else {
					id = entry.ObjectID
					if rel == "." || !entry.File.Exists || entry.File.Link != "" {
						valid = false
						break
					}
				}
				dev, _, ok := splitObjectID(id)
				cd, cok := currentDevices[dev]
				sd, sok := snapshotDevices[dev]
				if !ok || (!cok && !sok) || (cok && cd != parentDev) || (sok && sd != parentDev) {
					valid = false
					break
				}
			}
			if valid {
				snapshot.Published[i].DirectoryID = got.DirectoryID
			}
		}
	}
}
