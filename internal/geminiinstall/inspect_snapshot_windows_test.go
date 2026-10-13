//go:build windows

package geminiinstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// DirEntry.Info uses directory enumeration metadata on Windows; it may retain
// old directory write times after a child was created/removed. os.Lstat also
// uses an attribute fast path and os.SameFile loads its file ID lazily. Snapshot
// both metadata and ID from a no-follow handle BEFORE Inspect, so same-path
// replacements and genuine timestamp changes remain observable. No write or
// timestamp-reset access is requested, including when the leaf is a reparse point.
func inspectPathInfo(path string) (fs.FileInfo, string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, "", err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, "", err
	}
	f := os.NewFile(uintptr(h), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &attributes); err != nil {
		return nil, "", err
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, "", err
	}
	sddl := sd.String()
	if sddl == "" {
		return nil, "", errors.New("security descriptor unavailable")
	}
	return info, fmt.Sprintf("%08x:%s", attributes.FileAttributes, sddl), nil
}

// Immediate child writes/removals formerly left stale enumeration timestamps
// in the baseline. Readonly handle observations must be stable without sleeps.
func TestWindowsInspectSnapshotAfterChildMutation(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "child")
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(child, []byte("inert child"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(child); err != nil {
			t.Fatal(err)
		}
		before := inspectTree(t, base)
		if err := inspectTreeDifference(before, inspectTree(t, base)); err != nil {
			t.Fatalf("readonly observation after child mutation: %v", err)
		}
	}
}

// Unix-style modes cannot observe Windows DACL drift. Ensure a real ACL change
// is rejected even though contents, file ID, readonly mode and time stay equal.
func TestWindowsInspectSnapshotDetectsDACLMutation(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "node")
	if err := os.WriteFile(path, []byte("inert ACL fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	setACL := func(extra string) {
		t.Helper()
		sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")" + extra)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			t.Fatal(err)
		}
	}
	setACL("")
	before := inspectTree(t, base)
	setACL("(A;;FR;;;WD)")
	after := inspectTree(t, base)
	if !os.SameFile(before[path].info, after[path].info) || before[path].info.Mode() != after[path].info.Mode() ||
		!before[path].info.ModTime().Equal(after[path].info.ModTime()) || before[path].contents != after[path].contents {
		t.Fatal("ACL fixture changed more than the security descriptor")
	}
	if err := inspectTreeDifference(map[string]inspectPathSnapshot{path: before[path]},
		map[string]inspectPathSnapshot{path: after[path]}); err == nil {
		t.Fatal("readonly observer accepted DACL mutation")
	}
}
