package main

import (
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/opencodehost"
)

const unknownImage = "1111111111111111111111111111111111111111111111111111111111111111"

func familyLive(goos, goarch, entry, sha string, publisher bool) runtimeLiveImage {
	live := runtimeLiveImage{GOOS: goos, GOARCH: goarch, Entry: entry, SHA256: sha, NativePID: 4242, Publisher: publisher}
	if goos == "linux" {
		live.ProcessStartTick = 1
	} else {
		live.fingerprint = [32]byte{1}
	}
	return live
}

func TestFamilyGenerationRanges(t *testing.T) {
	for version, want := range map[string]string{
		"1.18.33": "v1", "1.19.5": "v1", "1.99.0": "v1", "2.0.21": "v2", "2.2.7": "v2", "2.3.0": "v2", "2.10.0": "v2",
		"1.18.32": "", "1.0.142": "", "2.0.20": "", "3.0.0": "", "0.9.0": "",
		"2.2.7-beta.1": "", "2.2": "", "+2.0.21": "", "2.-0.21": "", "2.02.7": "", "": "",
	} {
		got, ok := familyOf(version)
		if ok != (want != "") || got.generation != want {
			t.Fatalf("familyOf(%q) = %q/%v, want %q", version, got.generation, ok, want)
		}
	}
}

// Every platform resolves a new official release onto its reference; only macOS
// additionally needs the OS-verified publisher.
func TestFamilyAdmitsReleasesInRange(t *testing.T) {
	cases := []struct {
		name    string
		live    runtimeLiveImage
		version string
		want    runtimeObserverGeneration
	}{
		{"darwin arm64 v2 signed", familyLive("darwin", "arm64", "serve", unknownImage, true), "2.3.0", observerV2},
		{"darwin arm64 v1 signed", familyLive("darwin", "arm64", "serve", unknownImage, true), "1.19.5", observerV1},
		{"darwin arm64 v1 tui signed", familyLive("darwin", "arm64", "tui", unknownImage, true), "1.19.5", observerV1},
		{"darwin amd64 v2 signed", familyLive("darwin", "amd64", "serve", unknownImage, true), "2.3.0", observerV2},
		{"darwin unsigned", familyLive("darwin", "arm64", "serve", unknownImage, false), "2.3.0", observerUnverified},
		{"linux amd64 v2", familyLive("linux", "amd64", "serve", unknownImage, false), "2.3.0", observerV2},
		{"linux amd64 v1", familyLive("linux", "amd64", "serve", unknownImage, false), "1.19.5", observerV1},
		{"linux arm64 v2", familyLive("linux", "arm64", "serve", unknownImage, false), "2.3.0", observerV2},
		{"linux arm64 v1 run", familyLive("linux", "arm64", "run", unknownImage, false), "1.19.5", observerV1},
		{"windows amd64 v2", familyLive("windows", "amd64", "serve", unknownImage, false), "2.3.0", observerV2},
		{"windows amd64 v1 tui", familyLive("windows", "amd64", "tui", unknownImage, false), "1.19.5", observerV1},
		{"v2 has no tui reference", familyLive("linux", "amd64", "tui", unknownImage, false), "2.3.0", observerUnverified},
		{"below v1 floor", familyLive("linux", "amd64", "serve", unknownImage, false), "1.18.32", observerUnverified},
		{"legacy v1", familyLive("linux", "amd64", "serve", unknownImage, false), "1.0.142", observerUnverified},
		{"next major", familyLive("linux", "amd64", "serve", unknownImage, false), "3.0.0", observerUnverified},
		{"prerelease", familyLive("linux", "amd64", "serve", unknownImage, false), "2.3.0-rc.1", observerUnverified},
		{"unknown platform", familyLive("freebsd", "amd64", "serve", unknownImage, false), "2.3.0", observerUnverified},
		{"windows arm64", familyLive("windows", "arm64", "serve", unknownImage, false), "2.3.0", observerUnverified},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := qualifyRuntimeObserver(opencodehost.Profile{Version: c.version}, c.live); got != c.want {
				t.Fatalf("qualifyRuntimeObserver(%s) = %v, want %v", c.version, got, c.want)
			}
		})
	}
}

// A reviewed exact image keeps its own row even when it is signed, and the
// observed-only 2.2.6 darwin row (no clock row of its own) still uses the family.
func TestFamilyKeepsExactReviewedImages(t *testing.T) {
	v1 := familyLive("darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524", true)
	if familyTrusted(v1) {
		t.Fatal("reviewed V1 image was remapped to the family reference")
	}
	if got := qualifyRuntimeObserver(opencodehost.Profile{Version: "1.18.33"}, v1); got != observerV1 {
		t.Fatalf("reviewed V1 = %v", got)
	}
	observed := familyLive("darwin", "arm64", "serve", "90e4d03597b03d2051b65b4d793467764e55bacd8e22bf4824479c7233ea0e15", true)
	if got := qualifyRuntimeObserver(opencodehost.Profile{Version: "2.2.6"}, observed); got != observerV2 {
		t.Fatalf("official 2.2.6 = %v, want observerV2", got)
	}
}

// An event of a family image may match either generation's row; an exact image only its own.
func TestRuntimeSelections(t *testing.T) {
	if n := len(runtimeSelections(familyLive("linux", "amd64", "serve", unknownImage, false))); n != 2 {
		t.Fatalf("family serve selections = %d, want 2 (v1+v2)", n)
	}
	if n := len(runtimeSelections(familyLive("linux", "amd64", "tui", unknownImage, false))); n != 1 {
		t.Fatalf("family tui selections = %d, want 1 (v1 only)", n)
	}
	if n := len(runtimeSelections(familyLive("darwin", "arm64", "serve", unknownImage, false))); n != 0 {
		t.Fatalf("unsigned darwin selections = %d, want 0", n)
	}
	exact := familyLive("darwin", "arm64", "serve", "139ddeb6a46ba276827bb8f79c7b28208621746e4fd6914d9ae71cc1a0a57524", true)
	if n := len(runtimeSelections(exact)); n != 1 {
		t.Fatalf("exact image selections = %d, want 1", n)
	}
}
