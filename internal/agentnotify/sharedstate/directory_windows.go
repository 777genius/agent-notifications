package sharedstate

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
)

type directory struct {
	path    string
	roots   []*installruntime.PrivateCacheRoot
	handles []windows.Handle
}

func (d *directory) close() {
	for i := len(d.handles) - 1; i >= 0; i-- {
		_ = windows.CloseHandle(d.handles[i])
	}
	for i := len(d.roots) - 1; i >= 0; i-- {
		d.roots[i].Close()
	}
}
func privateSecurity(directory bool) (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid.String()
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;" + inherit + ";FA;;;" + sid + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
func strictHandle(h windows.Handle, isDir bool) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != isDir || !isDir && info.NumberOfLinks != 1 {
		return "", fmt.Errorf("nonphysical or hardlinked endpoint node")
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	trusted := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(user.User.Sid) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return "", err
	}
	if !trusted(owner) {
		return "", fmt.Errorf("endpoint owner mismatch")
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return "", err
	}
	if acl == nil {
		return "", fmt.Errorf("endpoint needs private DACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return "", err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return "", fmt.Errorf("endpoint DACL permits foreign access")
		}
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
func openWindowsNode(path string, isDir bool, access uint32, disposition uint32, security *windows.SecurityAttributes) (windows.Handle, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if isDir {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	sharing := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	// Discovery readers may finish reading the immutable old inode during an
	// atomic publication. Locks and directory handles continue denying moves.
	if !isDir && access == windows.GENERIC_READ && disposition == windows.OPEN_EXISTING {
		sharing |= windows.FILE_SHARE_DELETE
	}
	return windows.CreateFile(ptr, access|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, sharing, security, disposition, flags, 0)
}
func openDirectory(s *Store, create bool) (*directory, []string, error) {
	d := &directory{path: s.dir}
	fail := func(err error) (*directory, []string, error) { d.close(); return nil, nil, err }
	var ids []string
	for i, path := range []string{s.root, s.namespace, s.dir} {
		if i > 0 && create {
			security, err := privateSecurity(true)
			if err != nil {
				return fail(err)
			}
			ptr, err := windows.UTF16PtrFromString(path)
			if err != nil {
				return fail(err)
			}
			err = windows.CreateDirectory(ptr, security)
			if err != nil && err != windows.ERROR_ALREADY_EXISTS {
				return fail(err)
			}
		}
		// Existing installer primitive anchors and pins every non-reparse ancestor.
		root, err := installruntime.OpenPrivateCacheRoot(path)
		if err != nil {
			return fail(err)
		}
		d.roots = append(d.roots, root)
		handle, err := openWindowsNode(path, true, windows.FILE_TRAVERSE, windows.OPEN_EXISTING, nil)
		if err != nil {
			return fail(err)
		}
		d.handles = append(d.handles, handle)
		id, err := strictHandle(handle, true)
		if err != nil {
			return fail(err)
		}
		ids = append(ids, id)
		if len(s.ids) > i && s.ids[i] != id {
			return fail(fmt.Errorf("directory identity changed"))
		}
	}
	return d, ids, nil
}
func (d *directory) open(name string, access uint32, disposition uint32, security *windows.SecurityAttributes) (*os.File, error) {
	handle, err := openWindowsNode(filepath.Join(d.path, name), false, access, disposition, security)
	if err != nil {
		return nil, err
	}
	if _, err = strictHandle(handle, false); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}
func (d *directory) ensureLock(name string) error {
	security, err := privateSecurity(false)
	if err != nil {
		return err
	}
	f, err := d.open(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW, security)
	if err == windows.ERROR_FILE_EXISTS || err == windows.ERROR_ALREADY_EXISTS {
		return d.checkLock(name)
	}
	if err != nil {
		return err
	}
	return f.Close()
}
func (d *directory) checkLock(name string) error {
	f, err := d.open(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_EXISTING, nil)
	if err != nil {
		return err
	}
	return f.Close()
}
func (d *directory) read(name string, limit int64) ([]byte, error) {
	f, err := d.open(name, windows.GENERIC_READ, windows.OPEN_EXISTING, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("endpoint too large or nonregular")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("endpoint too large")
	}
	return data, nil
}

// write publishes with POSIX replacement semantics: a reader of the old
// immutable record keeps that inode, while new opens see the replacement.
// Legacy FileRenameInformation rejects an open destination even when all its
// readers specified FILE_SHARE_DELETE, so the observation-cache writer cannot
// supply this discovery contract.
func (d *directory) write(name string, data []byte) (err error) {
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	security, err := privateSecurity(false)
	if err != nil {
		return err
	}
	temp := ".endpoint-" + hex.EncodeToString(nonce[:])
	f, err := d.open(temp, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, windows.CREATE_NEW, security)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			remove := byte(1)
			_ = windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileDispositionInfo, &remove, 1)
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	written, err := f.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	// Recheck strict credential custody immediately before replacing a target.
	// The caller retains the server lock; all ancestor directories stay pinned.
	parent := d.handles[len(d.handles)-1]
	if _, err = strictHandle(parent, true); err != nil {
		return err
	}
	target, err := d.open(name, windows.GENERIC_READ, windows.OPEN_EXISTING, nil)
	if err == nil {
		err = target.Close()
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = renameEndpoint(windows.Handle(f.Fd()), parent, name); err != nil {
		return err
	}
	published = true
	return nil
}

// FILE_RENAME_INFO with FileRenameInfoEx accepts the POSIX flag. The root
// handle confines the single-component destination to the validated directory.
// Unsupported filesystems fail closed; there is no remove-then-rename fallback.
// https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information
func renameEndpoint(source, parent windows.Handle, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:`) {
		return fmt.Errorf("invalid relative endpoint component")
	}
	encoded, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	type renameInfo struct {
		Flags  uint32
		Root   windows.Handle
		Length uint32
		Name   [1]uint16
	}
	offset := unsafe.Offsetof(renameInfo{}.Name)
	length := int(unsafe.Sizeof(renameInfo{})) + len(encoded)*2
	buffer := make([]byte, length)
	info := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.Root = parent
	info.Length = uint32((len(encoded) - 1) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[offset])), len(encoded)), encoded)
	return windows.SetFileInformationByHandle(source, windows.FileRenameInfoEx, &buffer[0], uint32(len(buffer)))
}
func (d *directory) remove(name string) error { return installruntime.RemoveConfinedName(d.path, name) }
