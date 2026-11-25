//go:build windows

package setup

import (
	"os"

	"golang.org/x/sys/windows"
)

// The native Windows identity is volume serial and file index. Reuse the
// existing non-reparse, descriptor-scoped comparison used by provision.
func inspectPersistedStateIdentity(rootPath, name, expected string) error {
	root, err := openRoot(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	state, err := openChild(root, name)
	if err != nil {
		return err
	}
	defer func() { _ = state.Close() }()
	if err := matches(state, expected); err != nil {
		return err
	}
	journalDir, err := openChild(state, "journal")
	if err != nil {
		return err
	}
	// The journal decoder reads namespace/snapshot but does not open its
	// permanent lock. A damaged or publicly readable lock would make Apply
	// fail after preflight, so verify it with the same privateFile contract.
	lockErr := inspectPermanentLock(journalDir, "lock")
	_ = journalDir.Close()
	if lockErr != nil {
		return lockErr
	}
	spool, err := openChild(state, "native-spool")
	if err != nil {
		return err
	}
	defer func() { _ = spool.Close() }()
	return inspectPermanentLock(spool, ".spool.lock")
}

// Share writes with existing consumer handles without granting the inspector
// write access or permitting replacement of the permanent lock.
func inspectPermanentLock(dir *os.File, name string) error {
	f, err := privateFileWithShare(dir, name, 8*1024*1024, windows.FILE_SHARE_WRITE)
	if err != nil {
		return err
	}
	return f.Close()
}
