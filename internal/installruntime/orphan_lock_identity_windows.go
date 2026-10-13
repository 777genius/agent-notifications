package installruntime

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Permanent locks remain open for writing while held. Observe only their
// metadata and security, without acquiring a lock or sharing deletion.
func orphanPermanentLockIdentity(path string) (string, error) {
	parents, _, err := windowsParents(path, false)
	defer closeWindowsParents(parents)
	if err != nil {
		return "", err
	}
	handle, err := windowsOpenAtWithSharing(parents[len(parents)-1], filepath.Base(path),
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE, nil, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		return "", fmt.Errorf("permanent lock must be a regular non-reparse single-link file")
	}
	if err := privateWindowsHandle(handle); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
