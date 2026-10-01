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
		t.Fatal("native precise coordinate unavailable", err)
	}
}
