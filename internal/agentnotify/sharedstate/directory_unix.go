//go:build linux || darwin

package sharedstate

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

type directory struct{ file *os.File }

func (d *directory) close() { _ = d.file.Close() }

func openDirectory(s *Store, create bool) (*directory, []string, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	current := os.NewFile(uintptr(fd), "/")
	fail := func(err error) (*directory, []string, error) { _ = current.Close(); return nil, nil, err }
	for _, part := range strings.Split(strings.TrimPrefix(s.root, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fail(err)
		}
		_ = current.Close()
		current = os.NewFile(uintptr(next), part)
	}
	var ids []string
	for i, name := range []string{"", ".shared-mcp", filepath.Base(s.dir)} {
		if i > 0 {
			next, err := unix.Openat(int(current.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err == unix.ENOENT && create {
				err = unix.Mkdirat(int(current.Fd()), name, 0700)
				if err == nil || err == unix.EEXIST {
					next, err = unix.Openat(int(current.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				}
			}
			if err != nil {
				return fail(err)
			}
			_ = current.Close()
			current = os.NewFile(uintptr(next), name)
		}
		var st unix.Stat_t
		if err = unix.Fstat(int(current.Fd()), &st); err != nil {
			return fail(err)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
			return fail(fmt.Errorf("directory is not owned and private"))
		}
		if err = checkACL(int(current.Fd())); err != nil {
			return fail(err)
		}
		id := fmt.Sprintf("%d:%d", st.Dev, st.Ino)
		ids = append(ids, id)
		if len(s.ids) > i && s.ids[i] != id {
			return fail(fmt.Errorf("directory identity changed"))
		}
	}
	return &directory{file: current}, ids, nil
}

func (d *directory) open(name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(d.file.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err == nil {
		if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&07777 != 0600 {
			err = fmt.Errorf("file is not an owned private single-link inode")
		}
	}
	if err == nil {
		err = checkACL(fd)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func (d *directory) ensureLock(name string) error {
	f, err := d.open(name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL)
	if err == unix.EEXIST {
		return d.checkLock(name)
	}
	if err != nil {
		return err
	}
	return f.Close()
}
func (d *directory) checkLock(name string) error {
	f, err := d.open(name, unix.O_RDWR)
	if err != nil {
		return err
	}
	return f.Close()
}
func (d *directory) read(name string, limit int64) ([]byte, error) {
	f, err := d.open(name, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("endpoint too large")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("endpoint too large")
	}
	return data, nil
}
func (d *directory) write(name string, data []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := ".endpoint-" + hex.EncodeToString(nonce[:])
	f, err := d.open(temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Unlinkat(int(d.file.Fd()), temp, 0) }()
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Publication visibility needs no durable policy/journal fsync.
	return unix.Renameat(int(d.file.Fd()), temp, int(d.file.Fd()), name)
}
func (d *directory) remove(name string) error { return unix.Unlinkat(int(d.file.Fd()), name, 0) }
