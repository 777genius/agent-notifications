//go:build windows

package config

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// User device paths are rejected by Resolve. Native calls receive the extended
// spelling of an already validated absolute path so long paths do not depend
// on a host registry setting or executable manifest. Public identities retain
// their ordinary native spelling.
func extendedWindowsPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}

func windowsStorePath(path string) (*uint16, error) {
	if !filepath.IsAbs(path) {
		return nil, &Error{Code: ConfigInvalid}
	}
	return windows.UTF16PtrFromString(extendedWindowsPath(path))
}

type storeParent struct {
	file      *os.File
	path      string
	ancestors []*os.File
	dirty     map[string]bool
}

func privateSecurity(directory bool) (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	// Object/container inheritance flags belong on directories, where they
	// propagate the private ACL to descendants. They are meaningless on a
	// regular file and ReplaceFileW normalizes them away.
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;" + inheritance + ";FA;;;" + user.User.Sid.String() + ")(A;" + inheritance + ";FA;;;SY)")
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func winOpen(path string, access, creation, flags uint32, private bool) (*os.File, error) {
	name, err := windowsStorePath(path)
	if err != nil {
		return nil, err
	}
	var sa *windows.SecurityAttributes
	if private {
		sa, err = privateSecurity(false)
		if err != nil {
			return nil, err
		}
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa, creation, flags|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		f.Close()
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		f.Close()
		return nil, &Error{Code: ConfigLinkedPath, Path: path}
	}
	return f, nil
}

// checkWindowsACL conservatively accepts only user/SYSTEM data access for
// credential-bearing files. Local administrators are trusted OS principals;
// newly created objects still grant access only to the user and SYSTEM.
func checkWindowsACL(f *os.File, private bool) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if private && !owner.Equals(user.User.Sid) && owner.String() != "S-1-5-18" && owner.String() != "S-1-5-32-544" {
		return &Error{Code: ConfigPermissionDenied}
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return &Error{Code: ConfigPermissionDenied}
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return &Error{Code: ConfigPermissionDenied}
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user.User.Sid) || sid.String() == "S-1-5-18" || sid.String() == "S-1-5-32-544" {
			continue
		}
		mask := uint32(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | 0x40 | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL)
		if private {
			mask |= windows.FILE_READ_DATA | windows.GENERIC_READ
		}
		// Administrators and TrustedInstaller own system ancestors. They already
		// control the OS and are outside the hostile unprivileged principal model.
		if !private && (sid.String() == "S-1-5-32-544" || strings.HasPrefix(sid.String(), "S-1-5-80-")) {
			continue
		}
		if uint32(ace.Mask)&mask != 0 {
			return &Error{Code: ConfigPermissionDenied}
		}
	}
	return nil
}
func openStoreParent(path string, create bool) (*storeParent, error) {
	if !filepath.IsAbs(path) {
		return nil, &Error{Code: ConfigInvalid}
	}
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	p := &storeParent{path: path, dirty: map[string]bool{}}
	fail := func(e error) (*storeParent, error) { p.close(); return nil, e }
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		f, e := winOpen(current, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, false)
		if errors.Is(e, windows.ERROR_PATH_NOT_FOUND) || errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
			if !create {
				return fail(e)
			}
			sa, e := privateSecurity(true)
			if e != nil {
				return fail(e)
			}
			name, e := windowsStorePath(current)
			if e != nil {
				return fail(e)
			}
			// Publish newly owned directories with a write-through same-volume
			// move. No existing directory ACL is modified.
			staging, e := uniqueStoreName(filepath.Base(current), "tmp")
			if e != nil {
				return fail(e)
			}
			stagedPath := filepath.Join(filepath.Dir(current), staging)
			staged, e := windowsStorePath(stagedPath)
			if e != nil {
				return fail(e)
			}
			if e = windows.CreateDirectory(staged, sa); e != nil {
				return fail(e)
			}
			e = windows.MoveFileEx(staged, name, windows.MOVEFILE_WRITE_THROUGH)
			if e != nil {
				_ = windows.RemoveDirectory(staged)
				if !errors.Is(e, windows.ERROR_ALREADY_EXISTS) && !errors.Is(e, windows.ERROR_FILE_EXISTS) {
					return fail(e)
				}
			}
			f, e = winOpen(current, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, false)
			if e != nil {
				return fail(e)
			}
		} else if e != nil {
			return fail(e)
		}
		p.ancestors = append(p.ancestors, f)
		info, e := f.Stat()
		if e != nil {
			return fail(e)
		}
		if !info.IsDir() {
			return fail(&Error{Code: ConfigInvalid})
		}
		if e = checkWindowsACL(f, false); e != nil {
			return fail(e)
		}
	}
	if len(p.ancestors) == 0 {
		return fail(&Error{Code: ConfigPermissionDenied})
	}
	p.file = p.ancestors[len(p.ancestors)-1]
	sd, e := windows.GetSecurityInfo(windows.Handle(p.file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return fail(e)
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return fail(e)
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return fail(e)
	}
	if !owner.Equals(user.User.Sid) && owner.String() != "S-1-5-18" && owner.String() != "S-1-5-32-544" {
		return fail(&Error{Code: ConfigPermissionDenied, Path: path})
	}
	return p, nil
}
func (p *storeParent) close() {
	for i := len(p.ancestors) - 1; i >= 0; i-- {
		_ = p.ancestors[i].Close()
	}
}
func (p *storeParent) verify() error {
	f, e := winOpen(p.path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, false)
	if e != nil {
		return e
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil {
		return e
	}
	b, e := p.file.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(a, b) {
		return &Error{Code: ConfigConflict, Path: p.path}
	}
	return nil
}
func (p *storeParent) lock(ctx context.Context, name string, shared, create bool) (func(), error) {
	mode := uint32(windows.OPEN_EXISTING)
	access := uint32(windows.GENERIC_READ)
	if create {
		mode = windows.OPEN_ALWAYS
		access |= windows.GENERIC_WRITE
	}
	f, err := winOpen(filepath.Join(p.path, name), access, mode, windows.FILE_ATTRIBUTE_NORMAL, create)
	if err != nil {
		return nil, err
	}
	if err = checkWindowsACL(f, true); err != nil {
		f.Close()
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		f.Close()
		return nil, err
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		f.Close()
		return nil, &Error{Code: ConfigLinkedPath}
	}
	if reused, e := reusesStoreLock(ctx, f); e != nil {
		_ = f.Close()
		return nil, e
	} else if reused {
		_ = f.Close()
		return func() {}, nil
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	var ov windows.Overlapped
	for {
		if ctx.Err() != nil {
			f.Close()
			return nil, &Error{Code: ConfigLockTimeout}
		}
		err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &ov)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, &Error{Code: ConfigLockTimeout}
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !shared && create {
		if err = rememberStoreLock(ctx, f); err != nil {
			_ = f.Close()
			return nil, err
		}
	}

	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov); _ = f.Close() }, nil
}
func (p *storeParent) read(name string) (Snapshot, error) {
	path := filepath.Join(p.path, name)
	f, e := winOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if e != nil {
		return Snapshot{}, e
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); e != nil {
		return Snapshot{}, e
	}
	if info.NumberOfLinks != 1 {
		return Snapshot{}, &Error{Code: ConfigLinkedPath}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return Snapshot{}, &Error{Code: ConfigInvalid}
	}
	if e = checkWindowsACL(f, true); e != nil {
		return Snapshot{}, e
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if e != nil {
		return Snapshot{}, e
	}
	if len(data) > MaxDocumentBytes {
		return Snapshot{}, &Error{Code: ConfigInvalid}
	}
	return Snapshot{Bytes: data, PhysicalPath: path}, nil
}
func uniqueStoreName(base, kind string) (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base + "." + kind + "-" + hex.EncodeToString(b[:]), nil
}
func (p *storeParent) writeArtifact(base, kind string, data []byte) (string, error) {
	name, e := uniqueStoreName(base, kind)
	if e != nil {
		return "", e
	}
	f, e := winOpen(filepath.Join(p.path, name), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, true)
	if e != nil {
		return "", e
	}
	e = finishArtifact(f, data)
	if e != nil {
		p.discard(name)
		return "", e
	}
	published, e := uniqueStoreName(base, kind)
	if e != nil {
		p.discard(name)
		return "", e
	}
	source, e := windowsStorePath(filepath.Join(p.path, name))
	if e != nil {
		p.discard(name)
		return "", e
	}
	target, e := windowsStorePath(filepath.Join(p.path, published))
	if e != nil {
		p.discard(name)
		return "", e
	}
	if e = windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH); e != nil {
		p.discard(name)
		return "", e
	}
	p.dirty[published] = true
	return published, nil
}
func (p *storeParent) publish(ctx context.Context, temp, name string, exists bool, original, replacement []byte) error {
	if e := p.verify(); e != nil {
		return e
	}
	targetPath := filepath.Join(p.path, name)
	tempPath := filepath.Join(p.path, temp)
	target, e := windowsStorePath(targetPath)
	if e != nil {
		return e
	}
	source, e := windowsStorePath(tempPath)
	if e != nil {
		return e
	}
	if !exists {
		e := windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH)
		if e == nil {
			delete(p.dirty, temp)
			p.dirty[name] = true
			return nil
		}
		visible, readErr := readFileSnapshotUnmanaged(targetPath, MaxDocumentBytes)
		staged, tempErr := readFileSnapshotUnmanaged(tempPath, MaxDocumentBytes)
		if readErr == nil && bytes.Equal(visible.Bytes, replacement) {
			return &Error{Code: ConfigCommitUncertain, Path: targetPath}
		}
		if tempErr != nil || !bytes.Equal(staged.Bytes, replacement) {
			return &Error{Code: ConfigRecoveryRequired, Path: targetPath}
		}
		if os.IsNotExist(readErr) {
			return e
		}
		if readErr == nil && (errors.Is(e, windows.ERROR_ALREADY_EXISTS) || errors.Is(e, windows.ERROR_FILE_EXISTS)) {
			return &Error{Code: ConfigConflict, Path: targetPath}
		}
		return &Error{Code: ConfigRecoveryRequired, Path: targetPath}
	}
	// ReplaceFileW makes the replacement stream the visible file. Copy the
	// selected file's DACL (including its inheritance protection state) onto
	// that stream first, so a successful content update cannot silently change
	// an administrator-customized but still-private ACL.
	if e := preserveWindowsDACL(targetPath, tempPath); e != nil {
		return e
	}
	backup, e := uniqueStoreName(name, "backup")
	if e != nil {
		return e
	}
	backupPath := filepath.Join(p.path, backup)
	backupName, e := windowsStorePath(backupPath)
	if e != nil {
		return e
	}
	return p.replaceWithRecovery(ctx, temp, name, backup, original, replacement, func() error {
		ok, _, callErr := replaceFileW.Call(uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(backupName)), 0, 0, 0)
		if ok != 0 {
			return nil
		}
		return callErr
	})
}

func preserveWindowsDACL(source, destination string) error {
	sd, err := windows.GetNamedSecurityInfo(extendedWindowsPath(source), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		if err != nil {
			return err
		}
		return &Error{Code: ConfigPermissionDenied, Path: source}
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		info = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(extendedWindowsPath(destination), windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
}

// replaceWithRecovery is the narrow native-call boundary, allowing tests to
// supply documented partial-replacement outcomes without production switches.
func (p *storeParent) replaceWithRecovery(ctx context.Context, temp, name, backup string, original, replacement []byte, replace func() error) error {
	targetPath := filepath.Join(p.path, name)
	tempPath := filepath.Join(p.path, temp)
	backupPath := filepath.Join(p.path, backup)
	deadline := time.Now().Add(time.Second)
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return &Error{Code: ConfigLockTimeout, Path: targetPath}
		}
		callErr := replace()
		if callErr == nil {
			delete(p.dirty, temp)
			p.dirty[name] = true
			p.dirty[backup] = true
			return nil
		}
		// Classification occurs under the target lock. Never remove a possible
		// recovery temp, and never convert ReplaceFile into remove+rename.
		current, readErr := readFileSnapshotUnmanaged(targetPath, MaxDocumentBytes)
		tmp, tmpErr := readFileSnapshotUnmanaged(tempPath, MaxDocumentBytes)
		previous, backupErr := readFileSnapshotUnmanaged(backupPath, MaxDocumentBytes)
		if readErr == nil && bytes.Equal(current.Bytes, replacement) {
			return &Error{Code: ConfigCommitUncertain, Path: targetPath}
		}
		originalIntact := readErr == nil && bytes.Equal(current.Bytes, original)
		tempIntact := tmpErr == nil && bytes.Equal(tmp.Bytes, replacement)
		backupSafe := os.IsNotExist(backupErr) || (backupErr == nil && bytes.Equal(previous.Bytes, original))
		if !originalIntact || !tempIntact || !backupSafe {
			return &Error{Code: ConfigRecoveryRequired, Path: targetPath}
		}
		if errors.Is(callErr, windows.ERROR_SHARING_VIOLATION) && attempt < 2 && time.Now().Add(50*time.Millisecond).Before(deadline) {
			select {
			case <-ctx.Done():
				return &Error{Code: ConfigLockTimeout, Path: targetPath}
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		return callErr
	}
	return &Error{Code: ConfigRecoveryRequired, Path: targetPath}
}

// Windows has no supported directory FlushFileBuffers equivalent. Fresh
// directory/file namespace publication uses MOVEFILE_WRITE_THROUGH. Existing
// replacement uses ReplaceFileW without unsupported flags, followed by explicit
// FlushFileBuffers of the visible target and replacement backup. Native storage
// qualification is required; this is not a universal power-loss guarantee.
func (p *storeParent) sync() error {
	for name := range p.dirty {
		f, e := winOpen(filepath.Join(p.path, name), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
		if e != nil {
			return e
		}
		e = f.Sync()
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		delete(p.dirty, name)
	}
	return nil
}
func (p *storeParent) discard(name string) {
	_ = os.Remove(filepath.Join(p.path, name))
	delete(p.dirty, name)
}

func checkInitEntry(path string) error {
	f, e := winOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if e != nil {
		return e
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); e != nil {
		return e
	}
	if info.NumberOfLinks > 1 {
		return &Error{Code: ConfigLinkedPath, Path: path}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return &Error{Code: ConfigInvalid, Path: path}
	}
	return nil
}

// checkInitEntryContext waits only for the transient directory-publication
// conflict produced by MoveFileEx. In particular, linked paths, permission
// failures, and every other validation result remain immediate failures.
func checkInitEntryContext(ctx context.Context, path string, check func(string) error) error {
	for {
		e := check(path)
		var errno windows.Errno
		if !errors.As(e, &errno) || errno != windows.ERROR_SHARING_VIOLATION {
			return e
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return &Error{Code: ConfigLockTimeout, Path: path}
		case <-timer.C:
		}
	}
}

func readInitSnapshot(ctx context.Context, path string) (Snapshot, error) {
	// The general read adapter follows final links. Initialization must reject
	// that spelling before the adapter resolves it to its regular-file target.
	// An initializer may concurrently publish the containing directory, so this
	// unguarded probe waits out only ERROR_SHARING_VIOLATION within the caller's
	// existing initialization deadline.
	if e := checkInitEntryContext(ctx, path, checkInitEntry); e != nil && !os.IsNotExist(e) {
		return Snapshot{}, e
	}
	// Keep link checks inside the shared read so they cannot race a cooperating
	// replacement. Unlike mutation, an existing no-op does not require private ACLs.
	return readWindowsSnapshotContext(ctx, path, MaxDocumentBytes, func(p string, limit int) (Snapshot, error) {
		if e := checkInitEntry(path); e != nil {
			return Snapshot{}, e
		}
		snap, e := readFileSnapshotUnmanaged(p, limit)
		if e != nil {
			return Snapshot{}, e
		}
		if e = checkInitEntry(path); e != nil {
			return Snapshot{}, e
		}
		return snap, nil
	})
}
