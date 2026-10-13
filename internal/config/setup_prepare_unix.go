//go:build !windows

package config

import (
	"context"
	"os"
)

func prepareGlobalConfigParent(path string) error {
	return prepareGlobalConfigParentPhysical(path)
}

func prepareGlobalConfig(ctx context.Context, canonicalPath, legacyPath, defaultsPath string) (PrepareGlobalResult, error) {
	return prepareGlobalConfigPhysical(ctx, canonicalPath, legacyPath, defaultsPath)
}

func inspectPreparedCanonical(string) error { return nil }

func syncPreparedPublication(root *os.Root, _ string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}
