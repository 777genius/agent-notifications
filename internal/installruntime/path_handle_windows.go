package installruntime

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Resolve exactly one component relative to the held parent. Pinning by itself
// is insufficient: an absolute reopen could traverse a substituted ancestor.
func windowsOpenAt(parent windows.Handle, name string, access, disposition, options uint32) (windows.Handle, error) {
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
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, windowsShare(options), disposition, options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return handle, windowsStatusError(err)
}

// Directories must remain shareable for create/rename of children. Leaf CAS
// files keep exclusive write/delete so a held inode cannot be replaced.
func windowsShare(options uint32) uint32 {
	if options&windows.FILE_DIRECTORY_FILE != 0 {
		return windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	}
	return windows.FILE_SHARE_READ
}
func windowsStatusError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
func windowsDeleteHandle(handle windows.Handle) error {
	remove := byte(1)
	return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &remove, 1)
}
func windowsRenameHandle(handle, parent windows.Handle, name string, replace bool) error {
	encoded, err := windows.UTF16FromString(name)
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
	info.Root = parent
	info.Length = uint32(len(encoded) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buffer[offset])), len(encoded)), encoded)
	var status windows.IO_STATUS_BLOCK
	return windowsStatusError(windows.NtSetInformationFile(handle, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation))
}
func windowsRegularAt(parent windows.Handle, name string, deleting bool) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if deleting {
		access |= windows.DELETE
	}
	handle, err := windowsOpenAt(parent, name, access, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(handle, &info)
	if err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(handle)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("managed Windows file must be regular and non-reparse")
	}
	return os.NewFile(uintptr(handle), name), nil
}
