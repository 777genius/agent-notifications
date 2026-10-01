package opencodeevent

import "context"

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

// SelectTrustedClock has no qualified product cells yet. Parent qualification
// must supply complete source/rate/conversion evidence before adding a cell.
func SelectTrustedClock(goos, goarch string) (ClockSelection, error) {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64":
		// Closed ledger cells deliberately have no immutable complete T/source grant.
		return ClockSelection{}, ErrClockUnavailable
	default:
		return ClockSelection{}, ErrClockUnavailable
	}
}

// TrustedClock is a thin E0 adapter. Snapshot takes no sender arguments.
type TrustedClock struct {
	Source    SnapshotPort
	Selection ClockSelection
}

func (c TrustedClock) Snapshot(ctx context.Context) (ClockSample, error) {
	if ctx.Err() != nil || c.Source == nil || !c.Selection.valid() {
		return ClockSample{}, ErrClockUnavailable
	}
	s, err := c.Source.SampleSnapshot()
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
