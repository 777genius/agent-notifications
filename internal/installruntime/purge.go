package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// PurgeEntry persists individual ownership before the first deletion. Directory
// identities survive renames and reject replacement even by an identical tree.
type PurgeEntry struct {
	ObjectID  string
	Directory string
	File      Identity
}
type PurgeTree struct {
	Path    string
	Entries map[string]PurgeEntry
}

func capturePurgeTree(path string) (map[string]PurgeEntry, error) {
	var pathErr error
	path, pathErr = platformAnchorPath(path)
	if pathErr != nil {
		return nil, pathErr
	}
	entries := map[string]PurgeEntry{}
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, e error) error {
		if os.IsNotExist(e) && p == path {
			return nil
		}
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(path, p)
		if e != nil {
			return e
		}
		var entry PurgeEntry
		if d.IsDir() {
			anchors, e := pathAnchors(filepath.Join(p, ".identity"), false)
			if e != nil {
				return e
			}
			if len(anchors) == 0 {
				return fmt.Errorf("missing purge directory identity")
			}
			entry.Directory = anchors[len(anchors)-1].Identity
		} else {
			entry.ObjectID, e = regularObjectID(p)
			if e != nil {
				return e
			}
			entry.File, e = Fingerprint(p)
			if e != nil {
				return e
			}
			afterID, e := regularObjectID(p)
			if e != nil {
				return e
			}
			if afterID != entry.ObjectID {
				return fmt.Errorf("purge file inode changed during capture")
			}
			if !entry.File.Exists || entry.File.Link != "" {
				return fmt.Errorf("unsupported purge entry: %s", p)
			}
		}
		entries[rel] = entry
		return nil
	})
	return entries, err
}
func preparePurge(change *NativeChange) error {
	if change == nil || (!change.Purge && !change.Retire) {
		return nil
	}
	if err := checkNativeDirectoryID(change.Before.Path, change.Before.DirectoryID); err != nil {
		return err
	}
	if err := checkNativeDirectoryID(change.Before.PreviousPath, change.Before.PreviousDirectoryID); err != nil {
		return err
	}
	pairs := [][3]string{{change.Before.PreviousPath, change.Before.PreviousPath, change.Before.PreviousSHA256}}
	if change.Purge {
		pairs = append(pairs, [3]string{change.Before.Path, change.Staged, change.Before.SHA256})
		for _, gen := range change.Before.Published {
			if gen.Path == "" || gen.Path == change.Before.Path || gen.Path == change.Before.PreviousPath {
				continue
			}
			pairs = append(pairs, [3]string{gen.Path, gen.Path, gen.SHA256})
		}
	}
	for _, pair := range pairs {
		if pair[0] == "" {
			continue
		}
		before, err := treeFingerprint(pair[0])
		if err != nil {
			return err
		}
		if before != pair[2] {
			return fmt.Errorf("purge source changed")
		}
		entries, err := capturePurgeTree(pair[0])
		if err != nil {
			return err
		}
		after, err := treeFingerprint(pair[0])
		if err != nil {
			return err
		}
		if after != before {
			return fmt.Errorf("purge source changed while recording entries")
		}
		change.PurgeTrees = append(change.PurgeTrees, PurgeTree{pair[1], entries})
	}
	return nil
}
func cleanupPurgeTree(tree PurgeTree, fault func(string) error) error {
	var pathErr error
	tree.Path, pathErr = platformAnchorPath(tree.Path)
	if pathErr != nil {
		return pathErr
	}
	current, err := capturePurgeTree(tree.Path)
	if err != nil {
		return err
	}
	for rel, entry := range current {
		if want, ok := tree.Entries[rel]; !ok || !reflect.DeepEqual(want, entry) {
			return fmt.Errorf("purge entry changed; preserving for inspection: %s", filepath.Join(tree.Path, rel))
		}
	}
	names := make([]string, 0, len(current))
	for rel := range current {
		names = append(names, rel)
	}
	// Children sort before their containing directories, with root last.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, rel := range names {
		p := filepath.Join(tree.Path, rel)
		anchors, err := pathAnchors(p, false)
		if err != nil {
			return err
		}
		for _, anchor := range anchors {
			sub, e := filepath.Rel(tree.Path, anchor.Path)
			if e == nil {
				if want, ok := tree.Entries[sub]; ok && want.Directory != anchor.Identity {
					return fmt.Errorf("purge parent replaced: %s", anchor.Path)
				}
			}
		}
		entry := current[rel]
		if entry.Directory != "" {
			err = safeRemoveDirectory(p, entry.Directory, anchors)
		} else {
			err = safePublish(File{Path: p, Before: entry.File, Remove: true, Parents: anchors, WindowsReplacementID: entry.ObjectID}, true)
		}
		if err != nil {
			return err
		}
		if fault != nil {
			if err = fault("purge-entry:" + p); err != nil {
				return err
			}
		}
	}
	return nil
}
