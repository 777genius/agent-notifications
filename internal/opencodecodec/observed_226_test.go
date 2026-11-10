package opencodecodec

import "testing"

// Official 2.2.6 source/image observations have no product clock admission.
// The separate exact Go/JS reader/clock evidence is still unqualified.
func TestOpenCode226ObservedImagesDoNotGrantClock(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, image string }{
		{"linux", "amd64", "50503d24577fa4117a7c95ee85b2cd679606a87eacf20ca54ed5db5c9e1cf895"},
		{"darwin", "arm64", "90e4d03597b03d2051b65b4d793467764e55bacd8e22bf4824479c7233ea0e15"},
	} {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			key := ImageKey{GOOS: tc.goos, GOARCH: tc.goarch, Entry: "serve", SHA256: tc.image}
			candidate, ok := LookupCandidate(key)
			if !ok || candidate != (Candidate{Version: "2.2.6", Generation: "v2"}) {
				t.Fatalf("missing official observed candidate: %+v %v", candidate, ok)
			}
			if clock, ok := LookupQualifiedClock(key); ok || clock != (ClockRow{}) {
				t.Fatalf("unproved original time accepted: %+v", clock)
			}
			for _, mutate := range []func(*ImageKey){
				func(k *ImageKey) { k.SHA256 = "unknown" },
				func(k *ImageKey) { k.Entry = "tui" },
				func(k *ImageKey) { k.Entry = "run" },
				func(k *ImageKey) { k.GOARCH = "unknown" },
				func(k *ImageKey) { k.GOOS = "unknown" },
			} {
				unknown := key
				mutate(&unknown)
				if item, known := LookupCandidate(unknown); known || item != (Candidate{}) {
					t.Fatalf("foreign image/entry inherited candidate: %+v", unknown)
				}
				if clock, ok := LookupQualifiedClock(unknown); ok || clock != (ClockRow{}) {
					t.Fatalf("foreign image/entry gained clock: %+v", unknown)
				}
			}
		})
	}
}
