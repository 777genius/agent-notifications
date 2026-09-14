package installruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

var windowsAfterEvacuate func() error

func windowsReplacementName(file File) string {
	data, _ := json.Marshal(struct {
		Name          string
		Before, After Identity
	}{filepath.Base(file.Path), file.Before, desired(file)})
	sum := sha256.Sum256(data)
	return ".runtime-preimage-" + hex.EncodeToString(sum[:])
}
func windowsReplacementIdentity(path string) (string, error) { return regularObjectID(path) }
func windowsHandleIdentity(f *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
func windowsCheckReplacement(f *os.File, file File) error {
	id, err := windowsHandleIdentity(f)
	if err != nil {
		return err
	}
	if file.WindowsReplacementID == "" || id != file.WindowsReplacementID {
		return fmt.Errorf("Windows replacement preimage inode changed")
	}
	got, err := windowsFileIdentity(f)
	if err != nil {
		return err
	}
	if got != file.Before {
		return fmt.Errorf("Windows replacement preimage bytes changed")
	}
	return nil
}

// A missing final name is replayable only with the exact inode bound in the
// durable transaction, evacuated under a deny-write/delete-sharing handle.
func windowsReplacementPreimage(parent windows.Handle, name string, file File) (*os.File, Identity, error) {
	f, err := windowsRegularAt(parent, name, true)
	if err == nil {
		got, err := windowsFileIdentity(f)
		if err != nil {
			f.Close()
			return nil, Identity{}, err
		}
		if got == desired(file) {
			f.Close()
			return nil, got, nil
		}
		if file.WindowsReplacementID != "" {
			id, err := windowsHandleIdentity(f)
			if err != nil || id != file.WindowsReplacementID {
				f.Close()
				return nil, Identity{}, fmt.Errorf("Windows final preimage inode changed")
			}
		}
		return f, got, nil
	}
	if !os.IsNotExist(err) {
		return nil, Identity{}, err
	}
	backup, err := windowsRegularAt(parent, windowsReplacementName(file), true)
	if os.IsNotExist(err) {
		return nil, Identity{}, nil
	}
	if err != nil {
		return nil, Identity{}, err
	}
	defer backup.Close()
	if err := windowsCheckReplacement(backup, file); err != nil {
		return nil, Identity{}, err
	}
	return nil, file.Before, nil
}
func windowsCleanupReplacement(parent windows.Handle, file File) error {
	backup, err := windowsRegularAt(parent, windowsReplacementName(file), true)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer backup.Close()
	if err := windowsCheckReplacement(backup, file); err != nil {
		return err
	}
	return windowsDeleteHandle(windows.Handle(backup.Fd()))
}
func replacementFingerprint(file File) (Identity, error) {
	if file.Remove || !file.Before.Exists {
		return Fingerprint(file.Path)
	}
	handles, anchors, err := windowsParents(file.Path, false)
	defer closeWindowsParents(handles)
	if err != nil {
		return Identity{}, err
	}
	if err = checkAnchors(file.Parents, anchors); err != nil {
		return Identity{}, err
	}
	f, got, err := windowsReplacementPreimage(handles[len(handles)-1], filepath.Base(file.Path), file)
	if f != nil {
		f.Close()
	}
	return got, err
}

func checkReplacementRollback(file File) error {
	handles, _, err := windowsParents(file.Path, false)
	defer closeWindowsParents(handles)
	if err != nil {
		return err
	}
	f, err := windowsRegularAt(handles[len(handles)-1], windowsReplacementName(file), false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	f.Close()
	return fmt.Errorf("Windows replacement must finish forward recovery before rollback")
}
