//go:build linux || darwin

package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Open every component physically. A trusted root must already exist, be owned
// by this user and mode 0700. Ancestors may be owned by root or this user, and
// writable ancestors are refused except trusted-owner sticky directories (/tmp).
// All subsequent operations are relative to the pinned directory descriptor.
func openRoot(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, ErrInvalid
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		next, e := unix.Openat(fd, p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, wrap(e)
		}
		fd = next
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil {
			_ = unix.Close(fd)
			return nil, e
		}
		last := i == len(parts)-1
		uid := uint32(os.Geteuid())
		safeOwner := st.Uid == uid || st.Uid == 0
		unsafeWrite := st.Mode&0022 != 0 && (!safeOwner || st.Mode&unix.S_ISVTX == 0)
		if !safeOwner || unsafeWrite || (last && (st.Uid != uid || st.Mode&0777 != 0700)) {
			_ = unix.Close(fd)
			return nil, ErrRepair
		}
	}
	return os.NewFile(uintptr(fd), path), nil
}
func openFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, e := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		_ = unix.Close(fd)
		return nil, e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0777 != 0600 {
		_ = unix.Close(fd)
		return nil, ErrRepair
	}
	return os.NewFile(uintptr(fd), name), nil
}
func lock(ctx context.Context, dir *os.File, create bool) (*os.File, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	flags := unix.O_RDWR
	if create {
		flags |= unix.O_CREAT
	}
	f, e := openFile(dir, "lock", flags)
	if e != nil {
		return nil, wrap(e)
	}
	for {
		if e = ctx.Err(); e != nil {
			_ = f.Close()
			return nil, e
		}
		e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if e != unix.EAGAIN && e != unix.EWOULDBLOCK {
			_ = f.Close()
			return nil, e
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	// Never accept a detached/replaced lock inode.
	var a, b unix.Stat_t
	e = unix.Fstat(int(f.Fd()), &a)
	if e == nil {
		e = unix.Fstatat(int(dir.Fd()), "lock", &b, unix.AT_SYMLINK_NOFOLLOW)
	}
	if e != nil || a.Ino != b.Ino || a.Dev != b.Dev || a.Nlink != 1 {
		_ = f.Close()
		return nil, ErrRepair
	}
	return f, nil // close releases kernel flock, including process death
}
func readBounded(dir *os.File, name string, max int) ([]byte, error) {
	f, e := openFile(dir, name, unix.O_RDONLY)
	if e != nil {
		return nil, wrap(e)
	}
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if st.Size() > int64(max) {
		return nil, ErrRepair
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > max {
		return nil, ErrRepair
	}
	return b, nil
}
func (s *Store) read(dir *os.File) (*disk, error) {
	ns, e := readBounded(dir, "namespace", 65)
	if e != nil {
		return nil, e
	}
	if len(ns) != 65 || ns[64] != '\n' || !isHex(string(ns[:64])) {
		return nil, ErrRepair
	}
	b, e := readBounded(dir, "journal.json", s.limits.Bytes)
	if e != nil {
		return nil, e
	}
	if e = strictJSON(b); e != nil {
		return nil, wrap(e)
	}
	var d disk
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&d); e != nil {
		return nil, wrap(e)
	}
	canonical, e := json.Marshal(d)
	if e != nil {
		return nil, wrap(e)
	}
	var compact bytes.Buffer
	if e = json.Compact(&compact, b); e != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return nil, ErrRepair
	}
	if d.Namespace != string(ns[:64]) || (s.namespace != "" && s.namespace != d.Namespace) {
		return nil, ErrRepair
	}
	if e = d.validate(s); e != nil {
		return nil, e
	}
	return &d, nil
}
func (s *Store) transaction(ctx context.Context, fn func(*disk) (bool, error)) error {
	dir, e := openRoot(s.root)
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	l, e := lock(ctx, dir, false)
	if e != nil {
		return e
	}
	defer func() { _ = l.Close() }()
	d, e := s.read(dir)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	changed, e := fn(d)
	if changed {
		if writeErr := s.write(ctx, dir, d); writeErr != nil {
			return writeErr
		}
	}
	if e != nil {
		return e
	}
	return nil
}
func (s *Store) fail(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}
func (s *Store) write(ctx context.Context, dir *os.File, d *disk) error {
	if e := d.validate(s); e != nil {
		return e
	}
	// Inputs and record count are bounded before encoding. Check encoded size
	// before opening the same-filesystem temporary file.
	records := d.Records
	d.Records = map[string]Record{}
	header, e := json.Marshal(d)
	d.Records = records
	if e != nil {
		return e
	}
	size := len(header)
	for key, r := range records {
		entry, err := json.Marshal(r)
		if err != nil {
			return err
		}
		size += len(key) + 3 + len(entry) + 1
		if size > s.limits.Bytes {
			return ErrFull
		}
	}
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if len(b) > s.limits.Bytes {
		return ErrFull
	}
	if e = s.fail("before_temp"); e != nil {
		return e
	}
	// One fixed, validated orphan slot bounds disk use after repeated crashes.
	old, e := openFile(dir, "snapshot.tmp", unix.O_RDONLY)
	if e == nil {
		_ = old.Close()
		if e = unix.Unlinkat(int(dir.Fd()), "snapshot.tmp", 0); e != nil {
			return e
		}
	} else if !errors.Is(e, unix.ENOENT) {
		return e
	}
	f, e := openFile(dir, "snapshot.tmp", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	defer func() { _ = unix.Unlinkat(int(dir.Fd()), "snapshot.tmp", 0) }()
	half := len(b) / 2
	if _, e = f.Write(b[:half]); e != nil {
		return e
	}
	if e = s.fail("partial_write"); e != nil {
		return e
	}
	if _, e = f.Write(b[half:]); e != nil {
		return e
	}
	if e = s.fail("before_file_sync"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = s.fail("after_file_sync"); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = s.fail("before_rename"); e != nil {
		return e
	}
	if e = unix.Renameat(int(dir.Fd()), "snapshot.tmp", int(dir.Fd()), "journal.json"); e != nil {
		return e
	}
	if e = s.fail("after_rename"); e != nil {
		return e
	}
	if e = dir.Sync(); e != nil {
		return e
	}
	return s.fail("after_directory_sync")
}
func (s *Store) bootstrap(ctx context.Context) error {
	dir, e := openRoot(s.root)
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	// Refuse existing or interrupted state before allowing lock creation.
	// In particular, Initialize must not recreate a lost expected lock inode.
	for _, name := range []string{"namespace", "journal.json", "snapshot.tmp"} {
		var st unix.Stat_t
		err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			return ErrRepair
		}
		if !errors.Is(err, unix.ENOENT) {
			return wrap(err)
		}
	}
	l, e := lock(ctx, dir, true)
	if e != nil {
		return e
	}
	defer func() { _ = l.Close() }()
	names, e := dir.Readdirnames(-1)
	if e != nil {
		return e
	}
	for _, n := range names {
		if n != "lock" {
			return ErrRepair
		}
	}
	ns, e := token()
	if e != nil {
		return e
	}
	f, e := openFile(dir, "namespace", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	if _, e = f.WriteString(ns + "\n"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = dir.Sync(); e != nil {
		return e
	}
	if e = s.fail("after_namespace_sync"); e != nil {
		return e
	}
	s.namespace = ns
	d := &disk{Version: 1, Namespace: ns, Limits: s.limits, Records: map[string]Record{}, Events: []event{}}
	if _, e = s.advance(d); e != nil {
		return e
	}
	return s.write(ctx, dir, d)
}
