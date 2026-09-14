//go:build windows

package setup

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func renameExclusive(fd int, from, to string) error {
	if from == "" || to == "" || from == "." || to == "." || strings.ContainsAny(from, `\/:`) || strings.ContainsAny(to, `\/:`) {
		return fmt.Errorf("invalid relative Windows component")
	}
	parent := windows.Handle(fd)
	h, err := setupOpenAt(parent, from, windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
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
	info.Root = parent
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
