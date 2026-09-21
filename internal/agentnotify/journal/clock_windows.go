//go:build windows

package journal

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PlatformClock uses the kernel boot-environment GUID and interrupt time.
// Interrupt time includes suspend, like CLOCK_BOOTTIME, and is not wall time.
type PlatformClock struct{}

// DefaultClock is the production Windows journal clock.
func DefaultClock() Clock { return PlatformClock{} }

func (PlatformClock) Sample() Sample {
	boot, sec, _, ok := WindowsBootSample()
	if !ok {
		return Sample{}
	}
	return Sample{Boot: boot, Seconds: uint64(sec), Available: true}
}

type bootEnvironmentInfo struct {
	BootIdentifier [16]byte
	FirmwareType   uint32
	_              uint32
	BootFlags      uint64
}

func WindowsBootSample() (boot string, sec, nsec int64, ok bool) {
	var info bootEnvironmentInfo
	var retLen uint32
	if err := windows.NtQuerySystemInformation(windows.SystemBootEnvironmentInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), &retLen); err != nil {
		return "", 0, 0, false
	}
	if info.BootIdentifier == [16]byte{} {
		return "", 0, 0, false
	}
	boot = formatWindowsGUID(info.BootIdentifier)
	if !validText(boot, 256, true) {
		return "", 0, 0, false
	}
	sec, nsec, ok = windowsInterruptTime()
	if !ok {
		return "", 0, 0, false
	}
	return boot, sec, nsec, true
}

func formatWindowsGUID(g [16]byte) string {
	d1 := binary.LittleEndian.Uint32(g[0:4])
	d2 := binary.LittleEndian.Uint16(g[4:6])
	d3 := binary.LittleEndian.Uint16(g[6:8])
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x", d1, d2, d3, g[8], g[9], g[10], g[11], g[12], g[13], g[14], g[15])
}

var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	queryInterruptTime = kernel32.NewProc("QueryInterruptTime")
	getTickCount64     = kernel32.NewProc("GetTickCount64")
)

func windowsInterruptTime() (sec, nsec int64, ok bool) {
	if queryInterruptTime.Find() == nil {
		var t uint64
		_, _, _ = queryInterruptTime.Call(uintptr(unsafe.Pointer(&t)))
		if t > 0 {
			return int64(t / 10_000_000), int64(t%10_000_000) * 100, true
		}
	}
	if getTickCount64.Find() != nil {
		return 0, 0, false
	}
	ms, _, _ := getTickCount64.Call()
	if ms == 0 {
		return 0, 0, false
	}
	return int64(ms / 1000), int64(ms%1000) * 1_000_000, true
}
