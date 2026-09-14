//go:build darwin || linux

package setup

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Descriptor-relative traversal excludes symlinks, special files, foreign owners
// and writable ancestors. Setup never chmods/adopts an existing foreign path.
func openRoot(path string) (*os.File, error) {
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, name := range parts {
		next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil {
			_ = unix.Close(fd)
			return nil, e
		}
		own := st.Uid == uint32(os.Geteuid())
		trusted := own || st.Uid == 0
		if !trusted || (st.Mode&0022 != 0 && st.Mode&unix.S_ISVTX == 0) || (i == len(parts)-1 && (!own || st.Mode&07777 != 0700)) {
			_ = unix.Close(fd)
			return nil, fmt.Errorf("unsafe setup directory")
		}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func syncOpenedDir(f *os.File) error { return f.Sync() }
func openChild(parent *os.File, name string) (*os.File, error) {
	fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		_ = f.Close()
		return nil, e
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		_ = f.Close()
		return nil, fmt.Errorf("unsafe private directory %s", name)
	}
	return f, nil
}
func mkdir(parent *os.File, name string) (*os.File, error) {
	if e := unix.Mkdirat(int(parent.Fd()), name, 0700); e != nil {
		return nil, e
	}
	if e := parent.Sync(); e != nil {
		return nil, e
	}
	return openChild(parent, name)
}
func absent(parent *os.File, name string) error {
	var st unix.Stat_t
	e := unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if e == unix.ENOENT {
		return nil
	}
	if e == nil {
		return fmt.Errorf("path already exists")
	}
	return e
}
func directoryID(f *os.File) (string, error) {
	var st unix.Stat_t
	e := unix.Fstat(int(f.Fd()), &st)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), e
}
func matches(f *os.File, want string) error {
	id, e := directoryID(f)
	if e != nil {
		return e
	}
	if id != want {
		return fmt.Errorf("owned directory identity changed")
	}
	return nil
}
func createLock(dir *os.File) error {
	fd, e := unix.Openat(int(dir.Fd()), ".spool.lock", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), ".spool.lock")
	e = f.Sync()
	_ = f.Close()
	if e != nil {
		return e
	}
	return dir.Sync()
}
func checkLock(dir *os.File) error {
	fd, e := unix.Openat(int(dir.Fd()), ".spool.lock", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return e
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 {
		return fmt.Errorf("unsafe existing spool lock")
	}
	return nil
}
func privateFile(parent *os.File, name string, limit int64) (*os.File, error) {
	fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		_ = f.Close()
		return nil, e
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size > limit {
		_ = f.Close()
		return nil, fmt.Errorf("unsafe private file %s", name)
	}
	return f, nil
}
