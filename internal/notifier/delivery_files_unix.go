//go:build darwin || linux

package notifier

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func openPrivate(path string, directory bool, write bool, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	if write {
		flags &^= unix.O_RDONLY
		flags |= unix.O_RDWR
	}
	if create {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	err = unix.Fstat(fd, &st)
	expected := uint32(unix.S_IFREG)
	mode := uint32(0600)
	if directory {
		expected = unix.S_IFDIR
		mode = 0700
	}
	if err != nil || st.Uid != uint32(os.Geteuid()) || uint32(st.Mode)&unix.S_IFMT != expected || uint32(st.Mode)&07777 != mode || (!directory && st.Nlink != 1) {
		unix.Close(fd)
		return nil, errors.New("unsafe owned path")
	}
	return os.NewFile(uintptr(fd), path), nil
}

func lockPrivate(ctx context.Context, path string, create bool) (func(), error) {
	f, err := openPrivate(path, false, true, create)
	if create && errors.Is(err, unix.EEXIST) {
		f, err = openPrivate(path, false, true, false)
	}
	if err != nil {
		return nil, err
	}
	for {
		if ctx.Err() != nil {
			f.Close()
			return nil, ctx.Err()
		}
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
