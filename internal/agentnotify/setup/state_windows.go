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
	h, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, parent), `\`)
	for i, part := range parts {
		if part == "" {
			windows.CloseHandle(h)
			return nil, fmt.Errorf("unsafe setup directory")
		}
		next, err := setupOpenAt(h, part, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
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
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
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
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE|windows.WRITE_DAC|windows.WRITE_OWNER, windows.FILE_CREATE, windows.FILE_DIRECTORY_FILE)
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
	if err = parent.Sync(); err != nil {
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
	h, err := setupOpenAt(windows.Handle(dir.Fd()), ".spool.lock", windows.GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER|windows.DELETE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE)
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
	return dir.Sync()
}

func checkLock(dir *os.File) error {
	f, err := privateFile(dir, ".spool.lock", 8*1024*1024)
	if err != nil {
		return err
	}
	return f.Close()
}

func privateFile(parent *os.File, name string, limit int64) (*os.File, error) {
	h, err := setupOpenAt(windows.Handle(parent.Fd()), name, windows.GENERIC_READ, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE)
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
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	entries := []windows.EXPLICIT_ACCESS{
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)}},
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(system)}},
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_GROUP, TrusteeValue: windows.TrusteeValueFromSID(admins)}},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
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
	if owner == nil || !owner.Equals(user.User.Sid) {
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
