package opencodeevent

import (
	"context"
	"github.com/777genius/agent-notifications/internal/opencodecodec"
)

// ClockSelection is supplied only by trusted packaged composition. It binds the
// native clock and original source conversion, never a sender or config grant.
type ClockSelection struct {
	Policy                    TimePolicy
	CalibrationID, Generation string
	TranslationBoundNS        int64
}

func (s ClockSelection) valid() bool {
	return s.Policy.valid() && boundedToken(s.CalibrationID) &&
		(s.Generation == "v1" || s.Generation == "v2") && s.TranslationBoundNS >= 0 &&
		s.TranslationBoundNS <= s.Policy.ComparisonBoundNS-2*s.Policy.NativeReadBoundNS
}

// SelectTrustedClock preserves the old platform-only diagnostic boundary. A
// platform is insufficient authority; event composition uses the exact image.
func SelectTrustedClock(goos, goarch string) (ClockSelection, error) {
	return ClockSelection{}, ErrClockUnavailable
}

func SelectTrustedImageClock(key opencodecodec.ImageKey) (ClockSelection, error) {
	row, ok := opencodecodec.LookupQualifiedClock(key)
	if !ok {
		return ClockSelection{}, ErrClockUnavailable
	}
	selected := ClockSelection{Policy: TimePolicy{ProfileID: row.ProfileID, RawKind: row.RawKind,
		NativeReadBoundNS: row.NativeReadBoundNS, ComparisonBoundNS: row.ComparisonBoundNS},
		CalibrationID: row.CalibrationID, Generation: row.Generation, TranslationBoundNS: row.TranslationBoundNS}
	candidate, known := opencodecodec.LookupCandidate(key)
	if !known || selected.Generation != candidate.Generation || !selected.valid() {
		return ClockSelection{}, ErrClockUnavailable
	}
	return selected, nil
}

// TrustedClock is a thin E0 adapter. Snapshot takes no sender arguments.
type TrustedClock struct {
	Source    SnapshotPort
	Selection ClockSelection
}

// A held-runtime wrapper receives the caller's tightened admission context,
// while the unchanged E0 port remains argument-free and grants no authority.
type contextualSnapshotPort interface {
	SampleSnapshotContext(context.Context) (ClockSnapshot, error)
}

func (c TrustedClock) Snapshot(ctx context.Context) (ClockSample, error) {
	if ctx.Err() != nil || c.Source == nil || !c.Selection.valid() {
		return ClockSample{}, ErrClockUnavailable
	}
	var s ClockSnapshot
	var err error
	if port, ok := c.Source.(contextualSnapshotPort); ok {
		s, err = port.SampleSnapshotContext(ctx)
	} else {
		s, err = c.Source.SampleSnapshot()
	}
	if err != nil || ctx.Err() != nil {
		return ClockSample{}, ErrClockUnavailable
	}
	return c.Selection.mapSnapshot(s)
}

func (c ClockSelection) mapSnapshot(s ClockSnapshot) (ClockSample, error) {
	// JSON revalidates the exported E0 interval, closed coordinate and native rule.
	if _, err := s.JSON(); err != nil || !c.valid() || s.WallUnixNs <= 0 ||
		s.ClockKind != c.Policy.RawKind || s.UncertaintyNs > c.Policy.NativeReadBoundNS {
		return ClockSample{}, ErrClockUnavailable
	}
	return ClockSample{BootID: s.Boot, Domain: s.ClockDomain, Kind: "continuous",
		Fence: c.Policy.Fence(s.Boot, s.ClockDomain), TickNS: s.MonoLoNs,
		WallNS: s.WallUnixNs, UncertaintyNS: c.Policy.ComparisonBoundNS}, nil
}
