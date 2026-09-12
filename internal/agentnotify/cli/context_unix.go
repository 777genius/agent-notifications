//go:build linux || darwin

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var errContext = errors.New("context_unavailable")

// ReadSecureContext pins every path component without following symlinks. The
// final regular file must be owned by the effective user, private (0400 or 0600),
// singly linked and bounded. Lstat/Fstat identity and post-read metadata are checked.
// These checks establish safe local reading, never trusted provenance.
func ReadSecureContext(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || len(path) == 0 || len(path) > 4096 {
		return nil, errContext
	}
	absolute, e := filepath.Abs(path)
	if e != nil {
		return nil, errContext
	}
	parts := strings.Split(strings.TrimPrefix(absolute, "/"), "/")
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errContext
	}
	defer func() { _ = unix.Close(fd) }()
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, errContext
		}
		_ = unix.Close(fd)
		fd = next
	}
	name := parts[len(parts)-1]
	var before unix.Stat_t
	if unix.Fstatat(fd, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateFile(before) {
		return nil, errContext
	}
	opened, e := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, errContext
	}
	f := os.NewFile(uintptr(opened), "context")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(opened, &st) != nil || !privateFile(st) || st.Dev != before.Dev || st.Ino != before.Ino {
		return nil, errContext
	}
	info, e := f.Stat()
	if e != nil {
		return nil, errContext
	}
	b, e := io.ReadAll(io.LimitReader(f, ContextLimit+1))
	var after unix.Stat_t
	if e != nil || ctx.Err() != nil || len(b) > ContextLimit || unix.Fstat(opened, &after) != nil || (after.Dev != st.Dev || after.Ino != st.Ino || after.Mode != st.Mode || after.Uid != st.Uid || after.Gid != st.Gid || after.Nlink != st.Nlink || after.Size != st.Size) {
		return nil, errContext
	}
	final, e := f.Stat()
	if e != nil || !info.ModTime().Equal(final.ModTime()) {
		return nil, errContext
	}
	return b, nil
}
func privateFile(s unix.Stat_t) bool {
	return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Uid == uint32(os.Geteuid()) && s.Nlink == 1 && (s.Mode&07777 == 0400 || s.Mode&07777 == 0600) && s.Size >= 0 && s.Size <= ContextLimit
}
