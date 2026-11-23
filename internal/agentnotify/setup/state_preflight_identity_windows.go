//go:build windows

package setup

// The native Windows identity is volume serial and file index. Reuse the
// existing non-reparse, descriptor-scoped comparison used by provision.
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
	return matches(stage, expected)
}
