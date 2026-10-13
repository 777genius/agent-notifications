//go:build windows

package opencodeevent

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var preciseSystemTime = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimePreciseAsFileTime")

type systemWall struct{}

func (systemWall) SampleWall() time.Time { return preciseWindowsWall(preciseSystemTime) }

func preciseWindowsWall(proc *windows.LazyProc) time.Time {
	if proc == nil || proc.Find() != nil {
		return time.Time{}
	}
	var ft windows.Filetime
	// VOID API: its return register and stale syscall last-error are not status.
	// Zero initialization plus checked conversion refuses an unwritten result.
	_, _, _ = proc.Call(uintptr(unsafe.Pointer(&ft)))
	return filetimeWall(ft.LowDateTime, ft.HighDateTime)
}
