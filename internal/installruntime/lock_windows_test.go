//go:build windows

package installruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	assertLockDACLProtected(t, filepath.Join(root, "install.lock"))

	legacyPath := filepath.Join(root, "legacy.lock")
	if err := os.WriteFile(legacyPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	legacyCtx, legacyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer legacyCancel()
	release, err = Lock(legacyCtx, legacyPath)
	if err != nil {
		t.Fatalf("existing installer lock with inherited read ACE rejected: %v", err)
	}
	release()
	assertLockDACLProtected(t, legacyPath)
}

func assertLockDACLProtected(t *testing.T, path string) {
	t.Helper()
	f, err := openLock(path, false)
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
		t.Fatal("installer lock retained the control root DACL")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("installer lock has no DACL: %v", err)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).IsWellKnown(windows.WinWorldSid) {
			t.Fatal("installer lock grants Everyone access")
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

// Red: legacy Mkdir(0700) inherits foreign write access; public Lock must
// create a private managed suffix even though its ordinary parent is not private.
func TestWindowsLockCreatesPrivateManagedSuffix(t *testing.T) {
	parent, _ := windowsTestDirectoryWithForeignACE(t, "OICI", windows.FILE_WRITE_DATA)
	legacy := filepath.Join(parent, "legacy")
	if err := os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if CheckPrivateControlRoot(legacy) == nil {
		t.Fatal("legacy API fixture did not inherit foreign mutation rights")
	}
	before := lockDirectorySecurity(t, parent)
	root := filepath.Join(parent, "new", "control")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := Lock(ctx, filepath.Join(root, "install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, path := range []string{filepath.Dir(root), root} {
		if err := CheckPrivateControlRoot(path); err != nil {
			t.Fatal(err)
		}
		assertPrivateCreatedDirectory(t, lockDirectorySecurity(t, path))
	}
	if !reflect.DeepEqual(before, lockDirectorySecurity(t, parent)) {
		t.Fatal("ordinary parent security was rewritten")
	}
}

type lockDirectoryDescriptor struct {
	owner   string
	control windows.SECURITY_DESCRIPTOR_CONTROL
	raw     []byte
}
func lockDirectorySecurity(t *testing.T, path string) lockDirectoryDescriptor {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	}()
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		t.Fatal("missing TEST directory owner")
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	size := sd.Length()
	if !sd.IsValid() || control&windows.SE_SELF_RELATIVE == 0 || size < 20 || size > 65536 {
		t.Fatal("invalid TEST descriptor")
	}
	out := lockDirectoryDescriptor{owner.String(), control,
		append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(sd)), int(size))...)}
	runtime.KeepAlive(sd)
	return out
}
func assertPrivateCreatedDirectory(t *testing.T, got lockDirectoryDescriptor) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if got.owner != user.User.Sid.String() || got.control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("new managed directory has no protected current-user ownership")
	}
	sd := (*windows.SECURITY_DESCRIPTOR)(unsafe.Pointer(&got.raw[0]))
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 3 {
		t.Fatal("new directory retained foreign grants")
	}
	remaining := map[string]int{user.User.Sid.String(): 1}
	remaining["S-1-5-18"]++
	remaining["S-1-5-32-544"]++
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		// Directory grants inherit to files and directories; no inherited ACE.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 3 || ace.Mask != 0x001f01ff {
			t.Fatal("unexpected creation-time directory grant")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if remaining[sid] == 0 {
			t.Fatal("foreign/duplicate directory principal")
		}
		remaining[sid]--
	}
	runtime.KeepAlive(got.raw)
}

// Red: adopting an existing mutable directory repairs foreign metadata, or
// canceled/read-only claims create missing directory/lock state.
func TestWindowsLockRefusesForeignDirectoryWithoutRepair(t *testing.T) {
	root, _ := windowsTestDirectoryWithForeignACE(t, "OICI", windows.FILE_WRITE_DATA)
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("foreign unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	before := lockDirectorySecurity(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if release, err := Lock(ctx, filepath.Join(root, "install.lock")); err == nil {
		release()
		t.Fatal("foreign mutable directory admitted")
	}
	if !reflect.DeepEqual(before, lockDirectorySecurity(t, root)) {
		t.Fatal("foreign DACL/owner repaired")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || !bytes.Equal(data, []byte("foreign unchanged")) {
		t.Fatal("foreign sentinel changed")
	}
	if _, err := os.Stat(filepath.Join(root, "install.lock")); !os.IsNotExist(err) {
		t.Fatal("refused claim created a lock")
	}
	missing := filepath.Join(root, "missing", "install.lock")
	if release, err := LockExisting(ctx, missing); err == nil {
		release()
		t.Fatal("missing existing lock admitted")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := Lock(canceled, missing); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("canceled/readonly claim created directories")
	}
}

// Red: FILE_OPEN_IF accepts a raced foreign directory or overwrites its ACL.
// Both kernel race outcomes are valid, but a foreign winner must be refused.
func TestWindowsLockDirectoryCreationRace(t *testing.T) {
	foreignWins, privateWins := 0, 0
	for i := 0; i < 8; i++ {
		parent, _ := windowsTestDirectoryWithForeignACE(t, "OICI", windows.FILE_WRITE_DATA)
		legacy := filepath.Join(parent, "baseline")
		if err := os.Mkdir(legacy, 0700); err != nil {
			t.Fatal(err)
		}
		baseline := lockDirectorySecurity(t, legacy)
		root := filepath.Join(parent, "raced")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start, mkdirDone, lockDone := make(chan struct{}), make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			err := os.Mkdir(root, 0700)
			if err == nil {
				err = os.WriteFile(filepath.Join(root, "sentinel"), []byte("raced foreign unchanged"), 0600)
			}
			mkdirDone <- err
		}()
		go func() {
			<-start
			release, err := Lock(ctx, filepath.Join(root, "install.lock"))
			if err == nil {
				release()
			}
			lockDone <- err
		}()
		close(start)
		created, locked := <-mkdirDone, <-lockDone // Join both actors before any failure/cleanup.
		cancel()
		if created == nil {
			foreignWins++
			if locked == nil {
				t.Fatal("raced foreign directory admitted")
			}
			if !reflect.DeepEqual(baseline, lockDirectorySecurity(t, root)) {
				t.Fatal("raced foreign security changed")
			}
			data, err := os.ReadFile(filepath.Join(root, "sentinel"))
			if err != nil || !bytes.Equal(data, []byte("raced foreign unchanged")) {
				t.Fatal("raced foreign sentinel changed")
			}
		} else {
			privateWins++
			if !os.IsExist(created) || locked != nil {
				t.Fatalf("unproved directory race outcome: %v, %v", created, locked)
			}
			assertPrivateCreatedDirectory(t, lockDirectorySecurity(t, root))
		}
	}
	t.Logf("actual directory race outcomes: foreign=%d private=%d", foreignWins, privateWins)
}

// Red: a final reparse alias is followed or repaired by lock-directory creation.
func TestWindowsLockRejectsReparseDirectory(t *testing.T) {
	target, _ := windowsTestDirectoryWithForeignACE(t, "OICI", windows.FILE_WRITE_DATA)
	before := lockDirectorySecurity(t, target)
	alias := filepath.Join(filepath.Dir(target), "alias")
	if err := os.Symlink(target, alias); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			t.Skip("TEST symlink creation unsupported")
		}
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if release, err := Lock(ctx, filepath.Join(alias, "install.lock")); err == nil {
		release()
		t.Fatal("reparse directory admitted")
	}
	if !reflect.DeepEqual(before, lockDirectorySecurity(t, target)) {
		t.Fatal("alias target security changed")
	}
	if _, err := os.Stat(filepath.Join(target, "install.lock")); !os.IsNotExist(err) {
		t.Fatal("alias target lock created")
	}
}
