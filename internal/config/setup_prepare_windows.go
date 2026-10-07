//go:build windows

package config

import "os"

// Windows cannot FlushFileBuffers a directory handle. The temp file is already
// synced before Link/Rename; flush the published file the same way the Windows
// store does after ReplaceFileW.
func syncPreparedPublication(root *os.Root, name string) error {
	// FlushFileBuffers requires GENERIC_WRITE; Root.Open is read-only.
	f, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
