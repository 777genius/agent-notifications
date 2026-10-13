package installruntime

import "fmt"

// One bijection covers the complete transaction, so separately valid chains
// cannot disagree about the same volume or collapse distinct recorded volumes.
// This is observation only: journal snapshots and purge inventories stay intact.
type deviceRenumbering struct{ devices, taken map[string]string }

func newDeviceRenumbering() deviceRenumbering {
	return deviceRenumbering{devices: map[string]string{}, taken: map[string]string{}}
}

func (m deviceRenumbering) add(stored, fresh []PathAnchor) bool {
	if len(fresh) < len(stored) {
		return false
	}
	for i, anchor := range stored {
		oldDev, oldIno, ok := splitObjectID(anchor.Identity)
		newDev, newIno, freshOK := splitObjectID(fresh[i].Identity)
		if !ok || !freshOK || anchor.Path != fresh[i].Path || oldIno != newIno {
			return false
		}
		if mapped, ok := m.devices[oldDev]; ok && mapped != newDev {
			return false
		}
		if historical, ok := m.taken[newDev]; ok && historical != oldDev {
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

func preflightTransactionAnchors(tx transaction) error {
	mapping := newDeviceRenumbering()
	check := func(path string, stored []PathAnchor) error {
		fresh, err := pathAnchors(path, false)
		if err != nil {
			return err
		}
		if !persistedDevRelaxed {
			return checkAnchors(stored, fresh)
		}
		if !mapping.add(stored, fresh) {
			return fmt.Errorf("transaction persisted parent device mapping changed")
		}
		return nil
	}
	for _, file := range tx.Files {
		if err := check(file.Path, file.Parents); err != nil {
			return err
		}
	}
	if tx.Native != nil {
		return check(tx.Native.After.Path, tx.Native.Parents)
	}
	return nil
}
