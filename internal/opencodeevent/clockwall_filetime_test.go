package opencodeevent

import (
	"math"
	"testing"
	"time"
)

// Regression: signed FILETIME, float conversion, epoch/unit mistakes or an
// unchecked multiply yields a plausible but wrong wall anchor or wraps it.
func TestClockSnapshotFILETIMEConversion(t *testing.T) {
	for _, tc := range []struct {
		name string
		file uint64
		ns   int64
		ok   bool
	}{
		{"unwritten", 0, 0, false},
		{"before Unix epoch", 116444735999999999, 0, false},
		{"Unix epoch", 116444736000000000, 0, true},
		{"one tick", 116444736000000001, 100, true},
		{"2023-11-14T22:13:20.1234567Z", 133444736001234567, 1700000000123456700, true},
		{"last int64 100ns tick", 208678456368547758, 9223372036854775800, true},
		{"next tick overflows", 208678456368547759, 0, false},
		{"unsigned maximum", math.MaxUint64, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wall := filetimeWall(uint32(tc.file), uint32(tc.file>>32))
			if tc.ok {
				if wall.IsZero() || wall.UnixNano() != tc.ns {
					t.Fatal(wall, tc.ns)
				}
			} else {
				a := baseCounter()
				p := ClockPort{&sequenceCounter{samples: []CounterSample{a, a}}, wallSample(func() time.Time { return wall })}
				if s, err := p.SampleSnapshot(); !wall.IsZero() || err != ErrClockUnavailable || s != (ClockSnapshot{}) {
					t.Fatal(wall, s, err)
				}
			}
		})
	}
}
