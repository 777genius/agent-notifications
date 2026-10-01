//go:build linux || darwin

package geminievent

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// This is a narrow private-file adapter to the existing installation lock.
// No setup, directory creation or durable event journal lives here.
func checkCacheRoot(root string) error {
	for p := root; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			return errors.New("unsafe_cache")
		}
		if p == root && info.Mode().Perm() != 0700 {
			return errors.New("unsafe_cache")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe_cache")
	}
	return nil
}

func readCache(root string) ([]byte, error) {
	fd, err := unix.Open(filepath.Join(root, "observations.json"), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "observations.json")
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size > cacheBytes {
		return nil, errors.New("unsafe_cache")
	}
	return io.ReadAll(io.LimitReader(f, cacheBytes+1))
}

func writeCache(root string, data []byte) error {
	path := filepath.Join(root, ".observations-"+uuid.NewString())
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(path, filepath.Join(root, "observations.json"))
}
