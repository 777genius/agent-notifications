//go:build windows

package geminiinstall

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
	"golang.org/x/sys/windows"
)

func readNativeSettings(path string) ([]byte, error) {
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return nil, errors.New("native settings parent changed")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	defer func() { _ = f.Close() }()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return nil, errors.New("native settings must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, geminihooks.MaxSettingsBytes+1))
	if err != nil || len(data) == 0 || len(data) > geminihooks.MaxSettingsBytes {
		return nil, errors.New("native settings unavailable")
	}
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(opened, named) {
		return nil, errors.New("native settings changed")
	}
	resolved, err = filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return nil, errors.New("native settings parent changed")
	}
	return data, nil
}
