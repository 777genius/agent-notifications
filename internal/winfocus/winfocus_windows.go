//go:build windows

package winfocus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/777genius/agent-notifications/internal/warpfocus"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindow                = user32.NewProc("GetWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsIconic                 = user32.NewProc("IsIconic")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procIsWindow                 = user32.NewProc("IsWindow")
	procSwitchToThisWindow       = user32.NewProc("SwitchToThisWindow")

	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	procFreeConsole        = kernel32.NewProc("FreeConsole")
)

const (
	gwOwner   = 4 // GW_OWNER
	swRestore = 9 // SW_RESTORE
)

// winInfo is a snapshot of one visible, owner-less top-level window.
type winInfo struct {
	hwnd  uintptr
	pid   uint32
	title string
}

// parentMap returns a child-PID -> parent-PID table for all running processes.
func parentMap() map[uint32]uint32 {
	out := map[uint32]uint32{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return out
	}
	for {
		out[pe.ProcessID] = pe.ParentProcessID
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return out
}

// enumTopLevelWindows snapshots every visible, owner-less top-level window with
// its owning PID and title. EnumWindows only walks top-level windows; the
// owner-less filter drops tool windows and dialogs, keeping main app windows.
func enumTopLevelWindows() []winInfo {
	var list []winInfo
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1 // keep enumerating
		}
		if owner, _, _ := procGetWindow.Call(hwnd, gwOwner); owner != 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		list = append(list, winInfo{hwnd: hwnd, pid: pid, title: windowText(hwnd)})
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return list
}

func windowText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

// windowForPID returns a window owned by pid, resolving ambiguity when pid
// owns several top-level windows (e.g. Windows Terminal's shared "monarch"
// process hosting one window per project). Preference order:
//  1. a window whose title contains folder — the only signal that can tell
//     two same-PID windows for different projects apart.
//  2. hint, if it is itself one of pid's windows (typically the foreground
//     window at capture time).
//  3. any window with a non-empty title.
//  4. any window at all.
func windowForPID(list []winInfo, pid uint32, folder string, hint uintptr) uintptr {
	if pid == 0 {
		return 0
	}
	lower := strings.ToLower(strings.TrimSpace(folder))
	var hintOwned, firstTitled, firstAny uintptr
	for _, w := range list {
		if w.pid != pid {
			continue
		}
		if lower != "" && w.title != "" && strings.Contains(strings.ToLower(w.title), lower) {
			return w.hwnd
		}
		if w.hwnd == hint && hintOwned == 0 {
			hintOwned = w.hwnd
		}
		if w.title != "" && firstTitled == 0 {
			firstTitled = w.hwnd
		}
		if firstAny == 0 {
			firstAny = w.hwnd
		}
	}
	if hintOwned != 0 {
		return hintOwned
	}
	if firstTitled != 0 {
		return firstTitled
	}
	return firstAny
}

func windowByTitleContains(list []winInfo, needle string) uintptr {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return 0
	}
	lower := strings.ToLower(needle)
	for _, w := range list {
		if w.title != "" && strings.Contains(strings.ToLower(w.title), lower) {
			return w.hwnd
		}
	}
	return 0
}

func isWindow(hwnd uintptr) bool {
	r, _, _ := procIsWindow.Call(hwnd)
	return r != 0
}

func pidForWindow(hwnd uintptr) uint32 {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// CaptureFocusContext walks up the process tree from the current (hook) process
// to the nearest ancestor that owns a visible top-level window — the terminal
// window hosting Claude (e.g. WindowsTerminal.exe, Code.exe, conhost). It
// records the handle, owning PID, title and project folder for later focus.
func CaptureFocusContext(cwd string) (FocusContext, bool) {
	folder := ""
	if cwd != "" {
		folder = filepath.Base(cwd)
	}

	list := enumTopLevelWindows()
	parents := parentMap()

	// A single process can own several top-level windows — notably the Windows
	// Terminal "monarch" hosting one window per project. The foreground window
	// is passed in as a hint (it's often the window the user was looking at),
	// but windowForPID prefers an explicit folder-title match first: trusting
	// "foreground window's PID matches this ancestor" alone is wrong whenever
	// that shared PID owns windows for other projects too.
	fg, _, _ := procGetForegroundWindow.Call()

	ctx := FocusContext{
		Folder:  folder,
		WarpURL: warpfocus.FromEnv(),
	}

	pid := windows.GetCurrentProcessId()
	seen := map[uint32]bool{}
	for i := 0; i < 32 && pid != 0 && !seen[pid]; i++ {
		seen[pid] = true

		if hwnd := windowForPID(list, pid, folder, fg); hwnd != 0 {
			ctx.HWND = int64(hwnd)
			ctx.PID = pid
			ctx.Title = windowText(hwnd)
			return ctx, true
		}
		pid = parents[pid]
	}

	// No ancestor owns a window (fully detached hook). Still hand back a
	// folder-only context so the click handler can attempt a title match.
	// A Warp session URL is enough on its own to focus the originating pane.
	if ctx.HasTarget() {
		return ctx, true
	}
	return FocusContext{}, false
}

// resolveWindow turns a (possibly stale) FocusContext back into a live HWND.
// Order: validated stored handle, then owning PID, then title/folder match.
func resolveWindow(ctx FocusContext) uintptr {
	list := enumTopLevelWindows()

	if ctx.HWND != 0 {
		hwnd := uintptr(ctx.HWND)
		if isWindow(hwnd) && (ctx.PID == 0 || pidForWindow(hwnd) == ctx.PID) {
			return hwnd
		}
	}
	if hwnd := windowForPID(list, ctx.PID, ctx.Folder, 0); hwnd != 0 {
		return hwnd
	}
	for _, needle := range []string{ctx.Folder, ctx.Title} {
		if hwnd := windowByTitleContains(list, needle); hwnd != 0 {
			return hwnd
		}
	}
	return 0
}

// HideConsole detaches the calling process from its console. A process
// launched with no inherited console (e.g. by clicking a toast's
// protocol-activation link) gets one auto-created and shown by Windows for
// the console-subsystem binary; calling this immediately on entry tears it
// down again before the flash is visible for long. Safe to call even when a
// real console is inherited (e.g. manual invocation from a terminal for
// debugging) — FreeConsole only detaches this process, it doesn't close the
// console window while other processes (the shell) remain attached to it.
func HideConsole() {
	procFreeConsole.Call()
}

// Focus raises the terminal window described by ctx to the foreground.
func Focus(ctx FocusContext) error {
	if url := warpfocus.Normalize(ctx.WarpURL); url != "" {
		if err := warpfocus.Open(url); err == nil {
			return nil
		}
	}
	hwnd := resolveWindow(ctx)
	if hwnd == 0 {
		return fmt.Errorf("winfocus: no matching window for %+v", ctx)
	}
	forceForeground(hwnd)
	return nil
}

// forceForeground raises hwnd, defeating Windows' foreground-stealing lock by
// briefly attaching our input queue to the current foreground thread (the
// well-known AttachThreadInput recipe).
func forceForeground(hwnd uintptr) {
	// Only un-minimize when actually minimized; SW_RESTORE keeps a prior
	// maximized state. Don't call ShowWindow on an already-visible window — we
	// only resolve visible windows, so SW_SHOW is redundant and any show/normalize
	// call can knock a maximized or full-screen terminal out of its layout.
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}

	fg, _, _ := procGetForegroundWindow.Call()
	curThread, _, _ := procGetCurrentThreadId.Call()
	var fgThread uintptr
	if fg != 0 {
		fgThread, _, _ = procGetWindowThreadProcessId.Call(fg, 0) // 0 == NULL lpdwProcessId
	}

	attached := false
	if fgThread != 0 && fgThread != curThread {
		if r, _, _ := procAttachThreadInput.Call(curThread, fgThread, 1); r != 0 {
			attached = true
		}
	}
	procBringWindowToTop.Call(hwnd)
	// SwitchToThisWindow raises like Alt+Tab — it helps defeat the foreground
	// lock and, unlike a show/normalize call, doesn't disturb the window's
	// maximized/full-screen state. Undocumented but stable in user32 for decades.
	procSwitchToThisWindow.Call(hwnd, 1)
	procSetForegroundWindow.Call(hwnd)
	if attached {
		procAttachThreadInput.Call(curThread, fgThread, 0)
	}
}

// EnsureRegistered registers (or refreshes) the click-to-focus URI handler under
// HKCU pointing at the focus-handler executable. Idempotent: a no-op when the
// stored command already matches.
func EnsureRegistered() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	target := focusHandlerExecutable(exe)
	want := commandValue(target)

	cmdPath := `Software\Classes\` + ProtocolScheme + `\shell\open\command`
	if k, err := registry.OpenKey(registry.CURRENT_USER, cmdPath, registry.QUERY_VALUE); err == nil {
		cur, _, _ := k.GetStringValue("")
		k.Close()
		if cur == want {
			return nil
		}
	}
	return RegisterProtocolHandler(target)
}

// focusHandlerExecutable returns the GUI-subsystem sibling of the running
// console exe (same name plus a "-focus" suffix before the extension) that
// should be registered as the click-to-focus target instead of exe itself.
// Windows auto-creates and shows a console window for the instant a
// console-subsystem process starts with no inherited console — exactly what
// happens on a toast click — and that first paint can't be suppressed from
// inside the process once it's running. A GUI-subsystem binary never gets one
// allocated in the first place. Falls back to exe when the sibling hasn't
// been installed alongside it (older install, or a dev build without one).
func focusHandlerExecutable(exe string) string {
	ext := filepath.Ext(exe)
	sibling := strings.TrimSuffix(exe, ext) + "-focus" + ext
	if _, err := os.Stat(sibling); err == nil {
		return sibling
	}
	return exe
}

func commandValue(exe string) string {
	return fmt.Sprintf(`"%s" focus-windows "%%1"`, exe)
}

// RegisterProtocolHandler writes the HKCU\Software\Classes\<scheme> keys that
// let Windows launch exe with the click-to-focus URI. Per-user; no admin rights.
func RegisterProtocolHandler(exe string) error {
	base := `Software\Classes\` + ProtocolScheme
	root, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.SetStringValue("", "URL:Agent Notifications Focus"); err != nil {
		return err
	}
	if err := root.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	cmd, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmd.Close()
	return cmd.SetStringValue("", commandValue(exe))
}
