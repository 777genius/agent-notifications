//go:build windows

package installruntime

import (
	"fmt"
	"io"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// CheckPrivateCacheRoot requires an existing local non-reparse directory with
// the installation lock's owner/private-DACL policy. It never repairs access.
func CheckPrivateCacheRoot(root string) error {
	handles, err := privateCacheRootHandles(root)
	defer closeWindowsParents(handles)
	return err
}

// ReadPrivateCacheDocument validates the root and existing single-link file
// before reading bounded bytes. Both checks use the held, relative-open handles
// with the installation lock's owner/private-DACL policy; no ACL is changed.
// A missing document returns an error recognizable by os.IsNotExist.
func ReadPrivateCacheDocument(root, name string, limit int64) ([]byte, error) {
	handles, err := privateCacheRootHandles(root)
	defer closeWindowsParents(handles)
	if err != nil {
		return nil, err
	}
	f, err := windowsRegularAt(handles[len(handles)-1], name, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	handle := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.NumberOfLinks != 1 {
		return nil, fmt.Errorf("private cache document requires a single-link inode")
	}
	if err = privateWindowsHandle(handle); err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || limit <= 0 || stat.Size() < 0 || stat.Size() > limit {
		return nil, fmt.Errorf("private cache document must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("private cache document exceeds size limit")
	}
	return data, nil
}

func privateCacheRootHandles(root string) ([]windows.Handle, error) {
	handles, _, err := windowsParents(root, false)
	if err != nil {
		return handles, err
	}
	// windowsParents checks/pins ancestors, but its handles lack READ_CONTROL.
	// Open the root relative to its held parent with security-query access.
	handle, err := windowsOpenAt(handles[len(handles)-1], filepath.Base(root),
		windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE,
		windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return handles, err
	}
	handles = append(handles, handle)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		return handles, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return handles, fmt.Errorf("private cache root must be a non-reparse directory")
	}
	return handles, privateWindowsHandle(handle)
}
