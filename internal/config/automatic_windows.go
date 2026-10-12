//go:build windows

package config

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// The additive exception has native KnownFolder provenance. A PROGRAMDATA
// override uses ordinary strict ancestry checks. The SID directory is a direct
// child, avoiding adoption of a shared application parent precreated by others.
type windowsAutomaticAnchor struct {
	root   string
	dir    string
	native bool
}

func nativeWindowsAutomaticAnchor() (windowsAutomaticAnchor, error) {
	root, provided := os.LookupEnv("PROGRAMDATA")
	return windowsAutomaticAnchorForRoot(root, provided)
}

func windowsAutomaticAnchorForRoot(root string, provided bool) (windowsAutomaticAnchor, error) {
	native, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return windowsAutomaticAnchor{}, err
	}
	if !provided {
		root = native
	}
	if !validAbsolute("windows", root) || filepath.VolumeName(root) == "" {
		return windowsAutomaticAnchor{}, &Error{Code: ConfigBaseUnavailable}
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return windowsAutomaticAnchor{}, err
	}
	root = filepath.Clean(root)
	return windowsAutomaticAnchor{root: root, dir: filepath.Join(root, "agent-notifications-"+user.User.Sid.String()), native: sameStorePath(root, filepath.Clean(native), "windows")}, nil
}

func snapshotWindowsAutomatic(e *EnvSnapshot) {
	root, provided := e.Vars["PROGRAMDATA"]
	anchor, err := windowsAutomaticAnchorForRoot(root, provided)
	e.windowsFallbackErr = err
	if err == nil {
		e.windowsFallback = filepath.Join(anchor.dir, "config.json")
		e.windowsFallbackCheck = anchor.checkSelection
	}
	e.windowsPreflight = preflightWindowsAutomatic
}

// preflightWindowsAutomatic uses requested access on existing handles, without
// writing entries or creating locks. It checks the profile guard's parent and
// the deepest existing directory needed for normal APPDATA initialization.
func preflightWindowsAutomatic(home, target string) error {
	physicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	check := func(path string, access uint32) error {
		parent, err := openWindowsStoreParent(path, false, nil)
		if err != nil {
			return err
		}
		defer parent.close()
		f, err := winOpen(path, windows.GENERIC_READ|access, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, false)
		if err == nil {
			err = f.Close()
		}
		return err
	}
	if err := check(physicalHome, windows.FILE_WRITE_DATA); err != nil {
		return err
	}
	dir := filepath.Dir(target)
	access := uint32(windows.FILE_WRITE_DATA)
	for {
		_, err := os.Lstat(dir)
		if err == nil {
			return check(dir, access)
		}
		if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(dir)
		if next == dir {
			return err
		}
		dir = next
		access = windows.FILE_APPEND_DATA
	}
}

func (a windowsAutomaticAnchor) checkSelection(path string, exists bool) error {
	if !sameStorePath(path, filepath.Join(a.dir, "config.json"), "windows") {
		return &Error{Code: ConfigLinkedPath, Path: path}
	}
	parent, err := openWindowsStoreParent(a.dir, false, &a)
	if os.IsNotExist(err) && !exists {
		// Validate the anchor even before its private directory exists. This
		// also proves that additive root rights have a nonempty pinned child.
		parent, err = openWindowsStoreParent(a.root, false, &a)
	}
	if err != nil {
		return err
	}
	defer parent.close()
	if !exists {
		return nil
	}
	f, err := winOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, false)
	if err != nil {
		return err
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return &Error{Code: ConfigLinkedPath, Path: path}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return &Error{Code: ConfigInvalid, Path: path}
	}
	return checkWindowsACL(f, true)
}

// Native reads have only a pathname, so bind the special ancestry policy to
// today's native KnownFolder and SID. A changed anchor cannot widen the policy
// for a frozen resolver selection: it falls back to the normal strict walk.
func windowsAutomaticAnchorForParent(path string) *windowsAutomaticAnchor {
	a, err := nativeWindowsAutomaticAnchor()
	if err != nil || !sameStorePath(filepath.Clean(path), a.dir, "windows") {
		return nil
	}
	return &a
}

func trustedWindowsOwner(owner, user *windows.SID) bool {
	return owner.Equals(user) || owner.String() == "S-1-5-18" || owner.String() == "S-1-5-32-544"
}

func checkWindowsAnchorOwner(f *os.File, systemAncestor bool) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
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
	if !trustedWindowsOwner(owner, user.User.Sid) && !(systemAncestor && owner.String() == "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464") {
		return windowsPermissionDenied(f.Name(), "untrusted_owner", "", owner.String(), 0, nil)
	}
	return nil
}

// pinWindowsAnchorChild prevents an add-writable native root from becoming
// empty while the first private directory is published. Windows permits an
// in-place reparse conversion with WRITE_DATA or WRITE_ATTRIBUTES, but only on
// an empty directory. The sharing mode of winOpen prohibits deleting this entry.
func pinWindowsAnchorChild(root string, parent *os.File) (*os.File, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		f, err := winOpen(filepath.Join(root, entry.Name()), windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, false)
		if err == nil {
			// A concurrent empty-directory junction conversion could make
			// pathname enumeration/opening reach a different parent. Bind
			// the child to the still-held real parent, not to that pathname.
			parentPath, e := windowsHandlePath(parent)
			if e == nil {
				var childPath string
				childPath, e = windowsHandlePath(f)
				if e == nil && !sameStorePath(filepath.Dir(childPath), parentPath, "windows") {
					e = &Error{Code: ConfigLinkedPath, Path: root}
				}
			}
			if e == nil {
				e = checkWindowsDirectoryHandle(parent)
			}
			if e != nil {
				f.Close()
				return nil, e
			}
			return f, nil
		}
		var ce *Error
		if os.IsNotExist(err) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) || (errors.As(err, &ce) && (ce.Code == ConfigPermissionDenied || ce.Code == ConfigLinkedPath)) {
			continue
		}
		return nil, err
	}
	return nil, windowsPermissionDenied(root, "empty_add_writable_anchor", "", "", 0, nil)
}

func windowsHandlePath(f *os.File) (string, error) {
	buffer := make([]uint16, 256)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buffer)) {
			return filepath.Clean(windows.UTF16ToString(buffer[:n])), nil
		}
		buffer = make([]uint16, n+1)
	}
}

func checkWindowsDirectoryHandle(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return &Error{Code: ConfigLinkedPath, Path: f.Name()}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return &Error{Code: ConfigInvalid, Path: f.Name()}
	}
	return nil
}
