//go:build !linux && !darwin && !windows

package installruntime

import (
	"fmt"
	"os"
)

func safeFingerprint(path string) (Identity, error) {
	return Identity{}, fmt.Errorf("confined managed file operations unsupported on this platform: %s", path)
}
func safePublish(file File, cas bool) error {
	return fmt.Errorf("confined managed file operations unsupported on this platform: %s", file.Path)
}
func pathAnchors(path string, create bool) ([]PathAnchor, error) {
	return nil, fmt.Errorf("confined managed file operations unsupported on this platform: %s", path)
}
func safeRemoveDirectory(path, want string, anchors []PathAnchor) error {
	return fmt.Errorf("confined purge unsupported on this platform")
}

func openedDirectoryIdentity(f *os.File) (string, error) {
	return "", fmt.Errorf("directory identity unsupported on this platform")
}

func readRegularFile(path string) ([]byte, error) {
	return nil, fmt.Errorf("confined file reads unsupported on this platform")
}

func readControlDocument(path string) ([]byte, error) {
	return nil, fmt.Errorf("confined file reads unsupported on this platform")
}

func regularObjectID(path string) (string, error) {
	return "", fmt.Errorf("regular file identity unsupported on this platform")
}

func removePhysicalDirectory(path string) error {
	return fmt.Errorf("confined directory cleanup unsupported on this platform: %s", path)
}
