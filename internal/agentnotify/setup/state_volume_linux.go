//go:build linux

package setup

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux UAPI for BTRFS_IOC_GET_SUBVOL_INFO. Unlike BTRFS_IOC_FS_INFO,
// GET_SUBVOL_INFO is an unprivileged read and returns the persistent UUID
// and tree ID of the subvolume containing the already-opened directory.
const btrfsIOCGetSubvolInfo = 0x81f8943c

type btrfsTimespec struct {
	Sec  uint64
	Nsec uint32
	_    uint32
}

type btrfsSubvolInfo struct {
	TreeID      uint64
	Name        [256]byte // BTRFS_VOL_NAME_MAX + 1
	ParentID    uint64
	DirID       uint64
	Generation  uint64
	Flags       uint64
	UUID        [16]byte
	ParentUUID  [16]byte
	ReceivedUUID [16]byte
	CTransID    uint64
	OTransID    uint64
	STransID    uint64
	RTransID    uint64
	CTime       btrfsTimespec
	OTime       btrfsTimespec
	STime       btrfsTimespec
	RTime       btrfsTimespec
	Reserved    [8]uint64
}

// Catch UAPI layout drift before issuing the ioctl.
var (
	_ [504 - unsafe.Sizeof(btrfsSubvolInfo{})]byte
	_ [unsafe.Sizeof(btrfsSubvolInfo{}) - 504]byte
)

// Btrfs st_dev and statfs f_fsid are not durable subvolume identifiers:
// st_dev may be renumbered across boots, and some kernels include dev_t in
// f_fsid. The unprivileged GET_SUBVOL_INFO ioctl returns the persistent
// subvolume UUID and tree ID. Snapshots receive a new subvolume UUID/ID while
// retaining inode numbers. Top-level roots with no UUID fail closed.
func volumeID(f *os.File) (string, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &stat); err != nil {
		return "", err
	}
	if stat.Type != unix.BTRFS_SUPER_MAGIC {
		return "", nil
	}

	var info btrfsSubvolInfo
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), btrfsIOCGetSubvolInfo, uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(f)
	if errno == unix.ENOTTY || errno == unix.EOPNOTSUPP {
		// Older kernels do not expose GET_SUBVOL_INFO. Keep the installer
		// usable, but persist no volume evidence: matching remains exact-only.
		return "", nil
	}
	if errno != 0 {
		return "", fmt.Errorf("read btrfs subvolume identity: %w", errno)
	}
	if info.TreeID == 0 {
		return "", fmt.Errorf("missing btrfs subvolume ID")
	}
	if info.UUID == ([16]byte{}) {
		// The top-level root has no independent subvolume UUID. It remains
		// supported with the original exact dev:ino identity only.
		return "", nil
	}
	return btrfsVolumeID(info.UUID, info.TreeID), nil
}

func btrfsVolumeID(uuid [16]byte, subvolume uint64) string {
	return fmt.Sprintf("btrfs-subvol:%x:%d", uuid, subvolume)
}
