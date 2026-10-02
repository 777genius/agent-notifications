// Package opencodecodec contains pure private composition comparisons. Callers
// must separately authenticate the OS parent and retain its original lifetime.
package opencodecodec

// SourceDescriptor is a closed implementation selector, not qualification.
// Actual image/observer custody and source/math evidence must precede row data.
type SourceDescriptor struct {
	SourceKind, RawKind string
}

func LookupSourceDescriptor(goos, goarch string) (SourceDescriptor, bool) {
	switch {
	case goos == "linux" && (goarch == "amd64" || goarch == "arm64"):
		return SourceDescriptor{"linux-proc-boottime", "linux-boottime"}, true
	case goos == "darwin" && (goarch == "amd64" || goarch == "arm64"):
		return SourceDescriptor{"darwin-mach-continuous", "darwin-monotonic-raw"}, true
	case goos == "windows" && goarch == "amd64":
		return SourceDescriptor{"windows-interrupt-precise", "windows-interrupt-precise"}, true
	default:
		return SourceDescriptor{}, false
	}
}

func LookupCandidate(key ImageKey) (Candidate, bool) {
	if _, ok := LookupSourceDescriptor(key.GOOS, key.GOARCH); !ok {
		return Candidate{}, false
	}
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
		if descriptor, ok := LookupSourceDescriptor(key.GOOS, key.GOARCH); ok && item.key == key && item.row.RawKind == descriptor.RawKind {
			return item.row, true
		}
	}
	return ClockRow{}, false
}

// SameCoordinateCalibration compares source-before/source-after with the Go
// anchor. Each source upper endpoint already contains its fixed quantum;
// it must not be expanded again or derived from a sender's errorNS.
// Recognizing a native coordinate grants no row/image qualification.
func SameCoordinateCalibration(rawKind string, sourceLo, sourceHi, nativeLo, nativeHi, bound int64) bool {
	switch rawKind {
	case "linux-boottime", "darwin-monotonic-raw", "windows-interrupt-precise":
	default:
		return false
	}
	if bound < 0 || sourceLo < 0 || sourceHi <= sourceLo ||
		nativeLo < 0 || nativeHi < nativeLo || sourceHi-sourceLo > bound {
		return false
	}
	// Half-open containing upper endpoint rejects even a touching disjoint pair.
	return sourceLo <= nativeHi && nativeLo < sourceHi
}
