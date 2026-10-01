//go:build !windows

package geminiinstall

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
	"golang.org/x/sys/unix"
)

// Delivery reads the document only for UAP's owned-group verification. It never
// fingerprints the whole native document or records foreign settings.
func readNativeSettings(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("absolute native settings path required")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	defer unix.Close(fd)
	leaf, err := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(leaf), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > geminihooks.MaxSettingsBytes {
		return nil, errors.New("bounded native settings required")
	}
	data, err := io.ReadAll(io.LimitReader(f, geminihooks.MaxSettingsBytes+1))
	if err != nil || len(data) == 0 || len(data) > geminihooks.MaxSettingsBytes {
		return nil, errors.New("native settings unavailable")
	}
	return data, nil
}
