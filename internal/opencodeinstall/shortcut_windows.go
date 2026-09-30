//go:build windows

package opencodeinstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// OpenCodeToastAppID is deliberately distinct from the legacy Claude toast ID.
const OpenCodeToastAppID = "Genius.AgentNotifications.OpenCode"

const shortcutName = "OpenCode Notifications.lnk"

var (
	clsidShellLink       = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW        = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile       = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPropertyStore     = windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	appIDProperty        = propertyKey{FormatID: windows.GUID{Data1: 0x9f4c2855, Data2: 0x9f79, Data3: 0x4b39, Data4: [8]byte{0xa8, 0xd0, 0xe1, 0xd4, 0x2d, 0xe1, 0xd5, 0xf3}}, ID: 5}
	procCoCreateInstance = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	procPropVariantClear = windows.NewLazySystemDLL("ole32.dll").NewProc("PropVariantClear")
)

type propertyKey struct {
	FormatID windows.GUID
	ID       uint32
}

// PROPVARIANT has an eight-byte header and a pointer-sized string value.
type stringVariant struct {
	Type     uint16
	Reserved [3]uint16
	Value    unsafe.Pointer
	Padding  uintptr
}

func windowsShortcutPath(home string) (string, error) {
	if !filepath.IsAbs(home) {
		return "", errors.New("absolute home required for Windows shortcut")
	}
	programs := filepath.Join(home, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs")
	if currentHome, err := os.UserHomeDir(); err == nil && filepath.Clean(currentHome) == filepath.Clean(home) {
		actual, err := windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(actual) {
			return "", errors.New("Windows Programs folder is not absolute")
		}
		programs = actual
	}
	return filepath.Join(programs, shortcutName), nil
}

func stageWindowsShortcut(home, target string) (installruntime.File, error) {
	path, err := windowsShortcutPath(home)
	if err != nil {
		return installruntime.File{}, err
	}
	data, err := renderWindowsShortcut(target)
	if err != nil {
		return installruntime.File{}, err
	}
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return installruntime.File{}, err
	}
	return installruntime.File{Path: path, Before: before, Data: data, Mode: 0600}, nil
}

func stageWindowsShortcutForSetup(home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	ownedPath, err := ownedWindowsShortcutPath(ledger)
	if err != nil {
		return nil, err
	}
	if desktop {
		path, err := windowsShortcutPath(home)
		if err != nil {
			return nil, err
		}
		if ownedPath != "" && !sameWindowsPath(ownedPath, path) {
			return nil, errors.New("OpenCode toast shortcut location changed; use the original setup environment")
		}
		file, err := stageWindowsShortcut(home, target)
		if err != nil {
			return nil, err
		}
		if file.Before.Exists && ownedPath == "" {
			return nil, errors.New("OpenCode toast shortcut path is foreign")
		}
		if ownedPath != "" && ledger.Consumers[consumerID].Registration == "" {
			return nil, errors.New("OpenCode toast shortcut path is owned by another consumer")
		}
		return &file, nil
	}
	if ownedPath == "" {
		return nil, nil
	}
	if ledger.Consumers[consumerID].Registration == "" {
		return nil, errors.New("OpenCode toast shortcut path is owned by another consumer")
	}
	before, err := installruntime.Fingerprint(ownedPath)
	if err != nil {
		return nil, err
	}
	return &installruntime.File{Path: ownedPath, Before: before, Remove: true}, nil
}

func ownedWindowsShortcutPath(ledger installruntime.Ledger) (string, error) {
	var result string
	for path := range ledger.Files {
		if !strings.EqualFold(filepath.Base(path), shortcutName) {
			continue
		}
		if result != "" {
			return "", errors.New("ambiguous owned OpenCode toast shortcuts")
		}
		result = path
	}
	return result, nil
}

func renderWindowsShortcut(target string) ([]byte, error) {
	if !filepath.IsAbs(target) {
		return nil, errors.New("absolute shortcut target required")
	}
	dir, err := os.MkdirTemp("", "opencode-shortcut-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, shortcutName)
	err = withShellLink(func(link unsafe.Pointer) error {
		value, err := windows.UTF16PtrFromString(target)
		if err != nil {
			return err
		}
		if err := comResult("IShellLink.SetPath", comCall(link, 20, uintptr(unsafe.Pointer(value)))); err != nil {
			return err
		}
		arguments, err := windows.UTF16PtrFromString("--help")
		if err != nil {
			return err
		}
		if err := comResult("IShellLink.SetArguments", comCall(link, 11, uintptr(unsafe.Pointer(arguments)))); err != nil {
			return err
		}
		store, err := queryInterface(link, &iidPropertyStore)
		if err != nil {
			return err
		}
		defer releaseCOM(store)
		appID, err := windows.UTF16PtrFromString(OpenCodeToastAppID)
		if err != nil {
			return err
		}
		v := stringVariant{Type: 31, Value: unsafe.Pointer(appID)} // VT_LPWSTR
		if err := comResult("IPropertyStore.SetValue", comCall(store, 6, uintptr(unsafe.Pointer(&appIDProperty)), uintptr(unsafe.Pointer(&v)))); err != nil {
			return err
		}
		runtime.KeepAlive(appID)
		if err := comResult("IPropertyStore.Commit", comCall(store, 7)); err != nil {
			return err
		}
		persist, err := queryInterface(link, &iidPersistFile)
		if err != nil {
			return err
		}
		defer releaseCOM(persist)
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		return comResult("IPersistFile.Save", comCall(persist, 6, uintptr(unsafe.Pointer(name)), 1))
	})
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func inspectWindowsShortcut(path string) (target, appID, arguments string, err error) {
	err = withShellLink(func(link unsafe.Pointer) error {
		persist, e := queryInterface(link, &iidPersistFile)
		if e != nil {
			return e
		}
		defer releaseCOM(persist)
		name, e := windows.UTF16PtrFromString(path)
		if e != nil {
			return e
		}
		if e = comResult("IPersistFile.Load", comCall(persist, 5, uintptr(unsafe.Pointer(name)), 0)); e != nil {
			return e
		}
		var buf [32768]uint16
		if e = comResult("IShellLink.GetPath", comCall(link, 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)); e != nil {
			return e
		}
		target = windows.UTF16ToString(buf[:])
		var args [256]uint16
		if e = comResult("IShellLink.GetArguments", comCall(link, 10, uintptr(unsafe.Pointer(&args[0])), uintptr(len(args)))); e != nil {
			return e
		}
		arguments = windows.UTF16ToString(args[:])
		store, e := queryInterface(link, &iidPropertyStore)
		if e != nil {
			return e
		}
		defer releaseCOM(store)
		var v stringVariant
		if e = comResult("IPropertyStore.GetValue", comCall(store, 5, uintptr(unsafe.Pointer(&appIDProperty)), uintptr(unsafe.Pointer(&v)))); e != nil {
			return e
		}
		defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&v)))
		if v.Type != 31 || v.Value == nil {
			return errors.New("shortcut has no string AppUserModelID")
		}
		appID = windows.UTF16PtrToString((*uint16)(v.Value))
		return nil
	})
	return
}

// WindowsShortcutReady verifies both installer ownership and the properties
// Windows uses to match the Start Menu entry to an OpenCode toast.
func WindowsShortcutReady(controlRoot, executable string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return windowsShortcutReady(controlRoot, executable, home)
}

func windowsShortcutReady(controlRoot, executable, home string) error {
	ledger, recovery, err := installruntime.ReadOwnership(controlRoot)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("installation recovery required")
	}
	consumer, ok := ledger.Consumers[consumerID]
	if !ok || len(consumer.Commands) == 0 || !filepath.IsAbs(executable) || !sameWindowsPath(consumer.Commands[0], executable) {
		return errors.New("OpenCode executable is not registered")
	}
	path, err := ownedWindowsShortcutPath(ledger)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("OpenCode toast shortcut is not owned")
	}
	activePath, err := windowsShortcutPath(home)
	if err != nil {
		return err
	}
	if !sameWindowsPath(path, activePath) {
		return errors.New("OpenCode toast shortcut is outside the active Programs folder")
	}
	owned, ok := installruntime.OwnedFile(ledger, path)
	if !ok || !owned.Exists || owned.Link != "" {
		return errors.New("OpenCode toast shortcut is not owned")
	}
	actual, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	if !actual.Exists || actual.Link != "" || actual.SHA256 != owned.SHA256 {
		return errors.New("OpenCode toast shortcut changed")
	}
	target, appID, arguments, err := inspectWindowsShortcut(path)
	if err != nil {
		return err
	}
	if !sameWindowsPath(target, executable) || appID != OpenCodeToastAppID || arguments != "--help" {
		return errors.New("OpenCode toast shortcut identity mismatch")
	}
	return nil
}

func sameWindowsPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func withShellLink(fn func(unsafe.Pointer) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// x/sys exposes COM's S_FALSE (already initialized in this apartment) as
	// errno(1), although it is a success and still needs CoUninitialize.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && err != syscall.Errno(1) {
		return err
	}
	defer windows.CoUninitialize()
	var link unsafe.Pointer
	h, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, 1, uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if err := comResult("CoCreateInstance", h); err != nil {
		return err
	}
	defer releaseCOM(link)
	return fn(link)
}

func queryInterface(object unsafe.Pointer, iid *windows.GUID) (unsafe.Pointer, error) {
	var result unsafe.Pointer
	if err := comResult("QueryInterface", comCall(object, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&result)))); err != nil {
		return nil, err
	}
	return result, nil
}

func releaseCOM(object unsafe.Pointer) {
	if object != nil {
		comCall(object, 2)
	}
}

//go:uintptrescapes
func comCall(object unsafe.Pointer, method int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(object)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(method)*unsafe.Sizeof(uintptr(0))))
	call := make([]uintptr, 0, len(args)+1)
	call = append(call, uintptr(object))
	call = append(call, args...)
	r, _, _ := syscall.SyscallN(fn, call...)
	return r
}

func comResult(operation string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s failed: HRESULT 0x%08x", operation, uint32(hr))
	}
	return nil
}
