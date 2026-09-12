package installruntime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Hold every ancestor without write/delete sharing. This prevents rename,
// junction substitution and reparse changes while Win32 path operations run.
// Each child is opened only after its parent has been pinned and checked.
func windowsParents(path string, create bool) ([]windows.Handle, []PathAnchor, error) {
	var handles []windows.Handle
	var anchors []PathAnchor
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, fmt.Errorf("managed path must be absolute and clean")
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, nil, fmt.Errorf("managed paths require a local drive")
	}
	// Win32 normalization must not reinterpret ledger names as streams, devices,
	// or aliases of a different entry.
	for _, part := range strings.Split(strings.TrimPrefix(path, volume+`\`), `\`) {
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$"
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
			reserved = true
		}
		if part == "" || strings.ContainsAny(part, ":/<>\"|?*") || strings.TrimRight(part, " .") != part || reserved {
			return nil, nil, fmt.Errorf("ambiguous managed Windows path component: %s", part)
		}
	}
	parent := volume + `\`
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(path), parent), `\`)
	paths := []string{parent}
	for _, part := range parts {
		if part != "" {
			parent = filepath.Join(parent, part)
			paths = append(paths, parent)
		}
	}
	for index, p := range paths {
		var h windows.Handle
		var err error
		if index == 0 {
			name, e := windows.UTF16PtrFromString(p)
			if e != nil {
				return handles, anchors, e
			}
			h, err = windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		} else {
			disposition := uint32(windows.FILE_OPEN)
			if create {
				disposition = windows.FILE_OPEN_IF
			}
			h, err = windowsOpenAt(handles[len(handles)-1], filepath.Base(p), windows.FILE_READ_ATTRIBUTES|windows.FILE_TRAVERSE, disposition, windows.FILE_DIRECTORY_FILE)
		}
		if err != nil {
			return handles, anchors, err
		}
		handles = append(handles, h)
		var info windows.ByHandleFileInformation
		if err = windows.GetFileInformationByHandle(h, &info); err != nil {
			return handles, anchors, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return handles, anchors, fmt.Errorf("managed parent must be a non-reparse directory: %s", p)
		}
		anchors = append(anchors, PathAnchor{p, fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)})
	}
	return handles, anchors, nil
}
func closeWindowsParents(handles []windows.Handle) {
	for i := len(handles) - 1; i >= 0; i-- {
		windows.CloseHandle(handles[i])
	}
}
func windowsFileIdentity(f *os.File) (Identity, error) {
	st, err := f.Stat()
	if err != nil {
		return Identity{}, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return Identity{}, err
	}
	return identity(data, uint32(st.Mode().Perm())), nil
}
func windowsFingerprintAt(parent windows.Handle, name string) (Identity, error) {
	f, err := windowsRegularAt(parent, name, false)
	if os.IsNotExist(err) {
		return Identity{}, nil
	}
	if err != nil {
		return Identity{}, err
	}
	defer f.Close()
	return windowsFileIdentity(f)
}
func safeFingerprint(path string) (Identity, error) {
	h, _, err := windowsParents(path, false)
	defer closeWindowsParents(h)
	if os.IsNotExist(err) {
		return Identity{}, nil
	}
	if err != nil {
		return Identity{}, err
	}
	return windowsFingerprintAt(h[len(h)-1], filepath.Base(path))
}
func pathAnchors(path string, create bool) ([]PathAnchor, error) {
	h, a, err := windowsParents(path, create)
	defer closeWindowsParents(h)
	if !create && os.IsNotExist(err) {
		err = nil
	}
	return a, err
}

// Test seam for the final-entry race, after comparison and before rename.
var windowsBeforePublish func() error

func safePublish(file File, cas bool) error {
	h, a, err := windowsParents(file.Path, true)
	defer closeWindowsParents(h)
	if err != nil {
		return err
	}
	if err = checkAnchors(file.Parents, a); err != nil {
		return err
	}
	parent, name := h[len(h)-1], filepath.Base(file.Path)
	if file.Link != "" {
		return fmt.Errorf("managed Windows links require explicit reparse qualification")
	}
	// Deletion stays tied to the inode opened for CAS, never a later pathname.
	if file.Remove {
		f, e := windowsRegularAt(parent, name, true)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		defer f.Close()
		got, e := windowsFileIdentity(f)
		if e != nil {
			return e
		}
		if cas && got != file.Before {
			return fmt.Errorf("concurrent edit: %s", file.Path)
		}
		if cas && file.WindowsReplacementID != "" {
			id, e := windowsHandleIdentity(f)
			if e != nil || id != file.WindowsReplacementID {
				return fmt.Errorf("Windows deletion preimage inode changed")
			}
		}
		if windowsBeforePublish != nil {
			if err := windowsBeforePublish(); err != nil {
				return err
			}
		}
		return windowsDeleteHandle(windows.Handle(f.Fd()))
	}
	var previous *os.File
	if cas {
		var got Identity
		previous, got, err = windowsReplacementPreimage(parent, name, file)
		if err != nil {
			return err
		}
		if previous != nil {
			defer previous.Close()
			if file.WindowsReplacementID == "" {
				file.WindowsReplacementID, err = windowsHandleIdentity(previous)
				if err != nil {
					return err
				}
			}
		}
		if got == desired(file) {
			return windowsCleanupReplacement(parent, file)
		}
		if got != file.Before {
			return fmt.Errorf("concurrent edit: %s", file.Path)
		}
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".runtime-" + hex.EncodeToString(random[:])
	handle, err := windowsOpenAt(parent, temp, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(handle), temp)
	published := false
	defer func() {
		if !published {
			_ = windowsDeleteHandle(handle)
		}
		f.Close()
	}()
	err = f.Chmod(os.FileMode(file.Mode))
	if err == nil {
		_, err = f.Write(file.Data)
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		return err
	}
	if windowsBeforePublish != nil {
		if err = windowsBeforePublish(); err != nil {
			return err
		}
	}
	if cas && previous != nil {
		// Evacuate the compared inode by its DELETE handle, then publish without
		// replacing any concurrently created final entry. The durable transaction
		// binds the private preimage for crash replay.
		if err = windowsRenameHandle(windows.Handle(previous.Fd()), parent, windowsReplacementName(file), false); err != nil {
			return err
		}
		previous.Close()
		if windowsAfterEvacuate != nil {
			if err = windowsAfterEvacuate(); err != nil {
				return err
			}
		}
	}
	err = windowsRenameHandle(handle, parent, name, !cas)
	published = err == nil
	if err != nil {
		return err
	}
	if cas {
		return windowsCleanupReplacement(parent, file)
	}
	return nil
}

func safeRemoveDirectory(path, want string, anchors []PathAnchor) error {
	h, a, err := windowsParents(path, false)
	defer closeWindowsParents(h)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = checkAnchors(anchors, a); err != nil {
		return err
	}
	child, err := windowsOpenAt(h[len(h)-1], filepath.Base(path), windows.FILE_READ_ATTRIBUTES|windows.DELETE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(child)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(child, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow) != want {
		return fmt.Errorf("purge directory replaced: %s", path)
	}
	return windowsDeleteHandle(child)
}

func openedDirectoryIdentity(f *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("expected non-reparse directory handle")
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func readRegularFile(path string) ([]byte, error) {
	return readRegularFileLimit(path, maxManagedFile)
}

func readControlDocument(path string) ([]byte, error) {
	return readRegularFileLimit(path, maxControlDocument)
}

func readRegularFileLimit(path string, limit int64) ([]byte, error) {
	handles, _, err := windowsParents(path, false)
	defer closeWindowsParents(handles)
	if err != nil {
		return nil, err
	}
	f, err := windowsRegularAt(handles[len(handles)-1], filepath.Base(path), false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed input must be a regular non-link file")
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("managed input exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("managed input exceeds size limit")
	}
	return data, nil
}

func regularObjectID(path string) (string, error) {
	handles, _, err := windowsParents(path, false)
	defer closeWindowsParents(handles)
	if err != nil {
		return "", err
	}
	f, err := windowsRegularAt(handles[len(handles)-1], filepath.Base(path), false)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func removePhysicalDirectory(path string) error {
	handles, _, err := windowsParents(path, false)
	defer closeWindowsParents(handles)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	child, err := windowsOpenAt(handles[len(handles)-1], filepath.Base(path), windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.DELETE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("transaction blob directory is not a physical directory: %w", err)
	}
	f := os.NewFile(uintptr(child), path)
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("transaction blob directory is not a physical directory")
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		h, err := windowsOpenAt(windows.Handle(f.Fd()), name, windows.DELETE, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		delErr := windowsDeleteHandle(h)
		_ = windows.CloseHandle(h)
		if delErr != nil {
			return delErr
		}
	}
	return windowsDeleteHandle(windows.Handle(f.Fd()))
}
