package opencodeevent

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/strictjson"
)

type sequenceCounter struct {
	samples     []CounterSample
	calls       int
	unavailable bool
}

func (p *sequenceCounter) SampleCounter() (CounterSample, bool) {
	s := p.samples[p.calls]
	p.calls++
	return s, !p.unavailable
}

type wallSample func() time.Time

func (f wallSample) SampleWall() time.Time { return f() }
func baseCounter() CounterSample {
	return CounterSample{"12345678-1234-1234-1234-123456789abc", "linux-time:4:4026531834", "linux-boottime", 2, 0}
}

// Regression: sampling wall before A/after B or serializing numbers as JSON
// Number loses the bracket and integer authority at the public command boundary.
func TestClockSnapshotBracketAndClosedJSON(t *testing.T) {
	a, b := baseCounter(), baseCounter()
	b.Nsec = 100_000_000
	c := &sequenceCounter{samples: []CounterSample{a, b}}
	p := ClockPort{c, wallSample(func() time.Time {
		if c.calls != 1 {
			t.Fatal("wall not between counter samples")
		}
		return time.Unix(3, 7)
	})}
	s, err := p.SampleSnapshot()
	if err != nil || s.MonoLoNs != 2_000_000_000 || s.MonoHiNs != 2_100_000_000 ||
		s.WallUnixNs != 3_000_000_007 || s.UncertaintyNs != 103_000_000 {
		t.Fatal(s, err)
	}
	c.calls = 0
	var out bytes.Buffer
	if code := WriteClockSnapshot([]string{"--protocol", "1"}, &out, p); code != 0 {
		t.Fatal(code, out.String())
	}
	raw := out.Bytes()
	if err := strictjson.Validate(raw, strictjson.Budget{Bytes: 1024, Depth: 2, Entries: 8}); err != nil {
		t.Fatal("duplicate or malformed response", err)
	}
	if err != nil || len(raw) > 1024 {
		t.Fatal(string(raw), err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || len(fields) != 8 || string(fields["protocol"]) != "1" {
		t.Fatal(string(raw), err)
	}
	for _, k := range []string{"boot", "clockDomain", "clockKind", "monoLoNs", "monoHiNs", "wallUnixNs", "uncertaintyNs"} {
		var value string
		if json.Unmarshal(fields[k], &value) != nil || value == "" {
			t.Fatal(k, string(raw))
		}
		if k == "monoLoNs" || k == "monoHiNs" || k == "wallUnixNs" || k == "uncertaintyNs" {
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				t.Fatal(k, err)
			}
		}
	}
	s.UncertaintyNs = 0
	if _, err = s.JSON(); err != ErrClockUnavailable {
		t.Fatal("caller quality accepted")
	}
	// Exported snapshots must not bypass revalidation at serialization.
	s.UncertaintyNs = 103_000_000
	s.ClockDomain = string(make([]byte, 1025))
	if _, err = s.JSON(); err != ErrClockUnavailable {
		t.Fatal("oversized domain accepted")
	}
}

// Regression: treating identities as generic text, wrapping nanoseconds, or
// accepting a changed/slow clock can emit apparently fresh shared authority.
func TestClockSnapshotRefusesInvalidAuthority(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CounterSample, *CounterSample)
		wall   time.Time
	}{
		{"zero UUID", func(a, b *CounterSample) { a.Boot = "00000000-0000-0000-0000-000000000000"; b.Boot = a.Boot }, time.Unix(3, 0)},
		{"malformed UUID", func(a, b *CounterSample) { a.Boot = "private/path"; b.Boot = a.Boot }, time.Unix(3, 0)},
		{"boot change", func(a, b *CounterSample) { b.Boot = "22345678-1234-1234-1234-123456789abc" }, time.Unix(3, 0)},
		{"domain change", func(a, b *CounterSample) { b.Domain = "linux-time:4:42" }, time.Unix(3, 0)},
		{"missing domain", func(a, b *CounterSample) { a.Domain = "linux-time:4:0"; b.Domain = a.Domain }, time.Unix(3, 0)},
		{"kind change", func(a, b *CounterSample) { b.Kind = "darwin-monotonic-raw" }, time.Unix(3, 0)},
		{"coarse", func(a, b *CounterSample) { a.Kind = "windows-interrupt-coarse"; b.Kind = a.Kind }, time.Unix(3, 0)},
		{"wrong Windows domain", func(a, b *CounterSample) { a.Kind = "windows-interrupt-precise"; b.Kind = a.Kind }, time.Unix(3, 0)},
		{"regression", func(a, b *CounterSample) { b.Nsec = -1 }, time.Unix(3, 0)},
		{"counter regression", func(a, b *CounterSample) { b.Sec = 1 }, time.Unix(3, 0)},
		{"width over bound", func(a, b *CounterSample) { b.Nsec = 100_000_001 }, time.Unix(3, 0)},
		{"invalid nsec", func(a, b *CounterSample) { a.Nsec = 1_000_000_000 }, time.Unix(3, 0)},
		{"overflow", func(a, b *CounterSample) { a.Sec = math.MaxInt64 / 1_000_000_000; a.Nsec = 854_775_808 }, time.Unix(3, 0)},
		{"negative sec", func(a, b *CounterSample) { a.Sec = -1 }, time.Unix(3, 0)},
		{"wall overflow", func(a, b *CounterSample) {}, time.Unix(math.MaxInt64/1_000_000_000, 854_775_808)},
		{"negative wall", func(a, b *CounterSample) {}, time.Unix(-1, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := baseCounter(), baseCounter()
			tc.mutate(&a, &b)
			p := ClockPort{&sequenceCounter{samples: []CounterSample{a, b}}, wallSample(func() time.Time { return tc.wall })}
			if s, err := p.SampleSnapshot(); err != ErrClockUnavailable || s != (ClockSnapshot{}) {
				t.Fatal(s, err)
			}
		})
	}
	// The last representable nanosecond must survive, not be refused by an
	// off-by-one overflow check (the very next nanosecond above is refused).
	a := baseCounter()
	a.Sec = math.MaxInt64 / 1_000_000_000
	a.Nsec = 854_775_807
	p := ClockPort{&sequenceCounter{samples: []CounterSample{a, a}}, wallSample(func() time.Time { return time.Unix(a.Sec, a.Nsec) })}
	if s, err := p.SampleSnapshot(); err != nil || s.MonoHiNs != math.MaxInt64 || s.WallUnixNs != math.MaxInt64 {
		t.Fatal(s, err)
	}
}

// Regression: an absent OS port or failed native read is treated as tick zero.
func TestClockSnapshotUnavailable(t *testing.T) {
	a := baseCounter()
	for _, p := range []ClockPort{{}, {Counter: &sequenceCounter{}},
		{Counter: &sequenceCounter{samples: []CounterSample{a, a}, unavailable: true}, Wall: wallSample(func() time.Time { return time.Unix(3, 0) })}} {
		if s, err := p.SampleSnapshot(); err != ErrClockUnavailable || s != (ClockSnapshot{}) {
			t.Fatal(s, err)
		}
	}
}
