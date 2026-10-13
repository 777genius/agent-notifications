package opencodeevent

import (
	"context"
	"encoding/json"
	"github.com/777genius/agent-notifications/internal/opencodecodec"
	"testing"
	"time"
)

// Candidate R/T values are independent TEST policy inputs, never a production
// row or native qualification flag. Exercise the actual private decoder.
func TestPrivateSameCoordinateCandidateCalibrationAndGeneration(t *testing.T) {
	images := []struct{ sha, generation string }{
		{"0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427", "v1"},
		{"9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2", "v1"},
		{"f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7", "v2"},
	}
	for _, image := range images {
		candidate, known := opencodecodec.LookupCandidate(opencodecodec.ImageKey{GOOS: "linux", GOARCH: "amd64", Entry: "serve", SHA256: image.sha})
		if !known || candidate.Generation != image.generation {
			t.Fatal("closed candidate lookup")
		}
		selection := ClockSelection{Policy: TimePolicy{ProfileID: "TEST-candidate-r103-t430", RawKind: "linux-boottime", NativeReadBoundNS: 103_000_000, ComparisonBoundNS: 430_000_000}, CalibrationID: "TEST-containing-source", Generation: candidate.Generation, TranslationBoundNS: 224_000_000}
		var frame map[string]any
		if json.Unmarshal(literalPrivate(t, image.generation, "terminal_error"), &frame) != nil {
			t.Fatal("literal fixture")
		}
		p := frame["provenance"].(map[string]any)
		p["policyID"] = selection.Policy.ProfileID
		p["fence"] = selection.Policy.Fence("12345678-1234-1234-1234-123456789abc", "linux-time:4:7")
		c := p["calibration"].(map[string]any)
		c["calibrationID"] = selection.CalibrationID
		c["errorNS"] = "224000000"
		c["sourceLoNS"] = "99990000000"
		c["sourceHiNS"] = "100010000000"
		raw, _ := json.Marshal(frame)
		decoded, err := DecodePrivate(raw, selection)
		if err != nil || decoded.Provenance.EpochStartedTickNS != 99000000000 || decoded.Provenance.IngressTickNS != 100010000000 || decoded.Provenance.DeadlineTickNS != 121000000000 {
			t.Fatal("containing calibration changed original epoch/ingress/budget", err)
		}
		for _, tc := range []struct{ name, lo, hi string }{
			// epoch.after already includes the raw proc quantum. These schema-valid
			// intervals cannot gain overlap from a second allowance in the decoder.
			{"touchingBefore", "99990000000", "100000000000"},
			{"oneNSGapBefore", "99990000000", "99999999999"},
			{"zeroWidthAtNativeLo", "100000000000", "100000000000"},
			{"zeroWidthInsideNative", "100001000000", "100001000000"},
			{"disjointBefore", "99900000000", "99910000000"},
			{"disjointAfter", "100020000000", "100030000000"},
			{"overwide", "99900000000", "100124000001"},
		} {
			t.Run(candidate.Version+"/"+tc.name, func(t *testing.T) {
				c["sourceLoNS"] = tc.lo
				c["sourceHiNS"] = tc.hi
				bad, _ := json.Marshal(frame)
				if _, err := DecodePrivate(bad, selection); err == nil {
					t.Fatal("schema-valid inconsistent calibration accepted")
				}
			})
		}
		// The wire cannot choose the other generation even with a valid source
		// bracket, and no second decoder is tried after rejection.
		c["sourceLoNS"] = "99990000000"
		c["sourceHiNS"] = "100010000000"
		opposite := selection
		opposite.Generation = "v2"
		if selection.Generation == "v2" {
			opposite.Generation = "v1"
		}
		raw, _ = json.Marshal(frame)
		if _, err := DecodePrivate(raw, opposite); err == nil {
			t.Fatal("opposite generation accepted")
		}
		p["epochStartedTickNS"] = "100000000001"
		raw, _ = json.Marshal(frame)
		if _, err := DecodePrivate(raw, selection); err == nil {
			t.Fatal("later epoch replaced original pre-anchor epoch")
		}
	}
}

// The admission adapter must propagate its tightened context to the held image
// reader rather than perform revalidation under a longer origin-only budget.
type calibrationContextSnapshot struct {
	want  context.Context
	t     *testing.T
	reads int
}

func (p *calibrationContextSnapshot) SampleSnapshot() (ClockSnapshot, error) {
	p.t.Fatal("tightened context lost before runtime read")
	return ClockSnapshot{}, ErrClockUnavailable
}
func (p *calibrationContextSnapshot) SampleSnapshotContext(ctx context.Context) (ClockSnapshot, error) {
	if ctx != p.want {
		p.t.Fatal("caller context replaced")
	}
	p.reads++
	return independentSnapshot(), nil
}
func TestTrustedClockRetainsSuppliedAdmissionContext(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer cancel()
	port := &calibrationContextSnapshot{want: ctx, t: t}
	clock := TrustedClock{Source: port, Selection: independentSelection("v1")}
	if _, err := clock.Snapshot(ctx); err != nil || port.reads != 1 {
		t.Fatal("held reader did not receive original supplied context", err)
	}
	cancel()
	if _, err := clock.Snapshot(ctx); err == nil || port.reads != 1 {
		t.Fatal("cancelled admission acquired new read/budget")
	}
}
