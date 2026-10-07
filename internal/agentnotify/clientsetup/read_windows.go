//go:build windows

package clientsetup

import (
	"os"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func read(path string, limit int) ([]byte, installruntime.Identity, error) {
	var zero installruntime.Identity
	if limit <= 0 {
		return nil, zero, ErrConflict
	}
	path, err := installruntime.PhysicalPath(path)
	if err != nil {
		return nil, zero, err
	}
	data, got, err := installruntime.ReadConfinedDocument(path, int64(limit))
	if err != nil {
		return nil, zero, err
	}
	if !got.Exists {
		return nil, zero, nil
	}
	id, err := installruntime.Fingerprint(path)
	if err != nil {
		return nil, zero, err
	}
	if got != id {
		return nil, zero, ErrConflict
	}
	return data, got, nil
}

func requireDirectory(path string) error {
	if !clean(path) {
		return ErrConflict
	}
	path, err := installruntime.PhysicalPath(path)
	if err != nil {
		return err
	}
	err = installruntime.ConfinedDirectory(path)
	if os.IsNotExist(err) {
		return errDirectoryAbsent
	}
	if err != nil {
		return ErrConflict
	}
	return nil
}
