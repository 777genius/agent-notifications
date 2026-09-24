//go:build windows

package windowsacl

import "golang.org/x/sys/windows"

// ForeignReadOnlyFileRights cannot mutate a managed file, directory,
// descendants, owner, or DACL. Unknown rights are rejected.
const ForeignReadOnlyFileRights = windows.FILE_READ_DATA | windows.FILE_READ_EA |
	windows.FILE_EXECUTE | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL |
	windows.SYNCHRONIZE | windows.GENERIC_READ | windows.GENERIC_EXECUTE

func AllowsForeignReadOnly(mask uint32) bool {
	return mask&^ForeignReadOnlyFileRights == 0
}
