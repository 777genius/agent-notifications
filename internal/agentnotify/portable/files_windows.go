//go:build windows

package portable

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func physicalDirectory(path string) error {
	resolved, err := installruntime.PhysicalPath(path)
	if err != nil {
		return ErrInvalid
	}
	if err = installruntime.ConfinedDirectory(resolved); err != nil {
		return ErrInvalid
	}
	if err = installruntime.CheckPrivateControlRoot(resolved); err != nil {
		return ErrInvalid
	}
	return nil
}

func readPrivate(parent, name string) ([]byte, error) {
	if !selector.MatchString(name) {
		return nil, ErrInvalid
	}
	if err := physicalDirectory(parent); err != nil {
		return nil, err
	}
	resolved, err := installruntime.PhysicalPath(parent)
	if err != nil {
		return nil, ErrInvalid
	}
	data, id, err := installruntime.ReadConfinedDocument(filepath.Join(resolved, name), MaxBytes)
	if err != nil || !id.Exists {
		return nil, ErrInvalid
	}
	return data, nil
}

func checkPrimary(path string) error {
	resolved, err := installruntime.PhysicalPath(path)
	if err != nil {
		return ErrInvalid
	}
	id, err := installruntime.Fingerprint(resolved)
	if err != nil || !id.Exists || id.Link != "" {
		return ErrInvalid
	}
	return nil
}

func writePrivate(parent, name string, data []byte) error {
	if len(data) == 0 || len(data) > MaxBytes || !selector.MatchString(name) {
		return ErrInvalid
	}
	if err := physicalDirectory(parent); err != nil {
		return err
	}
	resolved, err := installruntime.PhysicalPath(parent)
	if err != nil {
		return ErrInvalid
	}
	err = installruntime.WriteConfinedExclusive(resolved, name, data)
	if errors.Is(err, os.ErrExist) {
		return errExists
	}
	if err != nil {
		return ErrInvalid
	}
	return nil
}

func removePrivate(parent, name string) error {
	if !selector.MatchString(name) {
		return ErrInvalid
	}
	resolved, err := installruntime.PhysicalPath(parent)
	if err != nil {
		return ErrInvalid
	}
	if err = installruntime.RemoveConfinedName(resolved, name); err != nil {
		return ErrInvalid
	}
	return nil
}
