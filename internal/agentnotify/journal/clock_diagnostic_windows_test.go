//go:build windows

package journal

import (
	"math"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Regression: a wrong class/layout, GUID byte order, precise export or tick
// conversion can refuse or misidentify a valid native boot coordinate. The
// oracle uses native GUID formatting and separate DLL bindings, not product
// layout/formatting/constants. ReturnLength is evidence, never a relaxed gate.
func TestWindowsNativeClockDiagnostic(t *testing.T) {
	// PHNT ntexapi.h at 025db3a0ec0fa1b303152d570df492625a021331:
	// SystemBootEnvironmentInformation (90), GUID/FIRMWARE_TYPE/BootFlags.
	// Private ABI: this observation does not qualify alternate returned lengths.
	var info struct {
		Identifier windows.GUID
		Firmware   uint32
		_          uint32
		Flags      uint64
	}
	const expectedLength = 32
	const lengthSentinel uint32 = 0xa5a5a5a5
	t.Logf("native ABI size=%d guid_offset=%d guid_size=%d firmware_offset=%d firmware_size=%d flags_offset=%d flags_size=%d expected_length=%d",
		unsafe.Sizeof(info), unsafe.Offsetof(info.Identifier), unsafe.Sizeof(info.Identifier),
		unsafe.Offsetof(info.Firmware), unsafe.Sizeof(info.Firmware), unsafe.Offsetof(info.Flags), unsafe.Sizeof(info.Flags), expectedLength)
	if unsafe.Sizeof(info) != expectedLength || unsafe.Offsetof(info.Identifier) != 0 || unsafe.Sizeof(info.Identifier) != 16 ||
		unsafe.Offsetof(info.Firmware) != 16 || unsafe.Sizeof(info.Firmware) != 4 || unsafe.Offsetof(info.Flags) != 24 || unsafe.Sizeof(info.Flags) != 8 {
		t.Fatal("diagnostic ABI mismatch")
	}
	v := windows.RtlGetVersion()
	t.Logf("native platform go=%s arch=%s windows_major=%d windows_minor=%d windows_build=%d", runtime.Version(), runtime.GOARCH, v.MajorVersion, v.MinorVersion, v.BuildNumber)
	query := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQuerySystemInformation")
	if query.Find() != nil {
		t.Fatal("native boot query export unavailable")
	}
	retLen := lengthSentinel
	status, _, _ := query.Call(90, uintptr(unsafe.Pointer(&info)), expectedLength, uintptr(unsafe.Pointer(&retLen)))
	statusOK := int32(uint32(status)) >= 0 // NT_SUCCESS; last-error is not NTSTATUS.
	nativeGUID := info.Identifier.String() // StringFromGUID2, independent of formatWindowsGUID.
	parsed, parseErr := windows.GUIDFromString(nativeGUID)
	canonical := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(nativeGUID, "{"), "}"))
	canonicalOK := len(nativeGUID) == 38 && len(canonical) == 36 && parseErr == nil && parsed == info.Identifier
	nonzero := info.Identifier != (windows.GUID{})
	bootFalse, falseOK := windowsBootIdentity(false)
	bootTrue, trueOK := windowsBootIdentity(true)
	stable := statusOK && nonzero && canonicalOK && falseOK && bootFalse == canonical
	trueMatches := stable && trueOK && bootTrue == canonical
	t.Logf("native boot ntstatus=0x%08x status_ok=%t return_length=%d expected_length=%d length_unchanged=%t length_exact=%t guid_nonzero=%t guid_canonical=%t guid_stable=%t boot_false_available=%t boot_true_available=%t boot_true_matches_native=%t",
		uint32(status), statusOK, retLen, expectedLength, retLen == lengthSentinel, retLen == expectedLength,
		nonzero, canonicalOK, stable, falseOK, trueOK, trueMatches)

	precise := windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryInterruptTimePrecise")
	exportOK := precise.Find() == nil
	var before, after uint64
	if exportOK {
		_, _, _ = precise.Call(uintptr(unsafe.Pointer(&before))) // VOID, ignore return/last-error.
	}
	boot, sec, nsec, sampleOK := WindowsPreciseBootSample()
	if exportOK {
		_, _, _ = precise.Call(uintptr(unsafe.Pointer(&after)))
	}
	counterNonzero := before != 0 && after != 0
	monotone := exportOK && counterNonzero && after >= before
	conversionOK := sampleOK && sec >= 0 && nsec >= 0 && nsec < 1_000_000_000 && nsec%100 == 0 &&
		sec <= (math.MaxInt64-nsec)/1_000_000_000 && before <= math.MaxInt64/100 && after <= math.MaxInt64/100
	withinNative := false
	if conversionOK && monotone {
		ns := sec*1_000_000_000 + nsec
		withinNative = uint64(ns) >= before*100 && uint64(ns) <= after*100
	}
	sampleBootMatches := stable && sampleOK && boot == canonical
	t.Logf("native precise export_available=%t counter_nonzero=%t counter_monotone=%t sample_available=%t sample_boot_matches_native=%t conversion_valid=%t sample_within_native_reads=%t",
		exportOK, counterNonzero, monotone, sampleOK, sampleBootMatches, conversionOK, withinNative)
	if !statusOK || retLen != expectedLength || !nonzero || !canonicalOK || !stable || !trueMatches {
		t.Error("native boot contract unavailable; see bounded predicates")
	}
	if !exportOK || !counterNonzero || !monotone || !sampleBootMatches || !conversionOK || !withinNative {
		t.Error("native precise contract unavailable; see bounded predicates")
	}
}
