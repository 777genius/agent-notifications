//go:build !windows

package config

import "os"

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
