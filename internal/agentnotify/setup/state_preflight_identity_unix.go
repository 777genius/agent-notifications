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
	return matches(stage, root, expected)
}
