//go:build darwin

package setup

import "os"

// Darwin keeps its own persisted-device renumber rule in installruntime.
func volumeID(*os.File) (string, error) {
	return "", nil
}
