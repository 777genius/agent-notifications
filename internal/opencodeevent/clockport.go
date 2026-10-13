package opencodeevent

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const MaxClockResponseBytes = 1024
const maxSnapshotWidthNs int64 = 100_000_000

var ErrClockUnavailable = errors.New("clock_unavailable")

// CounterSample names the OS coordinate; neither caller-supplied quality nor
// wall time can authorize a counter kind.
type CounterSample struct {
	Boot, Domain, Kind string
	Sec, Nsec          int64
}

type CounterPort interface{ SampleCounter() (CounterSample, bool) }
type WallPort interface{ SampleWall() time.Time }
type SnapshotPort interface{ SampleSnapshot() (ClockSnapshot, error) }

// ClockPort is shared by the private helper and future admission. Wall is an
// anchor only: this port does not establish original event age or continuity.
type ClockPort struct {
	Counter CounterPort
	Wall    WallPort
}

type ClockSnapshot struct {
	Boot, ClockDomain, ClockKind                  string
	MonoLoNs, MonoHiNs, WallUnixNs, UncertaintyNs int64
}

// qualityAllowance is a fixed local read/quantization budget, not a claim of
// absolute UTC accuracy or clock rate. Native continuity/rate qualification is
// still required. Resolution checks can refuse this budget, never enlarge it.
// The Windows candidate uses the same conditional local profile; 100ns units
// do not prove accuracy. All profiles still need packaged native qualification.
func qualityAllowance(kind string) (int64, bool) {
	switch kind {
	case "linux-boottime", "darwin-monotonic-raw", "windows-interrupt-precise":
		return 1_000_000, true
	default:
		return 0, false
	}
}

func (p ClockPort) SampleSnapshot() (ClockSnapshot, error) {
	if p.Counter == nil || p.Wall == nil {
		return ClockSnapshot{}, ErrClockUnavailable
	}
	a, okA := p.Counter.SampleCounter()
	wall := p.Wall.SampleWall()
	b, okB := p.Counter.SampleCounter()
	lo, okLo := clockNanoseconds(a.Sec, a.Nsec)
	hi, okHi := clockNanoseconds(b.Sec, b.Nsec)
	w, okWall := clockNanoseconds(wall.Unix(), int64(wall.Nanosecond()))
	q, qualified := qualityAllowance(a.Kind)
	if !okA || !okB || !okLo || !okHi || !okWall || !qualified ||
		!validClockUUID(a.Boot) || !validClockDomain(a.Kind, a.Domain) ||
		a.Boot != b.Boot || a.Domain != b.Domain || a.Kind != b.Kind ||
		hi < lo || hi-lo > maxSnapshotWidthNs {
		return ClockSnapshot{}, ErrClockUnavailable
	}
	// The whole bracket plus three read allowances (A, wall, B). Admission
	// derives q independently, rather than trusting a received uncertainty.
	return ClockSnapshot{a.Boot, a.Domain, a.Kind, lo, hi, w, hi - lo + 3*q}, nil
}

func clockNanoseconds(sec, nsec int64) (int64, bool) {
	if sec < 0 || nsec < 0 || nsec >= 1_000_000_000 || sec > (math.MaxInt64-nsec)/1_000_000_000 {
		return 0, false
	}
	return sec*1_000_000_000 + nsec, true
}

func validClockUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validClockDomain(kind, domain string) bool {
	switch kind {
	case "windows-interrupt-precise":
		return domain == "windows-kernel"
	case "darwin-monotonic-raw":
		return domain == "darwin-kernel"
	case "linux-boottime":
		parts := strings.Split(domain, ":")
		if len(parts) != 3 || parts[0] != "linux-time" {
			return false
		}
		for _, s := range parts[1:] {
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil || n == 0 || strconv.FormatUint(n, 10) != s {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// JSON emits a closed protocol-1 object. It revalidates exported snapshots so
// later callers cannot accidentally serialize malformed or oversized authority.
func (s ClockSnapshot) JSON() ([]byte, error) {
	q, ok := qualityAllowance(s.ClockKind)
	if !ok || !validClockUUID(s.Boot) || !validClockDomain(s.ClockKind, s.ClockDomain) ||
		s.MonoLoNs < 0 || s.MonoHiNs < s.MonoLoNs || s.MonoHiNs-s.MonoLoNs > maxSnapshotWidthNs ||
		s.WallUnixNs < 0 || s.UncertaintyNs != s.MonoHiNs-s.MonoLoNs+3*q {
		return nil, ErrClockUnavailable
	}
	out, err := json.Marshal(struct {
		Protocol    int    `json:"protocol"`
		Boot        string `json:"boot"`
		Domain      string `json:"clockDomain"`
		Kind        string `json:"clockKind"`
		Lo          string `json:"monoLoNs"`
		Hi          string `json:"monoHiNs"`
		Wall        string `json:"wallUnixNs"`
		Uncertainty string `json:"uncertaintyNs"`
	}{1, s.Boot, s.ClockDomain, s.ClockKind, strconv.FormatInt(s.MonoLoNs, 10),
		strconv.FormatInt(s.MonoHiNs, 10), strconv.FormatInt(s.WallUnixNs, 10), strconv.FormatInt(s.UncertaintyNs, 10)})
	if err != nil || len(out)+1 > MaxClockResponseBytes {
		return nil, ErrClockUnavailable
	}
	return append(out, '\n'), nil
}
