//go:build linux || darwin

package portable

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Hold every parent while walking; no symlink component is traversed.
func directory(path string) (*os.File, error) {
	if len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrInvalid
	}
	resolved, err := installruntime.PhysicalPath(path)
	if err != nil {
		return nil, ErrInvalid
	}
	path = resolved
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "/")
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(int(f.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		f.Close()
		if e != nil {
			return nil, e
		}
		f = os.NewFile(uintptr(next), part)
	}
	return f, nil
}
func physicalDirectory(path string) error {
	f, err := directory(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return ErrInvalid
	}
	return nil
}
func openFile(parent, name string, private bool) (*os.File, error) {
	p, err := directory(parent)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	var st unix.Stat_t
	if unix.Fstat(int(p.Fd()), &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 || (private && st.Mode&0077 != 0) {
		return nil, ErrInvalid
	}
	fd, err := unix.Openat(int(p.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&07022 != 0 || (private && (st.Mode&0777 != 0600 || st.Size > MaxBytes)) {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}
func readPrivate(parent, name string) ([]byte, error) {
	f, err := openFile(parent, name, true)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if len(raw) > MaxBytes {
		return nil, ErrInvalid
	}
	return raw, err
}
func checkPrimary(path string) error {
	f, err := openFile(filepath.Dir(path), filepath.Base(path), false)
	if err != nil {
		return err
	}
	return f.Close()
}

func writePrivate(parent, name string, data []byte) error {
	if len(data) == 0 || len(data) > MaxBytes || !selector.MatchString(name) {
		return ErrInvalid
	}
	if err := physicalDirectory(parent); err != nil {
		return err
	}
	dir, err := directory(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	tmp := "." + name + ".tmp"
	_ = unix.Unlinkat(int(dir.Fd()), tmp, 0)
	fd, err := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrInvalid
	}
	f := os.NewFile(uintptr(fd), tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = unix.Unlinkat(int(dir.Fd()), tmp, 0)
		return err
	}
	if closeErr != nil {
		_ = unix.Unlinkat(int(dir.Fd()), tmp, 0)
		return closeErr
	}
	if err := unix.Linkat(int(dir.Fd()), tmp, int(dir.Fd()), name, 0); err != nil {
		_ = unix.Unlinkat(int(dir.Fd()), tmp, 0)
		if err == unix.EEXIST {
			return errExists
		}
		return ErrInvalid
	}
	_ = unix.Unlinkat(int(dir.Fd()), tmp, 0)
	return nil
}

func removePrivate(parent, name string) error {
	if !selector.MatchString(name) {
		return ErrInvalid
	}
	dir, err := directory(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil && err != unix.ENOENT {
		return ErrInvalid
	}
	return nil
}
