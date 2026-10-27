//go:build !linux && !darwin && !windows

package installruntime

import (
	"fmt"
	"os"
)

func configOnlyDirectory(string) (*os.File, error) {
	return nil, fmt.Errorf("config-only control observation unsupported on this platform")
}
func configOnlyEntryInfo(*os.File, string) (os.FileInfo, error) {
	return nil, fmt.Errorf("config-only control observation unsupported on this platform")
}
