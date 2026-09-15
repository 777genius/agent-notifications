//go:build windows

package config

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// ReadFileSnapshot takes an existing managed shared lock without creating any
// entries. A lock appearing during an unmanaged snapshot forces a fresh read.
func ReadFileSnapshot(path string, limit int) (Snapshot, error) {
	return readWindowsSnapshot(path, limit, readFileSnapshotUnmanaged)
}
func readWindowsSnapshot(path string, limit int, read func(string, int) (Snapshot, error)) (Snapshot, error) {
	return readWindowsSnapshotContext(context.Background(), path, limit, read)
}
func readWindowsSnapshotContext(parentCtx context.Context, path string, limit int, read func(string, int) (Snapshot, error)) (Snapshot, error) {
	if !filepath.IsAbs(path) {
		return Snapshot{}, &Error{Code: ConfigInvalid, Path: path}
	}
	physical, err := canonicalParent(path)
	if err != nil {
		return Snapshot{}, err
	}
	// Reads may follow a final link; coordinate with its physical target.
	if resolved, e := filepath.EvalSymlinks(physical); e == nil {
		physical = resolved
	}
	lock := physical + ".lock"
	sharedRead := func() (Snapshot, error) {
		parent, e := openStoreParent(filepath.Dir(physical), false)
		if e != nil {
			return Snapshot{}, e
		}
		defer parent.close()
		ctx, cancel := context.WithTimeout(parentCtx, MutationTimeout)
		defer cancel()
		release, e := parent.lock(ctx, filepath.Base(lock), true, false)
		if e != nil {
			return Snapshot{}, e
		}
		defer release()
		return read(physical, limit)
	}
	if _, e := os.Lstat(lock); e == nil {
		return sharedRead()
	} else if !os.IsNotExist(e) {
		return Snapshot{}, e
	}
	snap, readErr := read(physical, limit)
	if _, e := os.Lstat(lock); e == nil {
		return sharedRead()
	} else if !os.IsNotExist(e) {
		return Snapshot{}, e
	}
	return snap, readErr
}

// Resolve remains pure; managed consumers wait out a Windows replacement gap
// before classifying missing entries or recovery artifacts.
func resolveForRead(env EnvSnapshot) (Selection, error) {
	if env.GOOS != "windows" || env.Canonicalize == nil {
		return Resolve(env)
	}
	for attempt := 0; attempt < 3; attempt++ {
		selected, err := Resolve(env)
		if selected.Path == "" {
			return selected, err
		}
		lock := selected.Path + ".lock"
		if _, e := os.Lstat(lock); os.IsNotExist(e) {
			return selected, err
		} else if e != nil {
			return selected, pathError(lock, e)
		}
		parent, e := openStoreParent(filepath.Dir(selected.Path), false)
		if e != nil {
			return selected, pathError(selected.Path, e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), MutationTimeout)
		release, e := parent.lock(ctx, filepath.Base(lock), true, false)
		if e != nil {
			cancel()
			parent.close()
			return selected, pathError(lock, e)
		}
		next, e := Resolve(env)
		release()
		cancel()
		parent.close()
		if next.Path == selected.Path {
			return next, e
		}
	}
	return Selection{}, &Error{Code: ConfigChanged}
}

func openSnapshotFile(path string) (*os.File, error) { return os.Open(path) }

// Query security metadata only; a broad read grant is diagnostic, never a
// reason to deny an otherwise readable legacy configuration. Conservative
// reporting includes inherited grants and administrators, without attempting
// to expand group memberships or reinterpret deny-ACE precedence.
func nativePermissionDiagnostics(path string) []Diagnostic {
	warning := func(code Code) []Diagnostic { return []Diagnostic{{Code: code, Path: path}} }
	sd, err := windows.GetNamedSecurityInfo(extendedWindowsPath(path), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return warning(ConfigPermissionDenied)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return warning(ConfigPermissionDenied)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return warning(ConfigPermissionDenied)
	}
	if acl == nil {
		return warning(ConfigPublicReadable)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, &ace); err != nil {
			return warning(ConfigPermissionDenied)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return warning(ConfigPublicReadable)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user.User.Sid) || sid.String() == "S-1-5-18" {
			continue
		}
		if ace.Mask&(windows.FILE_READ_DATA|windows.GENERIC_READ|windows.GENERIC_ALL) != 0 {
			return warning(ConfigPublicReadable)
		}
	}
	return nil
}

// FindFirstFile normalizes the final entry's case without following a final
// reparse point. EvalSymlinks on the final entry would hide a mutation hazard.
func normalizeExistingFinal(path string) (string, error) {
	name, e := windowsStorePath(path)
	if e != nil {
		return "", e
	}
	var data windows.Win32finddata
	h, e := windows.FindFirstFile(name, &data)
	if e != nil {
		if os.IsNotExist(e) {
			return path, nil
		}
		return "", e
	}
	windows.FindClose(h)
	path = filepath.Join(filepath.Dir(path), windows.UTF16ToString(data.FileName[:]))
	volume := filepath.VolumeName(path)
	if strings.HasPrefix(volume, `\\`) {
		path = strings.ToLower(volume) + path[len(volume):]
	}
	return path, nil
}

func readSnapshotContext(ctx context.Context, path string, limit int) (Snapshot, error) {
	if ctx.Err() != nil {
		return Snapshot{}, &Error{Code: ConfigLockTimeout, Path: path}
	}
	return readWindowsSnapshotContext(ctx, path, limit, readFileSnapshotUnmanaged)
}
func portableGuardUnavailable(err error) bool {
	return os.IsPermission(err) || errors.Is(err, windows.ERROR_WRITE_PROTECT)
}
