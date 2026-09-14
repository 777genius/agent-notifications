package config

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// migrateConfig publishes legacy config only if stable is absent. Even a
// malformed stable file belongs to its writer and must never be overwritten.
func migrateConfig(oldPath, stablePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := installruntime.Lock(ctx, stablePath+".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Lstat(stablePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(stablePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, "config-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return err
	}
	if err := os.Link(tmpPath, stablePath); err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	return nil
}
