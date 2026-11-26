//go:build linux || darwin

package setup

import (
	"strconv"
	"strings"
)

// A recorded Btrfs subvolume identity lets this setup-state directory survive
// st_dev renumbering only when its inode and subvolume UUID/ID still match.
// Without that evidence, matches keeps the platform's pre-existing policy
// (strict exact identity on Linux; Darwin's separate parent-device-bound rule).
func renumberedOnVolume(want, current, volume, currentVolume string) bool {
	if volume == "" || volume != currentVolume {
		return false
	}
	wantIno, ok := objectInode(want)
	currentIno, currentOK := objectInode(current)
	return ok && currentOK && wantIno == currentIno
}

func objectInode(id string) (string, bool) {
	dev, ino, ok := strings.Cut(id, ":")
	if !ok {
		return "", false
	}
	for _, part := range []string{dev, ino} {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return "", false
		}
	}
	return ino, true
}
