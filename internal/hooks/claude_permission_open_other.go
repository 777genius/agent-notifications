//go:build !linux && !darwin

package hooks

import "os"

// Windows has no POSIX FIFO at ordinary transcript paths. The caller still
// verifies that the opened handle is the original regular file.
func openClaudePermissionTranscript(path string) (*os.File, error) {
	return os.Open(path)
}
