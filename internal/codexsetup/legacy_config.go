package codexsetup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Defaults are independent mutable user state, not a runtime transaction asset.
// Publish after successful installation under the same config-only lock used by
// managed writers. A concurrent non-cooperating editor also wins via Link.
func createLegacyDefaults(ctx context.Context, source, destination string) error {
	path := filepath.Join(destination, "config", "config.json")
	parent, err := installruntime.CanonicalPath(filepath.Dir(path))
	if err != nil {
		return err
	}
	if parent != filepath.Dir(path) {
		return fmt.Errorf("legacy config directory redirects outside its installation; preserving it")
	}
	unlock, err := installruntime.Lock(ctx, path+".lock")
	if err != nil {
		return err
	}
	defer unlock()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	rel := filepath.Join("config", "config.json")
	if _, err := root.Lstat(rel); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer sourceRoot.Close()
	data, err := sourceRoot.ReadFile(rel)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// The permanent config lock creates the parent directory. Confine temporary
	// writes and publication to the opened installation, including parent races.
	configRoot, err := root.OpenRoot("config")
	if err != nil {
		return err
	}
	defer configRoot.Close()
	// CreateTemp's path is not used for publication; exclusive relative creation
	// below avoids following a substituted destination parent.
	for attempt := 0; attempt < 100; attempt++ {
		name := fmt.Sprintf(".config-defaults-%d-%d", os.Getpid(), attempt)
		file, err := configRoot.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		defer configRoot.Remove(name)
		_, err = file.Write(data)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		err = configRoot.Link(name, "config.json")
		if os.IsExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// Match the common kernel: directory fsync is unavailable on Windows.
		if runtime.GOOS == "windows" {
			return nil
		}
		dir, err := configRoot.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return fmt.Errorf("legacy config default staging busy; retry setup")
}
