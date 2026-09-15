//go:build windows

// ABOUTME: terminal-bell delivery on Windows — attach to the interactive Claude
// ABOUTME: session's console (its Windows Terminal ConPTY) and ring its BEL.
package notifier

import (
	"fmt"
	"unsafe"

	"github.com/777genius/agent-notifications/internal/logging"
	"golang.org/x/sys/windows"
)

var (
	modkernel32       = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole   = modkernel32.NewProc("FreeConsole")
	procAttachConsole = modkernel32.NewProc("AttachConsole")
)

// attachParentProcess is ATTACH_PARENT_PROCESS ((DWORD)-1) for AttachConsole.
const attachParentProcess = uintptr(0xFFFFFFFF)

// sendTerminalBell rings the BEL on the interactive Claude session's terminal so
// the originating Windows Terminal tab raises its bell indicator.
//
// A Claude Code hook is spawned with its own windowless console
// (CREATE_NO_WINDOW), so writing to our own CONOUT$ rings a private, invisible
// console (its only client is the hook itself). Instead we detach that console
// and attach to an ancestor's — the Claude session's console, which is the
// visible pane's ConPTY — then write the BEL there. Verified: AttachConsole on
// such a child lands on the console shared by the claude/shell processes (the
// visible tab) and the bell shows on the correct tab even when backgrounded.
//
// Best-effort: every step is debug-logged on failure and never fails the
// notification. stdout/stderr are pipes (the hook protocol) and are unaffected
// by the console juggling; the short-lived hook exits right after.
func sendTerminalBell() {
	// No locking around these process-wide console attach/detach calls: a hook
	// is a one-shot process that invokes this exactly once, so they never run
	// concurrently. Add a mutex here if a long-lived caller ever rings in parallel.

	// AttachConsole requires that we hold no console; drop our own first.
	procFreeConsole.Call()

	if ringAncestorConsole(attachParentProcess, "parent") {
		return
	}
	// Fallback: the immediate parent may have no console of its own; walk up.
	for _, pid := range ancestorPIDs() {
		if ringAncestorConsole(uintptr(pid), fmt.Sprintf("pid %d", pid)) {
			return
		}
	}
	logging.Debug("terminal bell: no ancestor console accepted a BEL")
}

// ringAncestorConsole attaches to the target process's console, writes a BEL,
// then detaches. Returns true once a BEL is delivered.
func ringAncestorConsole(target uintptr, label string) bool {
	if r, _, err := procAttachConsole.Call(target); r == 0 {
		logging.Debug("terminal bell: AttachConsole(%s) failed: %v", label, err)
		return false
	}
	err := writeBELToConsole()
	procFreeConsole.Call()
	if err != nil {
		logging.Debug("terminal bell: write to %s console failed: %v", label, err)
		return false
	}
	logging.Debug("terminal bell: delivered to %s console", label)
	return true
}

// writeBELToConsole opens the active console output buffer (CONOUT$) and writes
// a single BEL byte. CONOUT$ must be opened read+write with shared read+write
// to obtain a handle to the active screen buffer (per the Win32 console docs).
func writeBELToConsole() error {
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return fmt.Errorf("encode CONOUT$: %w", err)
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return fmt.Errorf("open CONOUT$: %w", err)
	}
	defer windows.CloseHandle(h)

	var written uint32
	if err := windows.WriteFile(h, []byte{'\a'}, &written, nil); err != nil {
		return fmt.Errorf("write BEL: %w", err)
	}
	return nil
}

// ancestorPIDs returns the process's ancestor PIDs (parent, grandparent, …) via
// a process snapshot — fallback AttachConsole targets when the immediate parent
// has no console of its own.
func ancestorPIDs() []uint32 {
	return ancestorChain(parentSnapshot(), windows.GetCurrentProcessId())
}

func ancestorChain(parents map[uint32]uint32, pid uint32) []uint32 {
	var chain []uint32
	seen := map[uint32]bool{}
	for i := 0; i < 32 && pid != 0; i++ {
		seen[pid] = true
		next := parents[pid]
		if next == 0 || seen[next] {
			break
		}
		chain = append(chain, next)
		pid = next
	}
	return chain
}

// parentSnapshot returns a child-PID -> parent-PID table for all processes.
func parentSnapshot() map[uint32]uint32 {
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
