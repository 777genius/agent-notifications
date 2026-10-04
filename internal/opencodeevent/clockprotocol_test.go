package opencodeevent

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// Regression: permissive parsing accepts version skew, extra/duplicate flags,
// or embeds private arguments in a diagnostic instead of the fixed protocol.
func TestClockSnapshotExactArguments(t *testing.T) {
	a := baseCounter()
	c := &sequenceCounter{samples: []CounterSample{a, a}}
	port := ClockPort{c, wallSample(func() time.Time { return time.Unix(3, 0) })}
	for _, args := range [][]string{nil, {"--protocol"}, {"--protocol", "2"}, {"--protocol=1"},
		{"--protocol", "1", "private/path"}, {"--protocol", "1", "--protocol", "1"}, {"1", "--protocol"}} {
		var out bytes.Buffer
		if code := WriteClockSnapshot(args, &out, port); code != 1 || out.String() != "{\"protocol\":1,\"error\":\"clock_unavailable\"}\n" {
			t.Fatal(args, code, out.String())
		}
	}
	if c.calls != 0 {
		t.Fatal("invalid command read the OS clock")
	}
}

// Regression: opening only one eligibility allowlist leaves precise Windows
// unusable; opening it broadly grants unknown/coarse kinds or foreign domains.
func TestClockSnapshotWindowsProfileProtocol(t *testing.T) {
	for _, tc := range []struct{ kind, domain string }{
		{"windows-interrupt-precise", "windows-kernel"},
		{"windows-interrupt-precise", "darwin-kernel"},
		{"windows-interrupt-precise", "windows-kernel:1"},
		{"windows-interrupt-coarse", "windows-kernel"},
		{"unknown", "windows-kernel"},
	} {
		t.Run(tc.kind+"/"+tc.domain, func(t *testing.T) {
			a := baseCounter()
			a.Kind, a.Domain = tc.kind, tc.domain
			p := ClockPort{&sequenceCounter{samples: []CounterSample{a, a}}, wallSample(func() time.Time { return time.Unix(3, 7) })}
			var out bytes.Buffer
			code := WriteClockSnapshot([]string{"--protocol", "1"}, &out, p)
			if tc.kind == "windows-interrupt-precise" && tc.domain == "windows-kernel" {
				var frame map[string]json.RawMessage
				if err := json.Unmarshal(out.Bytes(), &frame); err != nil || code != 0 || len(frame) != 8 ||
					string(frame["clockKind"]) != `"windows-interrupt-precise"` || string(frame["clockDomain"]) != `"windows-kernel"` ||
					string(frame["uncertaintyNs"]) != `"3000000"` || out.Len() > 1024 {
					t.Fatal(code, out.String(), err)
				}
			} else if code != 1 || out.String() != "{\"protocol\":1,\"error\":\"clock_unavailable\"}\n" {
				t.Fatal(code, out.String())
			}
		})
	}
}

// Regression: leaking a sample error or accepting unavailable tick zero changes
// the stable failure classification on a syntactically valid command.
func TestClockSnapshotUnavailableProtocol(t *testing.T) {
	var out bytes.Buffer
	if code := WriteClockSnapshot([]string{"--protocol", "1"}, &out, ClockPort{}); code != 1 || out.String() != "{\"protocol\":1,\"error\":\"clock_unavailable\"}\n" {
		t.Fatal(code, out.String())
	}
}
