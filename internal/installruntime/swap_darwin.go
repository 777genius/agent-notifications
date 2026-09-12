package installruntime

import (
	"fmt"
	"golang.org/x/sys/unix"
	"path/filepath"
)

// swapBundles never falls back to two renames. Both names are resolved through
// one held no-follow parent, so ancestor substitution cannot redirect the swap.
func swapBundles(staged, live string, expected ...[]PathAnchor) error {
	if filepath.Dir(staged) != filepath.Dir(live) {
		return fmt.Errorf("native swap requires sibling directories")
	}
	parent, anchors, err := anchoredParent(live, false)
	if err != nil {
		return err
	}
	defer parent.Close()
	if len(expected) != 0 {
		if err := checkAnchors(expected[0], anchors); err != nil {
			return err
		}
	}
	return unix.RenameatxNp(int(parent.Fd()), filepath.Base(staged), int(parent.Fd()), filepath.Base(live), unix.RENAME_SWAP)
}

func renameNativeAt(parent int, from, to string) error {
	return unix.RenameatxNp(parent, from, parent, to, unix.RENAME_EXCL)
}
