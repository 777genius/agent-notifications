//go:build windows

package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	oRDONLY = 0x0
	oWRONLY = 0x1
	oRDWR   = 0x2
	oCREAT  = 0x40
	oEXCL   = 0x80
)

// journalSyncDir is a no-op. FlushFileBuffers does not support directory
// handles; file contents are already flushed before rename. Same contract as
// installruntime.syncDir.
func journalSyncDir(*os.File) error { return nil }

// journalDirAccess walks ancestors with list/traverse only. The leaf also
// requests GENERIC_WRITE so FILE_ADD_FILE works on the pinned directory handle.
func journalDirAccess(leaf bool) uint32 {
	access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES | windows.FILE_TRAVERSE | windows.READ_CONTROL)
	if leaf {
		access |= windows.GENERIC_WRITE
	}
	return access
}

func openRoot(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrInvalid
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || path == volume+`\` {
		return nil, ErrInvalid
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
			return nil, ErrInvalid
		}
		next, err := journalOpenAt(h, part, journalDirAccess(i == len(parts)-1), windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE, true)
		windows.CloseHandle(h)
		if err != nil {
			return nil, wrap(err)
		}
		h = next
		info, err := journalFileInfo(h)
		if err != nil {
			windows.CloseHandle(h)
			return nil, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			windows.CloseHandle(h)
			return nil, ErrRepair
		}
		if i == len(parts)-1 {
			if err = journalRequirePrivate(h); err != nil {
				windows.CloseHandle(h)
				return nil, ErrRepair
			}
		}
	}
	return os.NewFile(uintptr(h), path), nil
}

func openFile(dir *os.File, name string, flags int) (*os.File, error) {
	acc := flags & 0x3
	access := uint32(windows.GENERIC_READ | windows.READ_CONTROL)
	switch acc {
	case oWRONLY:
		access = windows.GENERIC_WRITE | windows.READ_CONTROL
	case oRDWR:
		access = windows.GENERIC_READ | windows.GENERIC_WRITE | windows.READ_CONTROL
	}
	create := flags&oCREAT != 0
	excl := flags&oEXCL != 0
	disposition := uint32(windows.FILE_OPEN)
	if create && excl {
		disposition = windows.FILE_CREATE
		access |= windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE
	} else if create {
		disposition = windows.FILE_OPEN_IF
		access |= windows.WRITE_DAC | windows.WRITE_OWNER
	}
	share := uint32(windows.FILE_SHARE_READ)
	if name == "lock" {
		share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE
		access |= windows.GENERIC_READ | windows.GENERIC_WRITE
	}
	h, err := journalOpenAt(windows.Handle(dir.Fd()), name, access, disposition, windows.FILE_NON_DIRECTORY_FILE, share == windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil {
		return nil, err
	}
	if create {
		if err = journalRestrictPrivate(h); err != nil {
			windows.CloseHandle(h)
			return nil, err
		}
	}
	info, err := journalFileInfo(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(h)
		return nil, ErrRepair
	}
	if err = journalRequirePrivate(h); err != nil {
		windows.CloseHandle(h)
		return nil, ErrRepair
	}
	return os.NewFile(uintptr(h), name), nil
}

func lock(ctx context.Context, dir *os.File, create bool) (*os.File, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	flags := oRDWR
	if create {
		flags |= oCREAT
	}
	f, e := openFile(dir, "lock", flags)
	if e != nil {
		return nil, wrap(e)
	}
	for {
		if e = ctx.Err(); e != nil {
			_ = f.Close()
			return nil, e
		}
		var overlapped windows.Overlapped
		e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if e == nil {
			break
		}
		if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
			_ = f.Close()
			return nil, e
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	a, e := journalFileInfo(windows.Handle(f.Fd()))
	if e != nil {
		_ = f.Close()
		return nil, e
	}
	named, e := openFile(dir, "lock", oRDONLY)
	if e != nil {
		_ = f.Close()
		return nil, ErrRepair
	}
	b, e := journalFileInfo(windows.Handle(named.Fd()))
	_ = named.Close()
	if e != nil || a.VolumeSerialNumber != b.VolumeSerialNumber || a.FileIndexHigh != b.FileIndexHigh || a.FileIndexLow != b.FileIndexLow || a.NumberOfLinks != 1 {
		_ = f.Close()
		return nil, ErrRepair
	}
	return f, nil
}

func readBounded(dir *os.File, name string, max int) ([]byte, error) {
	f, e := openFile(dir, name, oRDONLY)
	if e != nil {
		return nil, wrap(e)
	}
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if st.Size() > int64(max) {
		return nil, ErrRepair
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > max {
		return nil, ErrRepair
	}
	return b, nil
}

func (s *Store) read(dir *os.File) (*disk, error) {
	ns, e := readBounded(dir, "namespace", 65)
	if e != nil {
		return nil, e
	}
	if len(ns) != 65 || ns[64] != '\n' || !isHex(string(ns[:64])) {
		return nil, ErrRepair
	}
	b, e := readBounded(dir, "journal.json", s.limits.Bytes)
	if e != nil {
		return nil, e
	}
	if e = strictJSON(b); e != nil {
		return nil, wrap(e)
	}
	var d disk
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&d); e != nil {
		return nil, wrap(e)
	}
	canonical, e := json.Marshal(d)
	if e != nil {
		return nil, wrap(e)
	}
	var compact bytes.Buffer
	if e = json.Compact(&compact, b); e != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return nil, ErrRepair
	}
	if d.Namespace != string(ns[:64]) || (s.namespace != "" && s.namespace != d.Namespace) {
		return nil, ErrRepair
	}
	if e = d.validate(s); e != nil {
		return nil, e
	}
	return &d, nil
}

func (s *Store) transaction(ctx context.Context, fn func(*disk) (bool, error)) error {
	dir, e := openRoot(s.root)
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	l, e := lock(ctx, dir, false)
	if e != nil {
		return e
	}
	defer func() { _ = l.Close() }()
	d, e := s.read(dir)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	changed, e := fn(d)
	if changed {
		if writeErr := s.write(ctx, dir, d); writeErr != nil {
			return writeErr
		}
	}
	if e != nil {
		return e
	}
	return nil
}

func (s *Store) fail(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}

func (s *Store) write(ctx context.Context, dir *os.File, d *disk) error {
	if e := d.validate(s); e != nil {
		return e
	}
	records := d.Records
	d.Records = map[string]Record{}
	header, e := json.Marshal(d)
	d.Records = records
	if e != nil {
		return e
	}
	size := len(header)
	for key, r := range records {
		entry, err := json.Marshal(r)
		if err != nil {
			return err
		}
		size += len(key) + 3 + len(entry) + 1
		if size > s.limits.Bytes {
			return ErrFull
		}
	}
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if len(b) > s.limits.Bytes {
		return ErrFull
	}
	if e = s.fail("before_temp"); e != nil {
		return e
	}
	old, e := openFile(dir, "snapshot.tmp", oRDONLY)
	if e == nil {
		_ = old.Close()
		if e = unlinkAt(dir, "snapshot.tmp"); e != nil {
			return e
		}
	} else if !journalNotExist(e) {
		return e
	}
	f, e := openFile(dir, "snapshot.tmp", oWRONLY|oCREAT|oEXCL)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	defer func() { _ = unlinkAt(dir, "snapshot.tmp") }()
	half := len(b) / 2
	if _, e = f.Write(b[:half]); e != nil {
		return e
	}
	if e = s.fail("partial_write"); e != nil {
		return e
	}
	if _, e = f.Write(b[half:]); e != nil {
		return e
	}
	if e = s.fail("before_file_sync"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = s.fail("after_file_sync"); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = s.fail("before_rename"); e != nil {
		return e
	}
	if e = renameAt(dir, "snapshot.tmp", "journal.json", true); e != nil {
		return e
	}
	if e = s.fail("after_rename"); e != nil {
		return e
	}
	if e = journalSyncDir(dir); e != nil {
		return e
	}
	return s.fail("after_directory_sync")
}

func (s *Store) bootstrap(ctx context.Context) error {
	dir, e := openRoot(s.root)
	if e != nil {
		return e
	}
	defer func() { _ = dir.Close() }()
	for _, name := range []string{"namespace", "journal.json", "snapshot.tmp"} {
		f, err := openFile(dir, name, oRDONLY)
		if err == nil {
			_ = f.Close()
			return ErrRepair
		}
		if !journalNotExist(err) {
			return wrap(err)
		}
	}
	l, e := lock(ctx, dir, true)
	if e != nil {
		return e
	}
	defer func() { _ = l.Close() }()
	names, e := dir.Readdirnames(-1)
	if e != nil {
		return e
	}
	for _, n := range names {
		if n != "lock" {
			return ErrRepair
		}
	}
	ns, e := token()
	if e != nil {
		return e
	}
	f, e := openFile(dir, "namespace", oWRONLY|oCREAT|oEXCL)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	if _, e = f.WriteString(ns + "\n"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = journalSyncDir(dir); e != nil {
		return e
	}
	if e = s.fail("after_namespace_sync"); e != nil {
		return e
	}
	s.namespace = ns
	d := &disk{Version: 1, Namespace: ns, Limits: s.limits, Records: map[string]Record{}, Events: []event{}}
	if _, e = s.advance(d); e != nil {
		return e
	}
	return s.write(ctx, dir, d)
}

func unlinkAt(dir *os.File, name string) error {
	h, err := journalOpenAt(windows.Handle(dir.Fd()), name, windows.DELETE, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, false)
	if err != nil {
		return err
	}
	remove := byte(1)
	err = windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, &remove, 1)
	_ = windows.CloseHandle(h)
	return err
}

func renameAt(dir *os.File, from, to string, replace bool) error {
	h, err := journalOpenAt(windows.Handle(dir.Fd()), from, windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, false)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	encoded, err := windows.UTF16FromString(to)
	if err != nil {
		return err
	}
	encoded = encoded[:len(encoded)-1]
	type renameInfo struct {
		Replace uint32
		Root    windows.Handle
		Length  uint32
		Name    [1]uint16
	}
	offset := unsafe.Offsetof(renameInfo{}.Name)
	buffer := make([]byte, int(offset)+len(encoded)*2)
	info := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	if replace {
		info.Replace = windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	info.Root = windows.Handle(dir.Fd())
	info.Length = uint32(len(encoded) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[offset])), len(encoded)), encoded)
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(h, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
	if err != nil {
		if nt, ok := err.(windows.NTStatus); ok {
			return nt.Errno()
		}
		return err
	}
	return nil
}

func journalOpenAt(parent windows.Handle, name string, access, disposition, options uint32, shareWrite bool) (windows.Handle, error) {
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
	} else if shareWrite {
		share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE
	}
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, share, disposition, options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if nt, ok := err.(windows.NTStatus); ok {
			return 0, nt.Errno()
		}
		return 0, err
	}
	return handle, nil
}

func journalFileInfo(h windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(h, &info)
	return info, err
}

func journalNotExist(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func journalCurrentSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func journalRestrictPrivate(h windows.Handle) error {
	sid, err := journalCurrentSID()
	if err != nil {
		return err
	}
	info, err := journalFileInfo(h)
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

func journalRequirePrivate(h windows.Handle) error {
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
