package sharedstate

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"testing"
)

// A private directory's default ACL can grant access to subsequently created
// files even though its own mode is 0700; refuse before creating state children.
func TestLinuxRefusesDefaultACL(t *testing.T) {
	root := testRoot(t)
	acl := make([]byte, 4+3*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range []struct{ tag, perm uint16 }{{1, 7}, {4, 0}, {32, 0}} {
		offset := 4 + i*8
		binary.LittleEndian.PutUint16(acl[offset:], entry.tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], entry.perm)
		binary.LittleEndian.PutUint32(acl[offset+4:], 0xffffffff)
	}
	if err := unix.Setxattr(root, "system.posix_acl_default", acl, 0); err == unix.ENOTSUP {
		t.Skip("filesystem has no ACL support")
	} else if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, keyA, runtimeA); err == nil {
		t.Fatal("default ACL accepted")
	}
}
