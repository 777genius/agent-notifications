//go:build linux || darwin

package clientsetup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Bounded descriptor-relative read only. The kernel supplies publication and CAS;
// its public Fingerprint API does not expose bounded document bytes.
func read(path string, limit int) ([]byte, installruntime.Identity, error) {
	var zero installruntime.Identity
	path, e := installruntime.PhysicalPath(path)
	if e != nil {
		return nil, zero, e
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, zero, e
	}
	parent := os.NewFile(uintptr(fd), "/")
	defer func() { parent.Close() }()
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/") {
		if part == "" {
			continue
		}
		n, e := unix.Openat(int(parent.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e == unix.ENOENT {
			return nil, zero, nil
		}
		if e != nil {
			return nil, zero, e
		}
		parent.Close()
		parent = os.NewFile(uintptr(n), part)
	}
	fd, e = unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e == unix.ENOENT {
		return nil, zero, nil
	}
	if e != nil {
		return nil, zero, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, zero, e
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, zero, fmt.Errorf("invalid or oversized document")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil {
		return nil, zero, e
	}
	if len(b) > limit {
		return nil, zero, fmt.Errorf("document limit exceeded")
	}
	h := sha256.Sum256(b)
	before := installruntime.Identity{Exists: true, SHA256: hex.EncodeToString(h[:]), Mode: uint32(info.Mode().Perm())}
	got, e := installruntime.Fingerprint(path)
	if e != nil {
		return nil, zero, e
	}
	if before != got {
		return nil, zero, ErrConflict
	}
	return b, before, nil
}

func requireDirectory(path string) error {
	if !clean(path) {
		return ErrConflict
	}
	path, e := installruntime.PhysicalPath(path)
	if e != nil {
		return e
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	parent := os.NewFile(uintptr(fd), "/")
	defer parent.Close()
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		n, e := unix.Openat(int(parent.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e == unix.ENOENT {
			return errDirectoryAbsent
		}
		if e != nil {
			return ErrConflict
		}
		parent.Close()
		parent = os.NewFile(uintptr(n), part)
	}
	return nil
}
