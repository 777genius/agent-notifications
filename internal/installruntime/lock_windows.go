package installruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/777genius/agent-notifications/internal/windowsacl"
	"golang.org/x/sys/windows"
)

// Create only managed suffixes with the existing private policy at creation.
// Existing system ancestors require structural custody, not private ACLs.
func ensureLockDirectory(ctx context.Context, path string, _ os.FileMode) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	handles, anchors, err := windowsParentsWithSharing(path, false, false)
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			resultErr = errors.Join(resultErr, windows.CloseHandle(handles[i]))
		}
	}()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(handles) == 0 || len(handles) != len(anchors) {
		return fmt.Errorf("managed directory ancestor custody unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	suffix, err := filepath.Rel(anchors[len(anchors)-1].Path, path)
	if err != nil {
		return err
	}
	security, err := privateWindowsSecurityDescriptor(true)
	if err != nil {
		return err
	}
	for _, leaf := range strings.Split(suffix, `\`) {
		if err := ctx.Err(); err != nil {
			return err
		}
		// FILE_OPEN_IF never replaces security on an existing/raced directory.
		handle, err := windowsOpenAtWithSharing(handles[len(handles)-1], leaf,
			windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.FILE_TRAVERSE,
			windows.FILE_OPEN_IF, windows.FILE_DIRECTORY_FILE, security,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
		if err != nil {
			return err
		}
		handles = append(handles, handle)
		if err := ctx.Err(); err != nil {
			return err
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return fmt.Errorf("managed directory requires a non-reparse inode")
		}
		if err := privateWindowsHandle(handle); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func tryLock(f *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func prepareLockedFile(f *os.File, writable bool) error {
	return nil
}

func openLock(path string, create bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	disposition := uint32(windows.OPEN_EXISTING)
	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
	var security *windows.SecurityAttributes
	if create {
		disposition = windows.OPEN_ALWAYS
		access |= windows.WRITE_DAC | windows.WRITE_OWNER
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return nil, err
		}
		// Preserve the kernel's default owner during creation. The handle-bound
		// snapshot below decides whether owner/DACL normalization is necessary.
		sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
		if err != nil {
			return nil, err
		}
		security = &windows.SecurityAttributes{
			Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
			SecurityDescriptor: sd,
		}
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, security, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		f.Close()
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		f.Close()
		return nil, fmt.Errorf("installation lock requires a regular non-reparse inode")
	}
	canonical, err := inspectPrivateWindowsHandle(h)
	if err != nil {
		f.Close()
		return nil, err
	}
	// Repeated observation claims reuse the permanent inode. Do not rewrite
	// already canonical security metadata, but retain legacy ACL adoption.
	if create && !canonical {
		if err := restrictPrivateWindowsHandle(h); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func privateDirectory(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("control directory must be a non-reparse directory")
	}
	return privateWindowsHandle(h)
}

func privateWindowsHandle(h windows.Handle) error {
	_, err := inspectPrivateWindowsHandle(h)
	return err
}

// Validation and canonicality use the same handle-bound security snapshot.
func inspectPrivateWindowsHandle(h windows.Handle) (bool, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, observedWindowsAPI("GetSecurityInfo", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	if owner == nil || (!owner.Equals(user.User.Sid) && owner.String() != "S-1-5-18" && owner.String() != "S-1-5-32-544") {
		return false, fmt.Errorf("managed inode owner mismatch")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if acl == nil {
		return false, fmt.Errorf("managed inode requires a private DACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return false, observedWindowsAPI("GetAce", err)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false, fmt.Errorf("unsupported managed inode ACL")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) && !windowsacl.AllowsForeignReadOnly(ace.Mask) {
			kind, flags, mask := ace.Header.AceType, ace.Header.AceFlags, uint32(ace.Mask)
			return false, &admissionObservation{Classification: "foreign_mutation_ace", ACEType: &kind, ACEFlags: &flags, AccessMask: &mask,
				Err: fmt.Errorf("managed inode DACL grants foreign access (ace_type=%d ace_flags=%d access_mask=0x%08x)", kind, flags, mask)}
		}
	}
	return canonicalPrivateWindowsDescriptor(sd, user.User.Sid)
}

// Canonical locks have exactly the policy installed by
// privateWindowsSecurityDescriptor(false). Accepted read-only legacy grants,
// deny/inherited ACEs or a SYSTEM/Admin owner still require normalization.
func canonicalPrivateWindowsDescriptor(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) (bool, error) {
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	if owner == nil || !owner.Equals(user) {
		return false, nil
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, nil
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if acl == nil || acl.AceCount != 3 {
		return false, nil
	}
	// A multiset also handles a process whose user SID is SYSTEM itself.
	remaining := map[string]int{user.String(): 1}
	remaining["S-1-5-18"]++
	remaining["S-1-5-32-544"]++
	const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Mask != fileAllAccess {
			return false, nil
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if remaining[sid] == 0 {
			return false, nil
		}
		remaining[sid]--
	}
	return true, nil
}

func privateWindowsSecurityDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	return windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;" + inherit + ";FA;;;" + sid.String() + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
}

func restrictPrivateWindowsHandle(h windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	sd, err := privateWindowsSecurityDescriptor(info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0)
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

// RestrictPrivatePath sets a user/SYSTEM/Administrators FILE_ALL_ACCESS DACL.
func RestrictPrivatePath(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return restrictPrivateWindowsHandle(h)
}

// Only an error returned by this immediate API call can supply its numeric code.
func observedWindowsAPI(api string, err error) error {
	var code syscall.Errno
	if !errors.As(err, &code) {
		return err
	}
	number := uint32(code)
	return &admissionObservation{Classification: "windows_api_error", API: api, ErrorCode: &number, Err: err}
}
