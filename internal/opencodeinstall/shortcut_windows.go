//go:build windows

package opencodeinstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

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
	id, _ := identityFor(OpenCodeDesktop)
	return windowsShortcutPathFor(home, id)
}

func windowsShortcutPathFor(home string, id desktopIdentity) (string, error) {
	if !filepath.IsAbs(home) {
		return "", errors.New("absolute home required for Windows shortcut")
	}
	programs := filepath.Join(home, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs")
	if currentHome, err := os.UserHomeDir(); err == nil && filepath.Clean(currentHome) == filepath.Clean(home) {
		actual, err := windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_DONT_VERIFY)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(actual) {
			return "", errors.New("Windows Programs folder is not absolute")
		}
		programs = actual
	}
	return filepath.Join(programs, id.shortcut), nil
}

func stageWindowsShortcut(home, target string) (installruntime.File, error) {
	id, _ := identityFor(OpenCodeDesktop)
	return stageWindowsShortcutFor(home, target, id)
}

func stageWindowsShortcutFor(home, target string, id desktopIdentity) (installruntime.File, error) {
	path, err := windowsShortcutPathFor(home, id)
	if err != nil {
		return installruntime.File{}, err
	}
	data, err := renderWindowsShortcutFor(target, id)
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
	return StageWindowsShortcut(OpenCodeDesktop, home, target, desktop, ledger)
}

// StageWindowsShortcut reuses the existing COM renderer and owned-file CAS
// staging for a fixed observer identity. It never publishes shortcut bytes.
func StageWindowsShortcut(product DesktopProduct, home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	id, err := identityFor(product)
	if err != nil || id.consumer == "" {
		return nil, errors.New("verified portable Local key required")
	}
	return stageWindowsShortcutForIdentity(id, home, target, desktop, ledger)
}

// StageLocalWindowsShortcut takes the verified recorded portable key, never a
// second fixed registration. Legacy product wrappers keep their fixed identities.
func StageLocalWindowsShortcut(key, home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	id, err := localWindowsIdentity(key, ledger)
	if err != nil {
		return nil, err
	}
	if !desktop {
		shared, err := localWindowsShortcutShared(key, target, ledger)
		if err != nil || shared {
			return nil, err
		}
		path, err := ownedWindowsShortcutPathFor(ledger, id)
		if err != nil {
			return nil, err
		}
		if path == "" {
			return nil, nil
		}
		owned, ok := installruntime.OwnedFile(ledger, path)
		actual, err := installruntime.Fingerprint(path)
		if err != nil || !ok || !owned.Exists || owned.Link != "" || actual != owned {
			return nil, errors.New("local toast shortcut changed")
		}
		actualTarget, appID, arguments, err := inspectWindowsShortcut(path)
		if err != nil || !sameWindowsFile(actualTarget, target) || appID != id.appID || arguments != "--help" {
			return nil, errors.New("local toast shortcut identity mismatch")
		}
		// Carry this verified preimage into CAS; do not fingerprint again and
		// accidentally adopt a replacement after the ownership/COM checks.
		return &installruntime.File{Path: path, Before: actual, Remove: true}, nil
	}
	return stageWindowsShortcutForIdentity(id, home, target, desktop, ledger)
}

// Registrations, not global desktop intent, retain the shared Local identity.
// A malformed portable record makes last-binding ownership ambiguous: refuse
// removal, including when a different valid peer was already found.
func localWindowsShortcutShared(key, target string, ledger installruntime.Ledger) (bool, error) {
	selected := ledger.Consumers[key]
	var binding portable.Binding
	if json.Unmarshal([]byte(selected.Registration), &binding) != nil || len(selected.Commands) != 1 || !sameWindowsFile(selected.Commands[0], target) {
		return false, errors.New("local toast executable is not registered")
	}
	shared := false
	for peerKey, consumer := range ledger.Consumers {
		if peerKey == key {
			continue
		}
		var peer portable.Binding
		if err := json.Unmarshal([]byte(consumer.Registration), &peer); err != nil {
			if strings.HasPrefix(peerKey, "portable:") {
				return false, errors.New("ambiguous portable toast shortcut peer")
			}
			continue
		}
		if strings.HasPrefix(peerKey, "portable:") {
			wantKey, _, _, err := peer.Registration()
			if err != nil || wantKey != peerKey || !portable.ExactCommittedBinding(ledger, peer) {
				return false, errors.New("ambiguous portable toast shortcut peer")
			}
		}
		if peer.Integration != portable.CopilotVSCode {
			continue
		}
		peerID, err := localWindowsIdentity(peerKey, ledger)
		if err != nil || peerID.appID != CopilotVSCodeToastAppID || !sameWindowsPath(peer.ControlRoot, binding.ControlRoot) ||
			len(consumer.Commands) != 1 || !sameWindowsFile(consumer.Commands[0], target) {
			return false, errors.New("unverified Local toast shortcut peer")
		}
		shared = true
	}
	return shared, nil
}

func stageWindowsShortcutForIdentity(id desktopIdentity, home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	ownedPath, err := ownedWindowsShortcutPathFor(ledger, id)
	if err != nil {
		return nil, err
	}
	if desktop {
		path, err := windowsShortcutPathFor(home, id)
		if err != nil {
			return nil, err
		}
		if ownedPath != "" && !sameWindowsPath(ownedPath, path) {
			return nil, fmt.Errorf("%s toast shortcut location changed; use the original setup environment", id.label)
		}
		if ownedPath != "" {
			if ledger.Consumers[id.consumer].Registration == "" {
				return nil, fmt.Errorf("%s toast shortcut path is owned by another consumer", id.label)
			}
			observed, err := installruntime.Fingerprint(ownedPath)
			owned, ok := installruntime.OwnedFile(ledger, ownedPath)
			if err != nil || !ok || observed != owned {
				return nil, fmt.Errorf("%s toast shortcut changed", id.label)
			}
			actualTarget, appID, arguments, err := inspectWindowsShortcut(ownedPath)
			if err == nil && sameWindowsFile(actualTarget, target) && appID == id.appID && arguments == "--help" {
				return nil, nil
			}
		}
		file, err := stageWindowsShortcutFor(home, target, id)
		if err != nil {
			return nil, err
		}
		if file.Before.Exists && ownedPath == "" {
			return nil, fmt.Errorf("%s toast shortcut path is foreign", id.label)
		}
		if ownedPath != "" && ledger.Consumers[id.consumer].Registration == "" {
			return nil, fmt.Errorf("%s toast shortcut path is owned by another consumer", id.label)
		}
		return &file, nil
	}
	if ownedPath == "" {
		return nil, nil
	}
	if ledger.Consumers[id.consumer].Registration == "" {
		return nil, fmt.Errorf("%s toast shortcut path is owned by another consumer", id.label)
	}
	before, err := installruntime.Fingerprint(ownedPath)
	if err != nil {
		return nil, err
	}
	return &installruntime.File{Path: ownedPath, Before: before, Remove: true}, nil
}

func ownedWindowsShortcutPath(ledger installruntime.Ledger) (string, error) {
	id, _ := identityFor(OpenCodeDesktop)
	return ownedWindowsShortcutPathFor(ledger, id)
}

func ownedWindowsShortcutPathFor(ledger installruntime.Ledger, id desktopIdentity) (string, error) {
	var result string
	for path := range ledger.Files {
		if !strings.EqualFold(filepath.Base(path), id.shortcut) {
			continue
		}
		if result != "" {
			return "", fmt.Errorf("ambiguous owned %s toast shortcuts", id.label)
		}
		result = path
	}
	return result, nil
}

func renderWindowsShortcut(target string) ([]byte, error) {
	id, _ := identityFor(OpenCodeDesktop)
	return renderWindowsShortcutFor(target, id)
}

func renderWindowsShortcutFor(target string, id desktopIdentity) ([]byte, error) {
	if !filepath.IsAbs(target) {
		return nil, errors.New("absolute shortcut target required")
	}
	dir, err := os.MkdirTemp("", "opencode-shortcut-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, id.shortcut)
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
		appID, err := windows.UTF16PtrFromString(id.appID)
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
	return WindowsShortcutReadyFor(OpenCodeDesktop, controlRoot, executable)
}

// WindowsShortcutReadyFor checks registration, ownership and actual COM
// properties for the same trusted product selected during setup staging.
func WindowsShortcutReadyFor(product DesktopProduct, controlRoot, executable string) error {
	if _, err := identityFor(product); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return windowsShortcutReadyFor(product, controlRoot, executable, home)
}

func windowsShortcutReady(controlRoot, executable, home string) error {
	return windowsShortcutReadyFor(OpenCodeDesktop, controlRoot, executable, home)
}

func windowsShortcutReadyFor(product DesktopProduct, controlRoot, executable, home string) error {
	id, err := identityFor(product)
	if err != nil {
		return err
	}
	if id.consumer == "" {
		return errors.New("verified portable Local key required")
	}
	return windowsShortcutReadyIdentity(id, controlRoot, executable, home)
}

func WindowsLocalShortcutReady(key, controlRoot, executable string) error {
	ledger, recovery, err := installruntime.ReadOwnership(controlRoot)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("installation recovery required")
	}
	id, err := localWindowsIdentity(key, ledger)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return windowsShortcutReadyIdentity(id, controlRoot, executable, home)
}

func localWindowsIdentity(key string, ledger installruntime.Ledger) (desktopIdentity, error) {
	id, _ := identityFor(CopilotVSCodeDesktop)
	consumer, ok := ledger.Consumers[key]
	var binding portable.Binding
	if !ok || json.Unmarshal([]byte(consumer.Registration), &binding) != nil || binding.Integration != portable.CopilotVSCode ||
		binding.ComponentID != ledger.ID || binding.Owner != ledger.Owner || !portable.ExactCommittedBinding(ledger, binding) {
		return id, errors.New("verified portable Local consumer required")
	}
	want, _, _, err := binding.Registration()
	if err != nil || key != want {
		return id, errors.New("portable Local consumer key mismatch")
	}
	id.consumer = key
	return id, nil
}

func windowsShortcutReadyIdentity(id desktopIdentity, controlRoot, executable, home string) error {
	ledger, recovery, err := installruntime.ReadOwnership(controlRoot)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("installation recovery required")
	}
	if id.appID == CopilotVSCodeToastAppID {
		if _, err := localWindowsIdentity(id.consumer, ledger); err != nil {
			return err
		}
	}
	consumer, ok := ledger.Consumers[id.consumer]
	if !ok || len(consumer.Commands) == 0 || !filepath.IsAbs(executable) || !sameWindowsFile(consumer.Commands[0], executable) {
		return fmt.Errorf("%s executable is not registered", id.label)
	}
	path, err := ownedWindowsShortcutPathFor(ledger, id)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("%s toast shortcut is not owned", id.label)
	}
	activePath, err := windowsShortcutPathFor(home, id)
	if err != nil {
		return err
	}
	if !sameWindowsPath(path, activePath) {
		return fmt.Errorf("%s toast shortcut is outside the active Programs folder", id.label)
	}
	owned, ok := installruntime.OwnedFile(ledger, path)
	if !ok || !owned.Exists || owned.Link != "" {
		return fmt.Errorf("%s toast shortcut is not owned", id.label)
	}
	actual, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	if !actual.Exists || actual.Link != "" || actual.SHA256 != owned.SHA256 {
		return fmt.Errorf("%s toast shortcut changed", id.label)
	}
	target, appID, arguments, err := inspectWindowsShortcut(path)
	if err != nil {
		return err
	}
	if !sameWindowsFile(target, executable) || appID != id.appID || arguments != "--help" {
		return fmt.Errorf("%s toast shortcut identity mismatch", id.label)
	}
	return nil
}

func sameWindowsPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func sameWindowsFile(a, b string) bool {
	if sameWindowsPath(a, b) {
		return true
	}
	left, leftErr := os.Stat(a)
	right, rightErr := os.Stat(b)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
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
