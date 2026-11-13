package main

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/opencodecodec"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
)

func familyLive(sha string, publisher bool) runtimeLiveImage {
	return runtimeLiveImage{GOOS: "darwin", GOARCH: "arm64", Entry: "serve", SHA256: sha, NativePID: 4242,
		Publisher: publisher, fingerprint: [32]byte{1}}
}

// The reviewed reference row must stay a V2 candidate, or every family lookup is meaningless.
func TestFamilyReferenceIsReviewedV2Candidate(t *testing.T) {
	candidate, ok := opencodecodec.LookupCandidate(familyReference)
	if !ok || candidate.Version != familyReferenceVersion || candidate.Generation != "v2" {
		t.Fatalf("family reference is not the reviewed V2 candidate: %+v ok=%v", candidate, ok)
	}
}

func TestFamilyAdmitsOnlyVerifiedPublisherInRange(t *testing.T) {
	const unknown = "1111111111111111111111111111111111111111111111111111111111111111"
	cases := []struct {
		name    string
		live    runtimeLiveImage
		version string
		want    runtimeObserverGeneration
	}{
		{"new official release", familyLive(unknown, true), "2.2.7", observerV2},
		{"reference release", familyLive(familyReference.SHA256, true), "2.0.21", observerV2},
		{"unsigned unknown image", familyLive(unknown, false), "2.2.7", observerUnverified},
		{"newer major", familyLive(unknown, true), "3.0.0", observerUnverified},
		{"below reference", familyLive(unknown, true), "2.0.20", observerUnverified},
		{"v1 release", familyLive(unknown, true), "1.19.4", observerUnverified},
		{"prerelease suffix", familyLive(unknown, true), "2.2.7-beta.1", observerUnverified},
		{"malformed", familyLive(unknown, true), "2.2", observerUnverified},
		{"signed number", familyLive(unknown, true), "+2.0.21", observerUnverified},
		{"negative part", familyLive(unknown, true), "2.-0.21", observerUnverified},
		{"leading zero", familyLive(unknown, true), "2.02.7", observerUnverified},
		{"other entry", func() runtimeLiveImage { l := familyLive(unknown, true); l.Entry = "run"; return l }(), "2.2.7", observerUnverified},
		{"other arch", func() runtimeLiveImage { l := familyLive(unknown, true); l.GOARCH = "amd64"; return l }(), "2.2.7", observerUnverified},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := qualifyRuntimeObserver(opencodehost.Profile{Version: c.version}, c.live); got != c.want {
				t.Fatalf("qualifyRuntimeObserver(%s) = %v, want %v", c.version, got, c.want)
			}
		})
	}
}

// A reviewed exact image keeps its own row even when the OS verified its publisher.
func TestFamilyKeepsExactQualifiedV1(t *testing.T) {
	live := familyLive("139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524", true)
	if familyEligible(live) {
		t.Fatal("reviewed V1 image was remapped to the family reference")
	}
	if got := qualifyRuntimeObserver(opencodehost.Profile{Version: "1.18.33"}, live); got == observerV2 {
		t.Fatalf("V1 image qualified as V2: %v", got)
	}
}

// A signed image never widens an exact qualified platform: Linux still needs its own row.
func TestFamilyDoesNotLeakToOtherPlatforms(t *testing.T) {
	live := familyLive("2222222222222222222222222222222222222222222222222222222222222222", true)
	live.GOOS = "linux"
	if key := qualificationKey(live); key == familyReference {
		t.Fatal("linux image resolved to the darwin reference")
	}
}
