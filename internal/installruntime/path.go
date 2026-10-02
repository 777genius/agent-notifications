package installruntime

import (
	"fmt"
	"strings"
)

const (
	// Operator-editable control JSON shares the 64 KiB budget used by
	// decodeUserPolicy. ReadPolicySnapshot must reject oversized documents
	// before allocating the file.
	maxControlDocument = 64 * 1024
	// Native helper hashing and recovery copies of managed files. This is a
	// hard cap against unbounded ReadAll, not the JSON document budget.
	maxManagedFile = 32 << 20
)

// PathAnchor binds a staged destination's existing parents to their opened
// filesystem identities. Recovery must not reinterpret a substituted directory.
type PathAnchor struct{ Path, Identity string }

// PhysicalPath rewrites only Darwin root-owned /var, /tmp, and /etc aliases.
// Arbitrary user-directory symlinks are not canonicalized.
func PhysicalPath(path string) (string, error) {
	return platformAnchorPath(path)
}

func checkAnchors(want, got []PathAnchor) error {
	for i, a := range want {
		if i >= len(got) || a != got[i] {
			return fmt.Errorf("managed destination parent substituted: %s", a.Path)
		}
	}
	return nil
}

// splitDeviceIdentity parses a Unix device:inode identity. Windows identities
// carry three fields and never parse, so device renumbering stays Unix-only.
func splitDeviceIdentity(id string) (dev, ino string, ok bool) {
	dev, ino, ok = strings.Cut(id, ":")
	return dev, ino, ok && dev != "" && ino != "" && !strings.Contains(ino, ":")
}

// deviceRenumbering maps device numbers recorded before a reboot onto the
// current ones. Each recorded device must map to exactly one current device and
// back, so a chain whose mount structure changed never matches.
type deviceRenumbering struct{ devices, taken map[string]string }

func newDeviceRenumbering() deviceRenumbering {
	return deviceRenumbering{devices: map[string]string{}, taken: map[string]string{}}
}

// add accepts a recorded parent chain only when the fresh one names the same
// paths and inodes under a consistent device mapping.
func (m deviceRenumbering) add(stored, fresh []PathAnchor) bool {
	if len(fresh) < len(stored) {
		return false
	}
	for i, a := range stored {
		oldDev, oldIno, ok := splitDeviceIdentity(a.Identity)
		if !ok {
			return false
		}
		newDev, newIno, ok := splitDeviceIdentity(fresh[i].Identity)
		if !ok || a.Path != fresh[i].Path || oldIno != newIno {
			return false
		}
		if d, seen := m.devices[oldDev]; seen && d != newDev {
			return false
		}
		if d, seen := m.taken[newDev]; seen && d != oldDev {
			return false
		}
		m.devices[oldDev], m.taken[newDev] = newDev, oldDev
	}
	return true
}

func (m deviceRenumbering) renumbered() bool {
	for oldDev, newDev := range m.devices {
		if oldDev != newDev {
			return true
		}
	}
	return false
}

func (m deviceRenumbering) rekey(id string) string {
	dev, ino, ok := splitDeviceIdentity(id)
	if newDev, known := m.devices[dev]; ok && known {
		return newDev + ":" + ino
	}
	return id
}

// rebindRenumberedJournal re-keys a journal interrupted before a reboot that
// renumbered the volumes, so recovery can finish. A journal whose parent chains
// do not all match under one renumbering is left for the strict checks.
func rebindRenumberedJournal(tx *transaction) error {
	m := newDeviceRenumbering()
	for _, f := range tx.Files {
		fresh, err := pathAnchors(f.Path, false)
		if err != nil {
			return err
		}
		if !m.add(f.Parents, fresh) {
			return nil
		}
	}
	if tx.Native != nil && len(tx.Native.Parents) != 0 {
		fresh, err := pathAnchors(tx.Native.After.Path, false)
		if err != nil {
			return err
		}
		if !m.add(tx.Native.Parents, fresh) {
			return nil
		}
	}
	if !m.renumbered() {
		return nil
	}
	for i := range tx.Files {
		for j := range tx.Files[i].Parents {
			tx.Files[i].Parents[j].Identity = m.rekey(tx.Files[i].Parents[j].Identity)
		}
	}
	if tx.Native != nil {
		for j := range tx.Native.Parents {
			tx.Native.Parents[j].Identity = m.rekey(tx.Native.Parents[j].Identity)
		}
		for k := range tx.Native.PurgeTrees {
			for rel, entry := range tx.Native.PurgeTrees[k].Entries {
				entry.ObjectID, entry.Directory = m.rekey(entry.ObjectID), m.rekey(entry.Directory)
				tx.Native.PurgeTrees[k].Entries[rel] = entry
			}
		}
	}
	return nil
}
