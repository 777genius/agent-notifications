//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package installruntime

import (
	"fmt"
	"os"
)

func tryLock(f *os.File) (bool, error) {
	return false, fmt.Errorf("managed installation kernel locking is unsupported on this platform")
}

func openLock(path string, create bool) (*os.File, error) {
	return nil, fmt.Errorf("managed installation kernel locking is unsupported on this platform")
}

func privateDirectory(path string) error {
	return fmt.Errorf("private managed directory verification unsupported")
}
