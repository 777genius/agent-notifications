// Package opencodecodec contains pure private composition comparisons. Callers
// must separately authenticate the OS parent and retain its original lifetime.
package opencodecodec

func LookupCandidate(key ImageKey) (Candidate, bool) {
	for _, item := range candidateImages {
		if item.key == key {
			return item.candidate, true
		}
	}
	return Candidate{}, false
}

func LookupQualifiedClock(key ImageKey) (ClockRow, bool) {
	if _, ok := LookupCandidate(key); !ok {
		return ClockRow{}, false
	}
	for _, item := range qualifiedClockRows {
		if item.key == key {
			return item.row, true
		}
	}
	return ClockRow{}, false
}

// SameCoordinateCalibration compares source-before/source-after with the Go
// anchor. The source upper endpoint already contains the fixed 10ms quantum
// added at raw Linux proc sample construction; it must not be expanded again.
// The quantum is never derived from a sender's errorNS. Other
// coordinates require their own qualification before a composition row exists.
func SameCoordinateCalibration(rawKind string, sourceLo, sourceHi, nativeLo, nativeHi, bound int64) bool {
	if rawKind != "linux-boottime" || bound < 0 || sourceLo < 0 || sourceHi <= sourceLo ||
		nativeLo < 0 || nativeHi < nativeLo || sourceHi-sourceLo > bound {
		return false
	}
	// Half-open containing upper endpoint rejects even a touching disjoint pair.
	return sourceLo <= nativeHi && nativeLo < sourceHi
}
