//go:build linux || darwin

package setup

// Use the same descriptor-relative, persisted-directory identity check as the
// setup transaction. Darwin and Linux btrfs may renumber the device after remount/reboot,
// but the parent device and persisted inode must still match.
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
	if err := matches(state, root, expected); err != nil {
		return err
	}
	// Exact private directories and permanent spool lock must satisfy the
	// same descriptor-relative checks that Apply uses when resuming.
	journalDir, err := openChild(state, "journal")
	if err != nil {
		return err
	}
	// The journal decoder reads namespace/snapshot but does not open its
	// permanent lock. A damaged or publicly readable lock would make Apply
	// fail after preflight, so verify it with the same privateFile contract.
	journalLock, lockErr := privateFile(journalDir, "lock", 8*1024*1024)
	if lockErr == nil {
		lockErr = journalLock.Close()
	}
	_ = journalDir.Close()
	if lockErr != nil {
		return lockErr
	}
	spool, err := openChild(state, "native-spool")
	if err != nil {
		return err
	}
	defer func() { _ = spool.Close() }()
	return checkLock(spool)
}
