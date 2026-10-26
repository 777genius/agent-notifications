package installruntime

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func configOnlyDirectory(root string) (*os.File, error) {
	parents, _, err := windowsParents(filepath.Join(root, "config.json"), false)
	defer closeWindowsParents(parents)
	if err != nil {
		return nil, err
	}
	if len(parents) < 2 {
		return nil, fmt.Errorf("config-only root must have a physical parent")
	}
	handle, err := windowsOpenAt(parents[len(parents)-2], filepath.Base(root), windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), root), nil
}

func configOnlyEntryInfo(directory *os.File, name string) (os.FileInfo, error) {
	handle, err := windowsOpenAt(windows.Handle(directory.Fd()), name, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	defer func() { _ = file.Close() }()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return nil, err
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return nil, fmt.Errorf("config-only entry requires a single-link inode")
	}
	if err = privateWindowsHandle(windows.Handle(file.Fd())); err != nil {
		return nil, err
	}
	return file.Stat()
}
