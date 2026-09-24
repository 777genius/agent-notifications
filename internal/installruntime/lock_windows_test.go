//go:build windows

package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrivateWindowsHandleForeignReadOnlyACE(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   string
		mask    uint32
		allowed bool
	}{
		{name: "inherited-style read execute", flags: "OICI", mask: 0x001200A9, allowed: true},
		{name: "generic read execute", flags: "OICI", mask: windows.GENERIC_READ | windows.GENERIC_EXECUTE, allowed: true},
		{name: "write data", flags: "OICI", mask: windows.FILE_WRITE_DATA},
		{name: "append data", flags: "OICI", mask: windows.FILE_APPEND_DATA},
		{name: "write extended attributes", flags: "OICI", mask: windows.FILE_WRITE_EA},
		{name: "delete child", flags: "OICI", mask: 0x40}, // FILE_DELETE_CHILD
		{name: "write attributes", flags: "OICI", mask: windows.FILE_WRITE_ATTRIBUTES},
		{name: "delete", flags: "OICI", mask: windows.DELETE},
		{name: "write DACL", flags: "OICI", mask: windows.WRITE_DAC},
		{name: "write owner", flags: "OICI", mask: windows.WRITE_OWNER},
		{name: "generic write", flags: "OICI", mask: windows.GENERIC_WRITE},
		{name: "generic all", flags: "OICI", mask: windows.GENERIC_ALL},
		{name: "inherit-only write", flags: "OICIIO", mask: windows.FILE_WRITE_DATA},
		{name: "unknown grant", flags: "OICI", mask: 0x02000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, h := windowsTestDirectoryWithForeignACE(t, tc.flags, tc.mask)
			err := privateWindowsHandle(h)
			if tc.allowed && err != nil {
				t.Fatalf("read-only foreign ACE rejected: %v", err)
			}
			if !tc.allowed && err == nil {
				t.Fatal("foreign mutation right accepted")
			}
		})
	}
}

func TestLockWithInheritedForeignReadOnlyACE(t *testing.T) {
	root, _ := windowsTestDirectoryWithForeignACE(t, "OICI", 0x001200A9)
	if err := CheckPrivateControlRoot(root); err != nil {
		t.Fatalf("control root with read-only inherited ACE rejected: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := Lock(ctx, filepath.Join(root, "install.lock"))
	if err != nil {
		t.Fatalf("installer lock inherited a read-only ACE: %v", err)
	}
	release()
	f, err := openLock(filepath.Join(root, "install.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("new installer lock inherited the control root DACL")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("new installer lock has no DACL: %v", err)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).IsWellKnown(windows.WinWorldSid) {
			t.Fatal("new installer lock grants Everyone access")
		}
	}
}

func windowsTestDirectoryWithForeignACE(t *testing.T, flags string, mask uint32) (string, windows.Handle) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;%s;0x%08X;;;WD)",
		user.User.Sid.String(), flags, mask)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	return path, h
}
