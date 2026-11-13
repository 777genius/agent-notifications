package main

import (
	"strconv"
	"strings"

	"github.com/777genius/agent-notifications/internal/opencodecodec"
)

// The darwin/arm64 V2 family is admitted by who the live process is, not by a
// release hash. An official OpenCode serve process whose publisher the OS has
// verified is mapped onto the one reviewed reference image, so every reader and
// clock lookup resolves to that reference row. The runtime clock anchors still
// compare this process against the independent Go clock on every event and fail
// closed, so an unreviewed runtime that disagrees is refused rather than trusted.
//
// This is a deliberate family-level assumption (owner decision), not per-release
// source evidence. Other platforms and V1 keep exact image qualification.
const familyReferenceVersion = "2.0.21"

var familyReference = opencodecodec.ImageKey{
	GOOS: "darwin", GOARCH: "arm64", Entry: "serve",
	SHA256: "0b2b68c1efaf20a29aaf636c2ffccc1abb56243a82f48cce45e257d232e03442",
}

func exactKey(live runtimeLiveImage) opencodecodec.ImageKey {
	return opencodecodec.ImageKey{GOOS: live.GOOS, GOARCH: live.GOARCH, Entry: live.Entry, SHA256: live.SHA256}
}

// familyEligible is false for an image that already has its own qualified row:
// an exact qualification (e.g. a reviewed V1 release) always wins over the family.
func familyEligible(live runtimeLiveImage) bool {
	if _, exact := opencodecodec.LookupCandidate(exactKey(live)); exact {
		return false
	}
	return live.Publisher && live.GOOS == familyReference.GOOS && live.GOARCH == familyReference.GOARCH &&
		live.Entry == familyReference.Entry
}

// familyVersionOK accepts plain release versions 2.x.y at or above the reference.
// Pre-release or build suffixes and malformed numbers are refused.
func familyVersionOK(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 6 || (len(p) > 1 && p[0] == '0') {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
		v, _ := strconv.Atoi(p)
		n[i] = v
	}
	return n[0] == 2 && (n[1] > 0 || n[2] >= 21)
}

// qualificationKey is the image identity every reader, candidate and clock lookup
// uses. Exact images keep their own key; a verified family member uses the reference.
func qualificationKey(live runtimeLiveImage) opencodecodec.ImageKey {
	if familyEligible(live) {
		return familyReference
	}
	return exactKey(live)
}
