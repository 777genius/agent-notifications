//go:build linux || darwin

package setup

// Use the same descriptor-relative, persisted-directory identity check as the
// setup transaction. Darwin may renumber the device after remount/reboot,
// but the parent device and persisted inode must still match.
func inspectPersistedStageIdentity(rootPath, expected string) error {
	root, err := openRoot(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	stage, err := openChild(root, stageName)
	if err != nil {
		return err
	}
	defer func() { _ = stage.Close() }()
	if err := matches(stage, root, expected); err != nil {
		return err
	}
	// Exact private directories and permanent spool lock must satisfy the
	// same descriptor-relative checks that Apply uses when resuming.
	journalDir, err := openChild(stage, "journal")
	if err != nil {
		return err
	}
	_ = journalDir.Close()
	spool, err := openChild(stage, "native-spool")
	if err != nil {
		return err
	}
	defer func() { _ = spool.Close() }()
	return checkLock(spool)
}
