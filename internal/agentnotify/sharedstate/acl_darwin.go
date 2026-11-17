package sharedstate

import (
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/unix"
	"unsafe"
)

// Darwin ACLs are not reflected in mode bits. Request the descriptor's extended
// security attribute; reject any ACL rather than guessing at inherited grants.
func checkACL(fd int) error {
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buf [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if errno != 0 {
		return errno
	}
	if binary.LittleEndian.Uint32(buf[:4]) < 12 {
		return fmt.Errorf("invalid security attribute")
	}
	length := binary.LittleEndian.Uint32(buf[8:12])
	if length != 0 {
		return fmt.Errorf("shared endpoint forbids extended ACLs")
	}
	return nil
}
