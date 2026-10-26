package installruntime

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// A user-settings directory is not an installation. This exception observes
// only the exact global config names; any managed or unknown entry keeps the
// ordinary locked reader authoritative. It never reads policy or ledger bytes.
func configOnlyControlRoot(root string) (bool, error) {
	physical, err := PhysicalPath(root)
	if err != nil {
		return false, err
	}
	path := filepath.Join(physical, "config.json")
	anchors, err := pathAnchors(path, false)
	if err != nil {
		return false, err
	}
	if len(anchors) == 0 || anchors[len(anchors)-1].Path != physical {
		return false, ErrPolicyConflict
	}
	identity := anchors[len(anchors)-1].Identity
	before, err := configOnlyCensus(physical, identity)
	if err != nil || before == nil {
		return false, err
	}
	after, err := configOnlyCensus(physical, identity)
	if err != nil {
		return false, err
	}
	if len(before) != len(after) {
		return false, ErrPolicyConflict
	}
	for name, info := range before {
		current, ok := after[name]
		if !ok || !os.SameFile(info, current) || info.Mode() != current.Mode() {
			return false, ErrPolicyConflict
		}
	}
	current, err := pathAnchors(path, false)
	if err != nil {
		return false, err
	}
	if err = checkAnchors(anchors, current); err != nil {
		return false, err
	}
	if err = privateDirectory(physical); err != nil {
		return false, err
	}
	// A managed writer publishes this lock before its ownership/policy state.
	// Refuse if it appeared while checking the otherwise config-only census.
	if _, err = os.Lstat(filepath.Join(physical, ".component-install.lock")); !os.IsNotExist(err) {
		if err == nil {
			err = ErrPolicyConflict
		}
		return false, err
	}
	return true, nil
}

func configOnlyCensus(root, identity string) (map[string]os.FileInfo, error) {
	directory, err := configOnlyDirectory(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	actual, err := openedDirectoryIdentity(directory)
	if err != nil {
		return nil, err
	}
	if actual != identity {
		return nil, ErrPolicyConflict
	}
	// Only two entries are admissible. A third proves this is not config-only
	// without allocating a census proportional to an arbitrary control tree.
	entries, err := directory.ReadDir(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > 2 {
		return nil, nil
	}
	result := map[string]os.FileInfo{}
	for _, entry := range entries {
		if entry.Name() != "config.json" && entry.Name() != "config.json.lock" {
			return nil, nil
		}
		info, err := configOnlyEntryInfo(directory, entry.Name())
		if err != nil {
			return nil, err
		}
		result[entry.Name()] = info
	}
	if result["config.json"] == nil {
		return nil, nil
	}
	return result, nil
}

func absentPolicySnapshot() PolicySnapshot {
	return PolicySnapshot{Policy: UserPolicy{SchemaVersion: 1}, Fields: map[string]json.RawMessage{}}
}
