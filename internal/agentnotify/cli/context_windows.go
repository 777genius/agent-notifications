//go:build windows

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errContext = errors.New("context_unavailable")

const (
	contextWindowsMaxPath = 4096
	// Like Unix mode 0600, private owner write access is allowed. Custody
	// denies write sharing while reading; no third-party ACE is accepted.
	contextWindowsPrivateRights = uint32(0x001f01ff | windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE | windows.GENERIC_ALL)
)

type contextWindowsACE struct {
	Type  uint8
	Flags uint8
	Mask  uint32
	SID   string
}

type contextWindowsSecurity struct {
	Control uint16
	Owner   string
	ACEs    []contextWindowsACE
}

type contextWindowsFileID struct {
	Volume uint64
	ID     [16]byte
}

type contextWindowsFileState struct {
	Attributes uint32
	Links      uint32
	Volume     uint32
	IndexHigh  uint32
	IndexLow   uint32
	FileID     contextWindowsFileID
	SizeHigh   uint32
	SizeLow    uint32
	WriteLow   uint32
	WriteHigh  uint32
}

type contextWindowsCustody struct {
	parents []windows.Handle
	file    *os.File
}

// ReadSecureContext is the Windows equivalent of the Unix custody contract.
// It accepts only a clean absolute local-drive path, holds every component
// without delete sharing, rejects reparse points, and reads one bounded private
// regular file from a single handle. These checks establish safe local reading,
// never trusted provenance.
func ReadSecureContext(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errContext
	}
	custody, err := contextWindowsHold(path)
	if err != nil {
		return nil, errContext
	}
	defer custody.Close()

	beforeState, beforeSecurity, err := contextWindowsInspect(custody.file)
	if err != nil || !contextWindowsPrivate(beforeState, beforeSecurity) {
		return nil, errContext
	}
	data, err := contextWindowsRead(ctx, custody.file)
	if err != nil {
		return nil, errContext
	}
	afterState, afterSecurity, err := contextWindowsInspect(custody.file)
	if err != nil || afterState != beforeState || !contextWindowsSecurityEqual(beforeSecurity, afterSecurity) || ctx.Err() != nil {
		return nil, errContext
	}
	return data, nil
}

func contextWindowsHold(path string) (*contextWindowsCustody, error) {
	parts, root, err := contextWindowsPath(path)
	if err != nil {
		return nil, err
	}
	rootName, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return nil, err
	}
	if windows.GetDriveType(rootName) != windows.DRIVE_FIXED {
		return nil, errContext
	}
	h, err := windows.CreateFile(
		rootName,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE|windows.READ_CONTROL|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	custody := &contextWindowsCustody{parents: []windows.Handle{h}}
	fail := func(err error) (*contextWindowsCustody, error) {
		custody.Close()
		return nil, err
	}
	if err = contextWindowsCheckDirectory(h); err != nil {
		return fail(err)
	}
	for index, part := range parts {
		last := index == len(parts)-1
		options := uint32(windows.FILE_DIRECTORY_FILE)
		access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES | windows.FILE_TRAVERSE | windows.READ_CONTROL | windows.SYNCHRONIZE)
		sharing := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
		if last {
			options = windows.FILE_NON_DIRECTORY_FILE
			access = windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE
			sharing = windows.FILE_SHARE_READ
		}
		next, openErr := contextWindowsOpenAt(
			custody.parents[len(custody.parents)-1],
			part,
			access,
			options,
			// All held components deny DELETE sharing. The final file also
			// denies write sharing so its contents cannot change under ReadFile.
			sharing,
		)
		if openErr != nil {
			return fail(openErr)
		}
		if last {
			file := os.NewFile(uintptr(next), "context")
			if file == nil {
				_ = windows.CloseHandle(next)
				return fail(errors.New("context handle collection failed"))
			}
			custody.file = file
			if !contextWindowsRegular(next) {
				return fail(errors.New("context is not a regular file"))
			}
			if physical, physicalErr := contextWindowsPhysicalPath(next); physicalErr != nil ||
				!strings.EqualFold(physical, `\\?\`+path) {
				return fail(errors.New("context physical path differs"))
			}
			return custody, nil
		}
		if err = contextWindowsCheckDirectory(next); err != nil {
			_ = windows.CloseHandle(next)
			return fail(err)
		}
		custody.parents = append(custody.parents, next)
	}
	return fail(errors.New("context path has no final component"))
}

func (c *contextWindowsCustody) Close() {
	if c == nil {
		return
	}
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
	for index := len(c.parents) - 1; index >= 0; index-- {
		_ = windows.CloseHandle(c.parents[index])
	}
	c.parents = nil
}

func contextWindowsPath(path string) ([]string, string, error) {
	if len(path) == 0 || len(path) > contextWindowsMaxPath || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", errContext
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || len(path) <= len(volume)+1 {
		return nil, "", errContext
	}
	root := volume + `\`
	relative := strings.TrimPrefix(path, root)
	if relative == "" || strings.Contains(relative, "/") {
		return nil, "", errContext
	}
	parts := strings.Split(relative, `\`)
	for _, part := range parts {
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			base == "CONIN$" || base == "CONOUT$"
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
			base[3] >= '1' && base[3] <= '9' {
			reserved = true
		}
		if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
			suffix := base[3:]
			reserved = reserved || suffix == "¹" || suffix == "²" || suffix == "³"
		}
		if part == "" || part == "." || part == ".." ||
			strings.ContainsAny(part, "\x00:\\/<>\"|?*") ||
			strings.ContainsFunc(part, func(r rune) bool { return r < 32 }) ||
			strings.TrimRight(part, " .") != part || reserved {
			return nil, "", errContext
		}
	}
	return parts, root, nil
}

func contextWindowsOpenAt(parent windows.Handle, name string, access, options, sharing uint32) (windows.Handle, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\x00\\/:") {
		return 0, errContext
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		access|windows.SYNCHRONIZE,
		&attrs,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		sharing,
		windows.FILE_OPEN,
		options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if statusErr, ok := err.(windows.NTStatus); ok {
		err = statusErr.Errno()
	}
	return handle, err
}

func contextWindowsCheckDirectory(handle windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != windows.FILE_ATTRIBUTE_DIRECTORY {
		return errContext
	}
	return nil
}

func contextWindowsRegular(handle windows.Handle) bool {
	var info windows.ByHandleFileInformation
	fileType, typeErr := windows.GetFileType(handle)
	return windows.GetFileInformationByHandle(handle, &info) == nil && typeErr == nil &&
		fileType == windows.FILE_TYPE_DISK &&
		info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) == 0
}

func contextWindowsPhysicalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		return "", errContext
	}
	return windows.UTF16ToString(buffer[:n]), nil
}

func contextWindowsInspect(file *os.File) (contextWindowsFileState, contextWindowsSecurity, error) {
	var state contextWindowsFileState
	var security contextWindowsSecurity
	if file == nil {
		return state, security, errContext
	}
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return state, security, err
	}
	state = contextWindowsFileState{
		Attributes: info.FileAttributes,
		Links:      info.NumberOfLinks,
		Volume:     info.VolumeSerialNumber,
		IndexHigh:  info.FileIndexHigh,
		IndexLow:   info.FileIndexLow,
		SizeHigh:   info.FileSizeHigh,
		SizeLow:    info.FileSizeLow,
		WriteLow:   info.LastWriteTime.LowDateTime,
		WriteHigh:  info.LastWriteTime.HighDateTime,
	}
	fileID := contextWindowsFileID{}
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&fileID)),
		uint32(unsafe.Sizeof(fileID)),
	); err != nil || fileID.Volume == 0 || fileID.ID == [16]byte{} {
		return state, security, errContext
	}
	state.FileID = fileID
	security, err := contextWindowsSecuritySnapshot(handle)
	runtime.KeepAlive(file)
	return state, security, err
}

func contextWindowsSecuritySnapshot(handle windows.Handle) (contextWindowsSecurity, error) {
	var snapshot contextWindowsSecurity
	sd, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return snapshot, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return snapshot, err
	}
	if owner == nil {
		return snapshot, errContext
	}
	snapshot.Owner = owner.String()
	control, _, err := sd.Control()
	if err != nil {
		return snapshot, err
	}
	snapshot.Control = uint16(control)
	acl, _, err := sd.DACL()
	if err != nil {
		return snapshot, err
	}
	if acl == nil {
		return snapshot, errContext
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, index, &ace); err != nil {
			return snapshot, err
		}
		// Other ACE layouts place the SID at different offsets. Refuse them
		// before interpreting any SID bytes as ACCESS_ALLOWED_ACE.
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < 16 {
			return snapshot, errContext
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		count := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(sid)) + 1))
		if count > 15 || uint32(ace.Header.AceSize) < 8+8+4*uint32(count) || !sid.IsValid() {
			return snapshot, errContext
		}
		snapshot.ACEs = append(snapshot.ACEs, contextWindowsACE{
			Type:  ace.Header.AceType,
			Flags: ace.Header.AceFlags,
			Mask:  uint32(ace.Mask),
			SID:   sid.String(),
		})
	}
	runtime.KeepAlive(sd)
	return snapshot, nil
}

func contextWindowsPrivate(state contextWindowsFileState, security contextWindowsSecurity) bool {
	if state.Attributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 ||
		state.Links != 1 ||
		int64(state.SizeHigh)<<32|int64(state.SizeLow) > int64(ContextLimit) {
		return false
	}
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || token == nil || token.User.Sid == nil {
		return false
	}
	owner, err := windows.StringToSid(security.Owner)
	if err != nil || owner == nil || !owner.Equals(token.User.Sid) {
		return false
	}
	if security.Control&windows.SE_DACL_PRESENT == 0 || security.Control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	userSeen := 0
	systemSeen := 0
	for _, ace := range security.ACEs {
		if ace.Type != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Flags != 0 ||
			ace.Mask&^contextWindowsPrivateRights != 0 {
			return false
		}
		sid, err := windows.StringToSid(ace.SID)
		if err != nil || sid == nil {
			return false
		}
		switch {
		case sid.Equals(token.User.Sid):
			userSeen++
		case sid.IsWellKnown(windows.WinLocalSystemSid):
			systemSeen++
		default:
			return false
		}
	}
	if token.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return userSeen == 1 && systemSeen == 0
	}
	return userSeen == 1 && systemSeen == 1
}

func contextWindowsSecurityEqual(before, after contextWindowsSecurity) bool {
	if before.Control != after.Control || before.Owner != after.Owner || len(before.ACEs) != len(after.ACEs) {
		return false
	}
	for index := range before.ACEs {
		if before.ACEs[index] != after.ACEs[index] {
			return false
		}
	}
	return true
}

func contextWindowsRead(ctx context.Context, file *os.File) ([]byte, error) {
	if file == nil || ctx == nil || ctx.Err() != nil {
		return nil, errContext
	}
	data := make([]byte, 0, ContextLimit)
	buffer := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return nil, errContext
		}
		n, err := file.Read(buffer)
		if n > 0 {
			if len(data)+n > ContextLimit {
				return nil, errContext
			}
			data = append(data, buffer[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, errContext
		}
		if n == 0 {
			break
		}
	}
	if ctx.Err() != nil {
		return nil, errContext
	}
	return data, nil
}
