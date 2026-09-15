//go:build linux || darwin

package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type storeParent struct {
	file *os.File
	path string
}

// openStoreParent walks the canonical physical path with directory descriptors.
// Every new entry is private and its containing directory is synced before use.
func openStoreParent(path string, create bool) (*storeParent, error) {
	if !filepath.IsAbs(path) {
		return nil, &Error{Code: ConfigInvalid, Path: path}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "/")
	current := "/"
	for _, name := range strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/") {
		if name == "" {
			continue
		}
		var parentStat unix.Stat_t
		if err = unix.Fstat(int(f.Fd()), &parentStat); err != nil {
			_ = f.Close()
			return nil, err
		}
		// Root-owned system paths and sticky temp roots are allowed. Writable
		// nonsticky ancestors allow another principal to exchange our boundary.
		if (parentStat.Uid != 0 && parentStat.Uid != uint32(os.Geteuid())) ||
			(parentStat.Mode&0022 != 0 && parentStat.Mode&unix.S_ISVTX == 0) {
			_ = f.Close()
			return nil, &Error{Code: ConfigPermissionDenied, Path: current}
		}
		next, e := unix.Openat(int(f.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) && create {
			e = unix.Mkdirat(int(f.Fd()), name, 0700)
			if e == nil {
				e = unix.Fchmodat(int(f.Fd()), name, 0700, 0)
				if e == nil {
					e = f.Sync()
				}
			} else if errors.Is(e, unix.EEXIST) {
				e = nil
			}
			if e == nil {
				next, e = unix.Openat(int(f.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		if e != nil {
			_ = f.Close()
			return nil, e
		}
		_ = f.Close()
		current = filepath.Join(current, name)
		f = os.NewFile(uintptr(next), current)
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		_ = f.Close()
		return nil, &Error{Code: ConfigPermissionDenied, Path: path}
	}
	return &storeParent{file: f, path: path}, nil
}
func (p *storeParent) close() { _ = p.file.Close() }
func (p *storeParent) verify() error {
	info, e := os.Stat(p.path)
	if e != nil {
		return e
	}
	held, e := p.file.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(info, held) {
		return &Error{Code: ConfigConflict, Path: p.path}
	}
	return nil
}
func (p *storeParent) lock(ctx context.Context, name string, shared bool, create bool) (func(), error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if !create {
		flags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	} else {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Openat(int(p.file.Fd()), name, flags, 0600)
	created := create && err == nil
	if create && errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(int(p.file.Fd()), name, flags&^(unix.O_CREAT|unix.O_EXCL), 0600)
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if created {
		if err = unix.Fchmod(fd, 0600); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		_ = f.Close()
		return nil, &Error{Code: ConfigLinkedPath, Path: filepath.Join(p.path, name)}
	}
	if reused, e := reusesStoreLock(ctx, f); e != nil {
		_ = f.Close()
		return nil, e
	} else if reused {
		_ = f.Close()
		return func() {}, nil
	}
	op := unix.LOCK_EX | unix.LOCK_NB
	if shared {
		op = unix.LOCK_SH | unix.LOCK_NB
	}
	for {
		if err = ctx.Err(); err != nil {
			_ = f.Close()
			return nil, &Error{Code: ConfigLockTimeout, Path: p.path}
		}
		err = unix.Flock(fd, op)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, &Error{Code: ConfigLockTimeout, Path: p.path}
		case <-time.After(10 * time.Millisecond):
		}
	}
	if create {
		if err = p.file.Sync(); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if !shared && create {
		if err = rememberStoreLock(ctx, f); err != nil {
			_ = f.Close()
			return nil, err
		}
	}

	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = f.Close() }, nil
}
func (p *storeParent) read(name string) (Snapshot, error) {
	fd, err := unix.Openat(int(p.file.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ELOOP) {
		return Snapshot{}, &Error{Code: ConfigLinkedPath}
	}
	if err != nil {
		return Snapshot{}, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return Snapshot{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return Snapshot{}, &Error{Code: ConfigInvalid}
	}
	if st.Nlink != 1 {
		return Snapshot{}, &Error{Code: ConfigLinkedPath}
	}
	if st.Uid != uint32(os.Geteuid()) {
		return Snapshot{}, &Error{Code: ConfigPermissionDenied}
	}
	if st.Size > MaxDocumentBytes {
		return Snapshot{}, &Error{Code: ConfigInvalid}
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return Snapshot{}, err
	}
	if len(data) > MaxDocumentBytes {
		return Snapshot{}, &Error{Code: ConfigInvalid}
	}
	return Snapshot{Bytes: data, PhysicalPath: filepath.Join(p.path, name)}, nil
}
func uniqueStoreName(base, kind string) (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base + "." + kind + "-" + hex.EncodeToString(b[:]), nil
}
func (p *storeParent) writeArtifact(base, kind string, data []byte) (string, error) {
	name, err := uniqueStoreName(base, kind)
	if err != nil {
		return "", err
	}
	fd, err := unix.Openat(int(p.file.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), name)
	if err = unix.Fchmod(fd, 0600); err != nil {
		_ = f.Close()
		_ = unix.Unlinkat(int(p.file.Fd()), name, 0)
		return "", err
	}
	err = finishArtifact(f, data)
	if err != nil {
		_ = unix.Unlinkat(int(p.file.Fd()), name, 0)
		return "", err
	}
	return name, nil
}
func (p *storeParent) publish(ctx context.Context, temp, name string, exists bool, original, replacement []byte) error {
	if err := p.verify(); err != nil {
		return err
	}
	fd := int(p.file.Fd())
	if exists {
		return unix.Renameat(fd, temp, fd, name)
	}
	return publishNewAt(fd, temp, name)
}
func (p *storeParent) sync() error         { return p.file.Sync() }
func (p *storeParent) discard(name string) { _ = unix.Unlinkat(int(p.file.Fd()), name, 0) }

func checkInitEntry(path string) error {
	var st unix.Stat_t
	if e := unix.Lstat(path, &st); e != nil {
		return e
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK || st.Nlink > 1 {
		return &Error{Code: ConfigLinkedPath, Path: path}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return &Error{Code: ConfigInvalid, Path: path}
	}
	return nil
}

func readInitSnapshot(ctx context.Context, path string) (Snapshot, error) {
	if e := checkInitEntry(path); e != nil {
		return Snapshot{}, e
	}
	snap, e := ReadFileSnapshot(path, MaxDocumentBytes)
	if e != nil {
		return Snapshot{}, e
	}
	if e = checkInitEntry(path); e != nil {
		return Snapshot{}, e
	}
	return snap, nil
}
