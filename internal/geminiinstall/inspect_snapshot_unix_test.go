//go:build !windows

package geminiinstall

import (
	"io/fs"
	"os"
)

func inspectPathInfo(path string) (fs.FileInfo, string, error) {
	info, err := os.Lstat(path)
	return info, "", err
}
