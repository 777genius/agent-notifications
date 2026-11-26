//go:build linux

package setup

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux UAPI (asm-generic) for BTRFS_IOC_FS_INFO and BTRFS_IOC_INO_LOOKUP.
// The latter's unrestricted treeid=0, objectid=256 form returns the subvolume
// containing the already-validated, descriptor-opened directory.
const (
	btrfsIOCFSInfo    = 0x8400941f
	btrfsIOCInoLookup = 0xd0009412
	btrfsFirstInode   = 256
)

type btrfsFSInfo struct {
	MaxID          uint64
	NumDevices     uint64
	FSID           [16]byte
	NodeSize       uint32
	SectorSize     uint32
	CloneAlignment uint32
	ChecksumType   uint16
	ChecksumSize   uint16
	Flags          uint64
	Generation     uint64
	MetadataUUID   [16]byte
	Reserved       [944]byte
}

type btrfsInoLookup struct {
	TreeID   uint64
	ObjectID uint64
	Name     [4080]byte
}

// Catch any accidental UAPI struct-layout changes before execution.
var (
	_ [1024 - unsafe.Sizeof(btrfsFSInfo{})]byte
	_ [unsafe.Sizeof(btrfsFSInfo{}) - 1024]byte
	_ [4096 - unsafe.Sizeof(btrfsInoLookup{})]byte
	_ [unsafe.Sizeof(btrfsInoLookup{}) - 4096]byte
)

// Btrfs st_dev is a mount-time anonymous device ID. statfs f_fsid is also
// unsuitable as a durable marker: some kernels mix a changing dev_t into it.
// Use on-disk filesystem UUID + immutable subvolume ID instead. Snapshots
// retain inode numbers but receive distinct subvolume IDs. Failure is strict:
// never fall back to inode-only or f_fsid matching.
func volumeID(f *os.File) (string, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &stat); err != nil {
		return "", err
	}
	if stat.Type != unix.BTRFS_SUPER_MAGIC {
		return "", nil
	}
	var fs btrfsFSInfo
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), btrfsIOCFSInfo, uintptr(unsafe.Pointer(&fs)))
	runtime.KeepAlive(f)
	if errno != 0 {
		return "", fmt.Errorf("read btrfs filesystem identity: %w", errno)
	}
	var lookup btrfsInoLookup
	lookup.ObjectID = btrfsFirstInode
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, f.Fd(), btrfsIOCInoLookup, uintptr(unsafe.Pointer(&lookup)))
	runtime.KeepAlive(f)
	if errno != 0 {
		return "", fmt.Errorf("read btrfs subvolume identity: %w", errno)
	}
	if lookup.TreeID == 0 || fs.FSID == ([16]byte{}) {
		return "", fmt.Errorf("missing btrfs filesystem/subvolume identity")
	}
	return btrfsVolumeID(fs.FSID, lookup.TreeID), nil
}

func btrfsVolumeID(fsid [16]byte, subvolume uint64) string {
	return fmt.Sprintf("btrfs:%x:%d", fsid, subvolume)
}
