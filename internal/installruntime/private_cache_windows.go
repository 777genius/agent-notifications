//go:build windows

package installruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/trace"
	"strings"

	"golang.org/x/sys/windows"
)

// CheckPrivateCacheRoot requires an existing local non-reparse directory with
// the installation lock's owner/private-DACL policy. It never repairs access.
func CheckPrivateCacheRoot(root string) error {
	var handles []windows.Handle
	var err error
	trace.WithRegion(context.Background(), "observation.windows/root", func() {
		handles, err = privateCacheRootHandles(root)
	})
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

// WritePrivateCacheDocument publishes a private same-boot observation document.
// The caller must retain its permanent cache lock through this operation.
// Buffered writes are sufficient for visibility to cooperating OS processes:
// the cache's kernel boot identity invalidates all entries after a reboot. This
// deliberately omits physical-disk Sync, unlike durable installer/journal writes.
// Preparation failures preserve the previous document; publication is one
// replacing rename relative to the held, validated private root.
func WritePrivateCacheDocument(root, name string, data []byte) (err error) {
	if len(data) == 0 {
		return fmt.Errorf("private cache write requires bytes")
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `\/:`) {
		return fmt.Errorf("invalid relative Windows component")
	}
	var handles []windows.Handle
	trace.WithRegion(context.Background(), "observation.publish/root", func() {
		handles, err = privateCacheRootHandles(root)
	})
	defer closeWindowsParents(handles)
	if err != nil {
		return err
	}
	parent := handles[len(handles)-1]
	var random [16]byte
	trace.WithRegion(context.Background(), "observation.publish/random", func() {
		_, err = rand.Read(random[:])
	})
	if err != nil {
		return err
	}
	temp := ".observations-" + hex.EncodeToString(random[:])
	var handle windows.Handle
	trace.WithRegion(context.Background(), "observation.publish/create", func() {
		handle, err = windowsCreatePrivateCacheAt(parent, temp)
	})
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(handle), temp)
	published := false
	defer func() {
		if !published {
			trace.WithRegion(context.Background(), "observation.publish/cleanup", func() {
				_ = windowsDeleteHandle(handle)
			})
		}
		var closeErr error
		trace.WithRegion(context.Background(), "observation.publish/close", func() {
			closeErr = f.Close()
		})
		if err == nil {
			err = closeErr
		}
	}()
	var written int
	trace.WithRegion(context.Background(), "observation.publish/write", func() {
		written, err = f.Write(data)
	})
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	trace.WithRegion(context.Background(), "observation.publish/target", func() {
		err = validatePrivateCacheReplacement(parent, name)
	})
	if err != nil {
		return err
	}
	trace.WithRegion(context.Background(), "observation.publish/rename", func() {
		err = windowsRenameHandle(handle, parent, name, true)
	})
	if err != nil {
		return err
	}
	published = true
	// A close error still denies the claim, even if its attempted bit has already
	// been published. The caller also checks its deadline before granting delivery.
	return nil
}

// Create with a private descriptor in the initial NtCreateFile call. Tightening
// an inherited DACL afterward cannot revoke a foreign reader's existing handle.
func windowsCreatePrivateCacheAt(parent windows.Handle, name string) (windows.Handle, error) {
	security, err := privateWindowsSecurityDescriptor(false)
	if err != nil {
		return 0, err
	}
	return windowsOpenAtWithSecurity(parent, name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL,
		windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE, security)
}

func validatePrivateCacheReplacement(parent windows.Handle, name string) (err error) {
	f, err := windowsRegularAt(parent, name, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// Leaf validation denies delete sharing, so it must close before replacement.
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	handle := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("private cache document requires a single-link inode")
	}
	if err = privateWindowsHandle(handle); err != nil {
		return err
	}
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("private cache document must be a regular file")
	}
	return nil
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
