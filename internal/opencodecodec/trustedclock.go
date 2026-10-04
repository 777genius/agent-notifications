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

// ValidOriginalNativeAge validates trusted metadata, never grants an image row.
func ValidOriginalNativeAge(key ImageKey, candidate Candidate, mode string) bool {
	if _, ok := LookupSourceDescriptor(key.GOOS, key.GOARCH); !ok {
		return false
	}
	// Exact candidate/clock row lookups remain mandatory. Local entries are V1
	// only; accepting an age mode never supplies their missing qualification.
	if key.Entry != "serve" && (candidate.Generation != "v1" || (key.Entry != "tui" && key.Entry != "run")) {
		return false
	}
	exceptional := key.GOOS == "windows" && key.GOARCH == "amd64" &&
		candidate.Generation == "v1" && candidate.Version == "1.18.33" &&
		key.SHA256 == "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"
	if exceptional {
		return mode == "unverified_original_date"
	}
	return mode == "" || mode == "bounded"
}

func LookupQualifiedClock(key ImageKey) (ClockRow, bool) {
	candidate, known := LookupCandidate(key)
	if !known {
		return ClockRow{}, false
	}
	for _, item := range qualifiedClockRows {
		if descriptor, ok := LookupSourceDescriptor(key.GOOS, key.GOARCH); ok && item.key == key && item.row.RawKind == descriptor.RawKind &&
			item.row.Generation == candidate.Generation && ValidOriginalNativeAge(key, candidate, item.row.OriginalNativeAge) {
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
