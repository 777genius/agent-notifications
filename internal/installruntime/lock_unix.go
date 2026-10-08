//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package installruntime

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

// Open the final component without following links, then validate the descriptor
// before flock. Writable callers may adopt read-only public permissions only
// after acquiring the lock. Never unlink a stale lock or replace its inode.
func openLock(path string, create bool) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err = validateLockFile(f, create); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func validateLockFile(f *os.File, writable bool) error {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	mode := uint32(st.Mode) & 07777
	validMode := mode == 0600 || writable && mode&^uint32(0044) == 0600
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || !validMode {
		return fmt.Errorf("installation lock requires an owned private regular inode: %s", f.Name())
	}
	return nil
}

// Called with flock held and the named inode verified. Descriptor-based chmod
// preserves other holders' lock identity and never follows a substituted path.
func prepareLockedFile(f *os.File, writable bool) error {
	if err := validateLockFile(f, writable); err != nil {
		return err
	}
	if writable {
		if err := f.Chmod(0600); err != nil {
			return fmt.Errorf("restrict installation lock %s: %w", f.Name(), err)
		}
	}
	return validateLockFile(f, false)
}

func privateDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		return fmt.Errorf("control directory must be owned and private")
	}
	return nil
}

func ensureLockDirectory(ctx context.Context, path string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ensureDir(path, mode)
}
