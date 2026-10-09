//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package installruntime

import (
	"context"
	"fmt"
	"os"
)

func tryLock(f *os.File) (bool, error) {
	return false, fmt.Errorf("managed installation kernel locking is unsupported on this platform")
}

func prepareLockedFile(f *os.File, writable bool) error {
	return fmt.Errorf("managed installation kernel locking is unsupported on this platform")
}

func openLock(path string, create bool) (*os.File, error) {
	return nil, fmt.Errorf("managed installation kernel locking is unsupported on this platform")
}

func privateDirectory(path string) error {
	return fmt.Errorf("private managed directory verification unsupported")
}

func ensureLockDirectory(ctx context.Context, path string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ensureDir(path, mode)
}
