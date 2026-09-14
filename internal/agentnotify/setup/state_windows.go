//go:build windows

package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// syncOpenedDir is a no-op. FlushFileBuffers does not support directory
// handles; file contents are already flushed before rename. Same contract as
// installruntime.syncDir.
func syncOpenedDir(*os.File) error { return nil }

// setupDirAccess walks ancestors with list/traverse only. NtCreateFile does
// not map GENERIC_* bits, so the private leaf requests FILE_GENERIC_WRITE
// (FILE_ADD_FILE/FILE_ADD_SUBDIRECTORY). GENERIC_WRITE (0x40000000) is outside
// FILE_ALL_ACCESS and is ACCESS_DENIED against a restricted DACL.
func setupDirAccess(leaf bool) uint32 {
	access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES | windows.FILE_TRAVERSE | windows.READ_CONTROL)
	if leaf {
		access |= windows.FILE_GENERIC_WRITE
	}
	return access
}

func openRoot(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("unsafe setup directory")
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || path == volume+`\` {
		return nil, fmt.Errorf("unsafe setup directory")
	}
	parent := volume + `\`
	name, err := windows.UTF16PtrFromString(parent)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, parent), `\`)
	for i, part := range parts {
		if part == "" {
			windows.CloseHandle(h)
			return nil, fmt.Errorf("unsafe setup directory")
		}
		next, err := setupOpenAt(h, part, setupDirAccess(i == len(parts)-1), windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
		windows.CloseHandle(h)
		if err != nil {
			return nil, err
		}
		h = next
		info, err := setupFileInfo(h)
		if err != nil {
			windows.CloseHandle(h)
			return nil, err
		}
		if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			windows.CloseHandle(h)
			return nil, fmt.Errorf("unsafe setup directory")
		}
		if i == len(parts)-1 {
			if err = setupRequirePrivate(h); err != nil {
				windows.CloseHandle(h)
				return nil, fmt.Errorf("unsafe setup directory")
			}
		}
	}
	return os.NewFile(uintptr(h), path), nil
}

func openChild(parent *os.File, name string) (*os.File, error) {
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, setupDirAccess(true), windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	info, err := setupFileInfo(h)
	if err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		windows.CloseHandle(h)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unsafe private directory %s", name)
	}
	if err = setupRequirePrivate(h); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("unsafe private directory %s", name)
	}
	return os.NewFile(uintptr(h), name), nil
}

func mkdir(parent *os.File, name string) (*os.File, error) {
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, setupDirAccess(true)|windows.WRITE_DAC|windows.WRITE_OWNER, windows.FILE_CREATE, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	if err = setupRestrictPrivate(h); err != nil {
		remove := byte(1)
		_ = windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, &remove, 1)
		windows.CloseHandle(h)
		return nil, err
	}
	windows.CloseHandle(h)
	if err = syncOpenedDir(parent); err != nil {
		return nil, err
	}
	return openChild(parent, name)
}

func absent(parent *os.File, name string) error {
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, 0)
	if err != nil {
		if setupNotExist(err) {
			return nil
		}
		return err
	}
	windows.CloseHandle(h)
	return fmt.Errorf("path already exists")
}

func directoryID(f *os.File) (string, error) {
	info, err := setupFileInfo(windows.Handle(f.Fd()))
	if err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("expected non-reparse directory handle")
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func matches(f *os.File, want string) error {
	id, err := directoryID(f)
	if err != nil {
		return err
	}
	if id != want {
		return fmt.Errorf("owned directory identity changed")
	}
	return nil
}

func createLock(dir *os.File) error {
	h, err := setupOpenAt(windows.Handle(dir.Fd()), ".spool.lock", windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER|windows.DELETE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), ".spool.lock")
	if err = setupRestrictPrivate(h); err != nil {
		_ = f.Close()
		return err
	}
	err = f.Sync()
	_ = f.Close()
	if err != nil {
		return err
	}
	return syncOpenedDir(dir)
}

func checkLock(dir *os.File) error {
	f, err := privateFile(dir, ".spool.lock", 8*1024*1024)
	if err != nil {
		return err
	}
	return f.Close()
}

func privateFile(parent *os.File, name string, limit int64) (*os.File, error) {
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, windows.FILE_GENERIC_READ, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	info, err := setupFileInfo(h)
	if err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(h)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unsafe private file %s", name)
	}
	if int64(info.FileSizeHigh)<<32|int64(info.FileSizeLow) > limit {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("unsafe private file %s", name)
	}
	if err = setupRequirePrivate(h); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("unsafe private file %s", name)
	}
	return os.NewFile(uintptr(h), name), nil
}

func setupOpenAt(parent windows.Handle, name string, access, disposition, options uint32) (windows.Handle, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:`) {
		return 0, fmt.Errorf("invalid relative Windows component")
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	share := uint32(windows.FILE_SHARE_READ)
	if options&windows.FILE_DIRECTORY_FILE != 0 {
		share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	}
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, share, disposition, options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			return 0, status.Errno()
		}
		return 0, err
	}
	return handle, nil
}

func setupFileInfo(h windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(h, &info)
	return info, err
}

func setupNotExist(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func setupCurrentSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func setupRestrictPrivate(h windows.Handle) error {
	sid, err := setupCurrentSID()
	if err != nil {
		return err
	}
	info, err := setupFileInfo(h)
	if err != nil {
		return err
	}
	inherit := ""
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		inherit = "OICI"
	}
	// FA is FILE_ALL_ACCESS. GENERIC_ALL ACEs make SetSecurityInfo return
	// ERROR_INVALID_PARAMETER on GitHub Windows runners.
	sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;" + inherit + ";FA;;;" + sid.String() + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("managed inode requires a private DACL")
	}
	flags := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, flags, owner, nil, dacl, nil); err == nil {
		return nil
	}
	if e := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil); e != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func setupRequirePrivate(h windows.Handle) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if owner == nil || (!owner.Equals(user.User.Sid) && owner.String() != "S-1-5-18" && owner.String() != "S-1-5-32-544") {
		return fmt.Errorf("managed inode owner mismatch")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return fmt.Errorf("managed inode requires a private DACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("unsupported managed inode ACL")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Mask != 0 && !sid.Equals(user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return fmt.Errorf("managed inode DACL grants foreign access")
		}
	}
	return nil
}
