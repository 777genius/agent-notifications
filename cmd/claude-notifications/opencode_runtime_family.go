package main

import (
	"strconv"
	"strings"

	"github.com/777genius/agent-notifications/internal/opencodecodec"
)

// An official OpenCode image is admitted by who the live process is and which
// release range it belongs to, not by a release hash. Each generation resolves
// onto one reviewed reference release of the same platform and entry, so every
// reader and clock lookup uses that reference row. The runtime clock anchors still
// compare this process against the independent Go clock on every event and fail
// closed, so an unreviewed runtime that disagrees is refused rather than trusted.
//
// This is a deliberate family-level assumption (owner decision), not per-release
// source evidence. Where the OS can verify the publisher (macOS code signing) that
// proof is required; Linux and Windows have no cheap equivalent, so there the
// range, the held live image and the clock anchors are the whole gate.
type familyGeneration struct {
	generation string
	reference  string // reviewed reference release; also the lower bound of the range
	major      int
	floor      [2]int // minimum minor and patch within the reference major
}

var familyGenerations = [...]familyGeneration{
	{generation: "v1", reference: "1.18.33", major: 1, floor: [2]int{18, 33}},
	{generation: "v2", reference: "2.0.21", major: 2, floor: [2]int{0, 21}},
}

// familyOf returns the generation of a plain release version in range. Pre-release
// or build suffixes and malformed numbers belong to no family.
func familyOf(version string) (familyGeneration, bool) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return familyGeneration{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 6 || (len(p) > 1 && p[0] == '0') {
			return familyGeneration{}, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return familyGeneration{}, false
			}
		}
		v, _ := strconv.Atoi(p)
		n[i] = v
	}
	for _, g := range familyGenerations {
		if n[0] == g.major && (n[1] > g.floor[0] || n[1] == g.floor[0] && n[2] >= g.floor[1]) {
			return g, true
		}
	}
	return familyGeneration{}, false
}

func exactKey(live runtimeLiveImage) opencodecodec.ImageKey {
	return opencodecodec.ImageKey{GOOS: live.GOOS, GOARCH: live.GOARCH, Entry: live.Entry, SHA256: live.SHA256}
}

// familyTrusted: an image that already has its own reviewed clock row keeps that
// exact qualification, and macOS additionally needs the OS-verified publisher.
func familyTrusted(live runtimeLiveImage) bool {
	if _, exact := opencodecodec.LookupQualifiedClock(exactKey(live)); exact {
		return false
	}
	return live.Publisher || live.GOOS != "darwin"
}

// familyReferenceKey is the reviewed reference image of a generation for this entry.
func familyReferenceKey(live runtimeLiveImage, g familyGeneration) (opencodecodec.ImageKey, bool) {
	return opencodecodec.ReferenceImage(live.GOOS, live.GOARCH, live.Entry, g.reference)
}

// qualificationKey is the image identity every reader, candidate and clock lookup
// uses for a probed release version. The bool reports a family (not exact) lookup.
func qualificationKey(live runtimeLiveImage, version string) (opencodecodec.ImageKey, bool) {
	if g, ok := familyOf(version); ok && familyTrusted(live) {
		if key, found := familyReferenceKey(live, g); found {
			return key, true
		}
	}
	return exactKey(live), false
}
