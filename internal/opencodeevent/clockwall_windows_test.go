//go:build windows

package opencodeevent

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Regression: an unresolved precise wall API falls back to time.Now/coarse
// time. Resolve an actual missing system export, rather than mocking Find.
func TestClockSnapshotWindowsMissingWallAPI(t *testing.T) {
	missing := windows.NewLazySystemDLL("kernel32.dll").NewProc("ANClockUnavailableExport")
	for _, proc := range []*windows.LazyProc{nil, missing} {
		a := baseCounter()
		a.Kind, a.Domain = "windows-interrupt-precise", "windows-kernel"
		p := ClockPort{&sequenceCounter{samples: []CounterSample{a, a}}, wallSample(func() time.Time { return preciseWindowsWall(proc) })}
		var out bytes.Buffer
		if code := WriteClockSnapshot([]string{"--protocol", "1"}, &out, p); code != 1 || out.String() != "{\"protocol\":1,\"error\":\"clock_unavailable\"}\n" {
			t.Fatal(code, out.String())
		}
	}
}

// Regression: treating a VOID call's stale last-error as status refuses valid
// wall samples. This uses real system APIs; native execution is a parent gate.
func TestClockSnapshotWindowsNativePreciseWall(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	setError := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetLastError")
	if setError.Find() != nil || preciseSystemTime.Find() != nil {
		t.Fatal("precise wall API unavailable")
	}
	_, _, _ = setError.Call(123)
	wall := (systemWall{}).SampleWall()
	if wall.IsZero() || wall.Unix() <= 0 || wall.Nanosecond()%100 != 0 {
		t.Fatal("invalid precise wall")
	}
	s, err := NewSystemSnapshotPort().SampleSnapshot()
	if err != nil || s.ClockDomain != "windows-kernel" || s.ClockKind != "windows-interrupt-precise" {
		logWindowsSnapshotPredicates(t)
		t.Fatal("native precise coordinate unavailable", err)
	}
}

// One additional actual A-wall-B triplet diagnoses a failed snapshot; it is
// neither a retry nor a replacement for the original production assertion.
// Only predicates and bracket width leave the test, never coordinates or IDs.
func logWindowsSnapshotPredicates(t *testing.T) {
	t.Helper()
	a, okA := (systemCounter{}).SampleCounter()
	wall := (systemWall{}).SampleWall()
	b, okB := (systemCounter{}).SampleCounter()
	lo, okLo := clockNanoseconds(a.Sec, a.Nsec)
	hi, okHi := clockNanoseconds(b.Sec, b.Nsec)
	_, okWall := clockNanoseconds(wall.Unix(), int64(wall.Nanosecond()))
	_, qualified := qualityAllowance(a.Kind)
	widthAvailable := okA && okB && okLo && okHi && hi >= lo
	var width int64
	if widthAvailable {
		width = hi - lo
	}
	t.Logf("native snapshot A_available=%t B_available=%t A_ns_valid=%t B_ns_valid=%t wall_ns_valid=%t wall_nonzero=%t wall_positive=%t wall_100ns_aligned=%t profile_qualified=%t A_uuid_valid=%t B_uuid_valid=%t A_domain_valid=%t B_domain_valid=%t boot_equal=%t domain_equal=%t kind_equal=%t counter_regressed=%t width_available=%t width_ns=%d width_within_bound=%t",
		okA, okB, okLo, okHi, okWall, !wall.IsZero(), wall.Unix() > 0, wall.Nanosecond()%100 == 0, qualified,
		validClockUUID(a.Boot), validClockUUID(b.Boot), validClockDomain(a.Kind, a.Domain), validClockDomain(b.Kind, b.Domain),
		a.Boot == b.Boot, a.Domain == b.Domain, a.Kind == b.Kind, okA && okB && okLo && okHi && hi < lo,
		widthAvailable, width, widthAvailable && width <= maxSnapshotWidthNs)
}
