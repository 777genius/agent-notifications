package opencodecodec

import (
	"regexp"
	"testing"
)

// These are pure closed-data comparisons, not executing-image attestations.
func TestClosedCandidateAndUnqualifiedClockRows(t *testing.T) {
	profileID := regexp.MustCompile(`^linux-amd64-proc-boottime-v1:[0-9a-f]{64}$`)
	images := []struct{ sha, version, generation string }{
		{"0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427", "1.18.33", "v1"},
		{"9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2", "1.18.34", "v1"},
		{"f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7", "2.0.21", "v2"},
	}
	for _, image := range images {
		key := ImageKey{"linux", "amd64", "serve", image.sha}
		candidate, ok := LookupCandidate(key)
		if !ok || candidate.Version != image.version || candidate.Generation != image.generation {
			t.Fatal("wrong closed image row")
		}
		// Reviewed prerequisite rows are data; live image attestation is separate.
		clock, qualified := LookupQualifiedClock(key)
		if !qualified || clock.Generation != image.generation || clock.RawKind != "linux-boottime" ||
			clock.NativeReadBoundNS != 103_000_000 || clock.ComparisonBoundNS != 430_000_000 ||
			clock.TranslationBoundNS != 224_000_000 {
			t.Fatal("wrong reviewed clock prerequisite row")
		}
		if !profileID.MatchString(clock.ProfileID) || clock.CalibrationID != clock.ProfileID+":same-coordinate" {
			t.Fatal("noncanonical clock profile or unrelated calibration")
		}
		for _, change := range []func(*ImageKey){
			func(k *ImageKey) { k.SHA256 = "unknown" }, func(k *ImageKey) { k.Entry = "tui" },
			func(k *ImageKey) { k.GOOS = "darwin" }, func(k *ImageKey) { k.GOARCH = "arm64" },
		} {
			bad := key
			change(&bad)
			if _, ok := LookupCandidate(bad); ok {
				t.Fatal("unknown tuple selected")
			}
			if _, ok := LookupQualifiedClock(bad); ok {
				t.Fatal("unknown tuple granted clock")
			}
		}
	}
}

func TestFixedContainingCalibration(t *testing.T) {
	const anchor = int64(100_000_000_000)
	cases := []struct {
		name   string
		lo, hi int64
		want   bool
	}{
		{"contains", anchor - 10_000_000, anchor + 10_000_000, true},
		{"alreadyContainingUpperEndpoint", anchor - 9_000_000, anchor - 1_000_000, false},
		{"touchingBefore", anchor - 10_000_000, anchor, false},
		{"oneNSGapBefore", anchor - 10_000_000, anchor - 1, false},
		{"zeroWidthAtNativeLo", anchor, anchor, false},
		{"zeroWidthInsideNative", anchor + 1, anchor + 1, false},
		{"oneNSOverlap", anchor - 10_000_000, anchor + 1, true},
		{"nativeUpperEndpoint", anchor + 2_000_000, anchor + 10_000_000, true},
		{"disjointBefore", anchor - 20_000_000, anchor - 10_000_000, false},
		{"disjointAfter", anchor + 2_000_001, anchor + 10_000_000, false},
		{"overwide", anchor - 112_000_000, anchor + 112_000_001, false},
		{"boundEdge", anchor - 112_000_000, anchor + 112_000_000, true},
		{"negative", -1, anchor, false},
		{"reverse", anchor + 1, anchor, false},
		{"overflow", 9223372036854775800, 9223372036854775807, false},
	}
	// No arithmetic expansion is needed at the largest valid containing endpoint.
	const max = int64(9223372036854775807)
	if !SameCoordinateCalibration("linux-boottime", max-10_000_000, max, max-2_000_000, max, 224_000_000) {
		t.Fatal("valid containing endpoint rejected by a second quantum allowance")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameCoordinateCalibration("linux-boottime", tc.lo, tc.hi, anchor, anchor+2_000_000, 224_000_000); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if SameCoordinateCalibration("windows-interrupt-precise", anchor, anchor, anchor, anchor, 224_000_000) {
		t.Fatal("unqualified coordinate inherited proc quantum")
	}
}

// Baseline rejects valid overlapping Darwin/Windows coordinates. These pure
// checks prove the selector/arithmetic only, never clock/native qualification.
func TestClosedNativeContainingCalibration(t *testing.T) {
	for _, kind := range []string{"linux-boottime", "darwin-monotonic-raw", "windows-interrupt-precise"} {
		if !SameCoordinateCalibration(kind, 100, 200, 150, 160, 100) {
			t.Fatal("known containing interval rejected", kind)
		}
		if SameCoordinateCalibration(kind, 100, 150, 150, 160, 100) ||
			SameCoordinateCalibration(kind, 100, 201, 150, 160, 100) ||
			SameCoordinateCalibration(kind, 100, 200, 160, 150, 100) {
			t.Fatal("half-open, width or reversal invariant weakened", kind)
		}
	}
	for _, kind := range []string{"", "darwin-continuous", "windows-interrupt-coarse", "unknown"} {
		if SameCoordinateCalibration(kind, 100, 200, 150, 160, 100) {
			t.Fatal("unknown coordinate accepted", kind)
		}
	}
}

func TestSourceDescriptorsDoNotGrantImages(t *testing.T) {
	for _, pair := range [][2]string{{"linux", "arm64"}, {"darwin", "arm64"}, {"darwin", "amd64"}, {"windows", "amd64"}} {
		if descriptor, ok := LookupSourceDescriptor(pair[0], pair[1]); !ok || descriptor.RawKind == "" {
			t.Fatal("implemented source missing", pair)
		}
		key := ImageKey{pair[0], pair[1], "serve", "unknown"}
		if _, ok := LookupCandidate(key); ok {
			t.Fatal("source descriptor granted an image", pair)
		}
		if _, ok := LookupQualifiedClock(key); ok {
			t.Fatal("source descriptor granted qualification", pair)
		}
	}
	for _, pair := range [][2]string{{"windows", "arm64"}, {"darwin", "386"}, {"linux", "x64"}, {"freebsd", "amd64"}} {
		if _, ok := LookupSourceDescriptor(pair[0], pair[1]); ok {
			t.Fatal("unknown platform accepted", pair)
		}
	}
}

// Closed metadata cannot turn the recognized exception into clock authority.
func TestOriginalNativeAgePolicy(t *testing.T) {
	key := ImageKey{"windows", "amd64", "serve", "52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c"}
	candidate := Candidate{"1.18.33", "v1"}
	if !ValidOriginalNativeAge(key, candidate, "unverified_original_date") {
		t.Fatal("explicit exact exception rejected")
	}
	for _, mode := range []string{"", "bounded", "unknown"} {
		if ValidOriginalNativeAge(key, candidate, mode) {
			t.Fatal("exception concealed by mode", mode)
		}
	}
	for _, change := range []func(*ImageKey, *Candidate){
		func(k *ImageKey, c *Candidate) { k.SHA256 = "unknown" },
		func(k *ImageKey, c *Candidate) { k.GOOS = "linux" },
		func(k *ImageKey, c *Candidate) { k.GOOS = "darwin" },
		func(k *ImageKey, c *Candidate) { k.GOARCH = "arm64" },
		func(k *ImageKey, c *Candidate) { k.Entry = "tui" },
		func(k *ImageKey, c *Candidate) { c.Generation = "v2"; c.Version = "2.0.21" },
		func(k *ImageKey, c *Candidate) { c.Version = "1.18.34" },
	} {
		k, c := key, candidate
		change(&k, &c)
		if ValidOriginalNativeAge(k, c, "unverified_original_date") {
			t.Fatal("exception escaped exact tuple", k, c)
		}
	}
	if _, ok := LookupQualifiedClock(key); ok {
		t.Fatal("metadata recognition granted absent clock row")
	}
	legacy := ImageKey{"linux", "amd64", "serve", "0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"}
	if !ValidOriginalNativeAge(legacy, candidate, "") || !ValidOriginalNativeAge(legacy, candidate, "bounded") || ValidOriginalNativeAge(legacy, candidate, "unknown") {
		t.Fatal("legacy default or closed enum changed")
	}
}
