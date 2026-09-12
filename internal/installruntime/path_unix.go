//go:build linux || darwin

package installruntime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// anchoredParent walks from the filesystem root using held directory descriptors.
// No intermediate link is followed, including when a parent is concurrently
// replaced. Publication stays relative to the directory actually opened.
func anchoredParent(path string, create bool) (*os.File, []PathAnchor, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, fmt.Errorf("managed path must be absolute and clean: %s", path)
	}
	path, err := platformAnchorPath(path)
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), "/")
	var anchors []PathAnchor
	parent := ""
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/") {
		if part == "" {
			continue
		}
		parent += "/" + part
		next, e := unix.Openat(int(f.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e == unix.ENOENT && create {
			e = unix.Mkdirat(int(f.Fd()), part, 0700)
			if e == nil {
				e = f.Sync()
			}
			if e == nil || e == unix.EEXIST {
				next, e = unix.Openat(int(f.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		if e != nil {
			f.Close()
			return nil, anchors, e
		}
		f.Close()
		f = os.NewFile(uintptr(next), parent)
		var st unix.Stat_t
		if e = unix.Fstat(next, &st); e != nil {
			f.Close()
			return nil, anchors, e
		}
		anchors = append(anchors, PathAnchor{Path: parent, Identity: fmt.Sprintf("%d:%d", st.Dev, st.Ino)})
	}
	return f, anchors, nil
}
func anchoredFingerprint(parent *os.File, name string) (Identity, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return Identity{}, nil
		}
		return Identity{}, err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		b := make([]byte, 65536)
		n, err := unix.Readlinkat(int(parent.Fd()), name, b)
		if err != nil {
			return Identity{}, err
		}
		if n == len(b) {
			return Identity{}, fmt.Errorf("link too long")
		}
		return Identity{Exists: true, Link: string(b[:n])}, nil
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return Identity{}, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Identity{}, err
	}
	if !info.Mode().IsRegular() {
		return Identity{}, fmt.Errorf("non-regular managed file: %s", name)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return Identity{}, err
	}
	return identity(data, uint32(info.Mode().Perm())), nil
}
func safeFingerprint(path string) (Identity, error) {
	parent, _, err := anchoredParent(path, false)
	if os.IsNotExist(err) {
		return Identity{}, nil
	}
	if err != nil {
		return Identity{}, err
	}
	defer parent.Close()
	return anchoredFingerprint(parent, filepath.Base(path))
}
func safePublish(file File, cas bool) error {
	parent, anchors, err := anchoredParent(file.Path, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err = checkAnchors(file.Parents, anchors); err != nil {
		return err
	}
	name := filepath.Base(file.Path)
	if cas {
		got, e := anchoredFingerprint(parent, name)
		if e != nil {
			return e
		}
		if got == desired(file) {
			return nil
		}
		if got != file.Before {
			return fmt.Errorf("concurrent edit: %s", file.Path)
		}
	}
	if file.Remove {
		err = unix.Unlinkat(int(parent.Fd()), name, 0)
		if err != nil && err != unix.ENOENT {
			return err
		}
		return parent.Sync()
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".runtime-" + hex.EncodeToString(random[:])
	defer unix.Unlinkat(int(parent.Fd()), temp, 0)
	if file.Link != "" {
		err = unix.Symlinkat(file.Link, int(parent.Fd()), temp)
	} else {
		var fd int
		fd, err = unix.Openat(int(parent.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), temp)
		err = f.Chmod(os.FileMode(file.Mode))
		if err == nil {
			_, err = f.Write(file.Data)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return err
	}
	if err = unix.Renameat(int(parent.Fd()), temp, int(parent.Fd()), name); err != nil {
		return err
	}
	return parent.Sync()
}
func pathAnchors(path string, create bool) ([]PathAnchor, error) {
	f, a, err := anchoredParent(path, create)
	if f != nil {
		f.Close()
	}
	if !create && os.IsNotExist(err) {
		err = nil
	}
	return a, err
}

func safeRemoveDirectory(path, want string, anchors []PathAnchor) error {
	parent, got, err := anchoredParent(path, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	if err = checkAnchors(anchors, got); err != nil {
		return err
	}
	var st unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || fmt.Sprintf("%d:%d", st.Dev, st.Ino) != want {
		return fmt.Errorf("purge directory replaced: %s", path)
	}
	if err = unix.Unlinkat(int(parent.Fd()), filepath.Base(path), unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return parent.Sync()
}

func openedDirectoryIdentity(f *os.File) (string, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("expected directory handle")
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}

func readRegularFile(path string) ([]byte, error) {
	return readRegularFileLimit(path, maxManagedFile)
}

func readControlDocument(path string) ([]byte, error) {
	return readRegularFileLimit(path, maxControlDocument)
}

func readRegularFileLimit(path string, limit int64) ([]byte, error) {
	parent, _, err := anchoredParent(path, false)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed input must be a regular non-link file")
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("managed input exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("managed input exceeds size limit")
	}
	return data, nil
}

func regularObjectID(path string) (string, error) {
	parent, _, err := anchoredParent(path, false)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	var st unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return "", fmt.Errorf("expected regular file identity")
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}

func removePhysicalDirectory(path string) error {
	parent, _, err := anchoredParent(path, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return fmt.Errorf("transaction blob directory is not a physical directory: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name == "." || name == ".." {
			continue
		}
		if err := unix.Unlinkat(int(f.Fd()), name, 0); err != nil && err != unix.ENOENT {
			return err
		}
	}
	if err := unix.Unlinkat(int(parent.Fd()), filepath.Base(path), unix.AT_REMOVEDIR); err != nil && err != unix.ENOENT {
		return err
	}
	return nil
}
