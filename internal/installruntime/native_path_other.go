//go:build !linux && !darwin

package installruntime

import (
	"fmt"
	"os"
)

func renameNative(from, to string, expected []PathAnchor, sourceID ...string) error {
	return fmt.Errorf("native bundle promotion requires a supported native platform")
}

func openNativeStageLock(root *os.Root) (*os.File, error) {
	return nil, fmt.Errorf("native bundle staging requires a supported native platform")
}
