//go:build darwin

package opencodeinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
	"golang.org/x/sys/unix"
)

// NativeInstallation is bound to one event's policy snapshot. Its lease checks
// OpenCode registration and desktop consent independently of portable enablement
// while holding both managed locks through native handoff.
type NativeInstallation struct {
	ControlRoot  string
	Expected     installruntime.PolicySnapshot
	Executable   string
	GOOS, GOARCH string
}

var _ notifier.NativeInstallation = NativeInstallation{}

type nativeLease struct {
	bundle  string
	release func()
	once    sync.Once
}

func (l *nativeLease) BundlePath() string { return l.bundle }
func (l *nativeLease) ExecutablePath() string {
	return filepath.Join(l.bundle, "Contents", "MacOS", "terminal-notifier-modern")
}
func (l *nativeLease) Release() { l.once.Do(l.release) }

func (n NativeInstallation) Acquire(ctx context.Context) (notifier.NativeLease, error) {
	if n.ControlRoot == "" || n.Executable == "" {
		return nil, errors.New("OpenCode native installation requires control root and executable")
	}
	s, release, err := installruntime.AcquirePolicyLease(ctx, n.ControlRoot, n.Expected)
	if err != nil {
		return nil, err
	}
	goos, goarch := n.GOOS, n.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	desktop, _ := ChannelsFromSnapshot(s, n.Executable, goos, goarch)
	if !desktop || s.Installation.Ledger.Native == nil || s.Installation.Ledger.Native.DecoderFloor < 1 {
		release()
		return nil, errors.New("OpenCode desktop consent or verified native helper unavailable")
	}
	return &nativeLease{bundle: s.Installation.Ledger.Native.Path, release: release}, nil
}

// PrepareNativeSpool owns a separate OpenCode-only private namespace. Portable
// setup may later initialize controlRoot/state without colliding with it. Keep
// the spool on remove so a previously admitted bounded callback can complete.
func PrepareNativeSpool(ctx context.Context, controlRoot string) (string, error) {
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
	const name = "opencode-native-spool"
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
		return "", fmt.Errorf("OpenCode spool must be an owned private directory")
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
		return "", fmt.Errorf("OpenCode spool lock must be an owned private inode")
	}
	var named unix.Stat_t
	if err := unix.Fstatat(rootFD, name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", err
	}
	if named.Dev != st.Dev || named.Ino != st.Ino || named.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("OpenCode spool directory changed during preparation")
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

// SetupPermission is an explicit OpenCode setup operation. Registration and
// trusted native evidence are checked before the helper may probe or request OS
// authorization. No event handler calls this function.
func SetupPermission(ctx context.Context, controlRoot string, request bool) (string, error) {
	s, err := installruntime.ReadPolicySnapshot(ctx, controlRoot)
	if err != nil {
		return "unavailable", err
	}
	c, ok := s.Installation.Ledger.Consumers[consumerID]
	if !ok || len(c.Commands) == 0 || !RegisteredFromSnapshot(s, c.Commands[0], runtime.GOOS, runtime.GOARCH) ||
		s.Installation.Ledger.Native == nil || s.Installation.Ledger.Native.DecoderFloor < 1 {
		return "unavailable", errors.New("registered OpenCode installation with verified native helper required")
	}
	return notifier.SetupPermission(ctx, controlRoot, s.Installation, request)
}
