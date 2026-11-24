//go:build darwin

package desktopinstall

import (
	"context"
	"errors"
	"fmt"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// PrepareNativeSpoolFor shares only the no-follow private-directory preparation.
// Each trusted observer retains its own namespace and permanent spool lock.
func PrepareNativeSpoolFor(ctx context.Context, controlRoot string, product DesktopProduct) (string, error) {
	id, err := identityFor(product)
	if err != nil {
		return "", err
	}
	if err := installruntime.CheckPrivateControlRoot(controlRoot); err != nil {
		return "", err
	}
	release, err := installruntime.LockExisting(ctx, filepath.Join(controlRoot, ".component-install.lock"))
	if err != nil {
		return "", err
	}
	defer release()
	rootFD, err := unix.Open(controlRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(rootFD)
	name := id.spool
	if err := unix.Mkdirat(rootFD, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return "", err
	}
	spoolFD, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(spoolFD)
	var st unix.Stat_t
	if err := unix.Fstat(spoolFD, &st); err != nil {
		return "", err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		return "", fmt.Errorf("%s spool must be an owned private directory", id.label)
	}
	spool := filepath.Join(controlRoot, name)
	lockFD, err := unix.Openat(spoolFD, ".spool.lock", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if errors.Is(err, unix.EEXIST) {
		lockFD, err = unix.Openat(spoolFD, ".spool.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return "", err
	}
	defer unix.Close(lockFD)
	var lockStat unix.Stat_t
	if err := unix.Fstat(lockFD, &lockStat); err != nil {
		return "", err
	}
	if lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Uid != uint32(os.Geteuid()) || lockStat.Mode&07777 != 0600 || lockStat.Nlink != 1 {
		return "", fmt.Errorf("%s spool lock must be an owned private inode", id.label)
	}
	var named unix.Stat_t
	if err := unix.Fstatat(rootFD, name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", err
	}
	if named.Dev != st.Dev || named.Ino != st.Ino || named.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("%s spool directory changed during preparation", id.label)
	}
	if err := unix.Fsync(lockFD); err != nil {
		return "", err
	}
	if err := unix.Fsync(spoolFD); err != nil {
		return "", err
	}
	if err := unix.Fsync(rootFD); err != nil {
		return "", err
	}
	return spool, nil
}
