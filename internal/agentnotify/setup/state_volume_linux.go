//go:build linux

package setup

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// btrfs gives each subvolume an anonymous st_dev that can change across boots.
// Its statfs f_fsid mixes the filesystem UUID with the subvolume ID: stable, and
// different for every subvolume and snapshot. Other filesystems need no volume.
func volumeID(f *os.File) (string, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return "", err
	}
	if st.Type != unix.BTRFS_SUPER_MAGIC {
		return "", nil
	}
	return btrfsVolumeID(st.Fsid.Val), nil
}

func btrfsVolumeID(val [2]int32) string {
	return fmt.Sprintf("btrfs:%08x%08x", uint32(val[0]), uint32(val[1]))
}
