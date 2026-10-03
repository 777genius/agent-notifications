//go:build windows

package installruntime

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Red condition: a cache temp inherits foreign read access at creation, allowing
// a reader to retain access to later bytes after its DACL is made private.
func TestWindowsPrivateCacheCreationExcludesInheritedReaders(t *testing.T) {
	root, _ := windowsTestDirectoryWithForeignACE(t, "OICI", 0x001200A9)
	handles, err := privateCacheRootHandles(root)
	defer closeWindowsParents(handles)
	if err != nil {
		t.Fatalf("read-only inheritable root was rejected: %v", err)
	}
	handle, err := windowsCreatePrivateCacheAt(handles[len(handles)-1], ".cache-creation-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windowsDeleteHandle(handle); err != nil {
			t.Errorf("delete TEST cache temp: %v", err)
		}
		if err := windows.CloseHandle(handle); err != nil {
			t.Errorf("close TEST cache temp: %v", err)
		}
	})
	// Inspect the still-empty creation handle before any bytes or ACL mutation.
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("created temp lacks exact current-user owner: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("created temp inherited its root DACL: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		t.Fatalf("created temp lacks three private grants: %v", err)
	}
	want := map[string]bool{user.User.Sid.String(): false, "S-1-5-18": false, "S-1-5-32-544": false}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		// FILE_ALL_ACCESS is 0x001F01FF; no inherited/foreign ACE may remain.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || ace.Mask != 0x001F01FF {
			t.Fatalf("created temp has an unexpected access grant: %+v", ace)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		seen, expected := want[sid]
		if !expected || seen {
			t.Fatalf("created temp grants a foreign/duplicate principal: %s", sid)
		}
		want[sid] = true
	}
}
