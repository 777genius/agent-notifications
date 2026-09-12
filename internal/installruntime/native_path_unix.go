//go:build linux || darwin

package installruntime

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func renameNative(from, to string, expected []PathAnchor, sourceID ...string) error {
	if filepath.Dir(from) != filepath.Dir(to) {
		return fmt.Errorf("native rename requires siblings")
	}
	parent, anchors, err := anchoredParent(to, false)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := checkAnchors(expected, anchors); err != nil {
		return err
	}
	if len(sourceID) != 0 && sourceID[0] != "" {
		var st unix.Stat_t
		if err := unix.Fstatat(int(parent.Fd()), filepath.Base(from), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR || fmt.Sprintf("%d:%d", st.Dev, st.Ino) != sourceID[0] {
			return fmt.Errorf("native rename source inode changed")
		}
	}
	if err := renameNativeAt(int(parent.Fd()), filepath.Base(from), filepath.Base(to)); err != nil {
		return err
	}
	return parent.Sync()
}

func openNativeStageLock(root *os.Root) (*os.File, error) {
	f, err := root.OpenFile(".staging.lock", os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		f.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0600 || st.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("native staging lock requires an owned private regular inode")
	}
	return f, nil
}
