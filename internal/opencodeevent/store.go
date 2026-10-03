package opencodeevent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const claimHorizon = int64(24 * time.Hour)
const maxClaims = 4096

// All outcomes stay permanently claimed within the horizon, including crash or
// uncertain IO. No finalizer can reopen a record. No raw fact identifiers persist.
type claimRecord struct {
	Key                   string
	TickNS, UncertaintyNS int64
	NativeReadBoundNS     *int64
	State                 string
	Generation            uint64
}
type claimState struct {
	Version           int
	Checkpoint        ClockSample
	NativeReadBoundNS *int64
	EpochFloorTickNS  int64
	RejectedEpoch     string
	Records           []claimRecord
}

func sourceEpochKey(epoch string) string {
	sum := sha256.Sum256([]byte("AN/OpenCode/source-epoch/v1\x00" + epoch))
	return hex.EncodeToString(sum[:])
}

func writeClaims(store *installruntime.OpenCodeStore, s claimState) bool {
	data, err := json.Marshal(s)
	return err == nil && store.Write(data) == nil
}

func claim(store *installruntime.OpenCodeStore, registration installruntime.OpenCodeRegistration, fact FactIdentity, provenance Provenance, now ClockSample, nativeBound int64, generation uint64) AdmissionStatus {
	raw, err := store.Read()
	if err != nil {
		return StoreUnavailable
	}
	s := claimState{}
	if !bytes.Equal(raw, []byte(`{}`)) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&s) != nil || s.Version != 2 || !validSample(s.Checkpoint) || len(s.Records) > maxClaims ||
			s.NativeReadBoundNS == nil || *s.NativeReadBoundNS < 0 || *s.NativeReadBoundNS > int64(103*time.Millisecond) || s.Checkpoint.UncertaintyNS < 2*(*s.NativeReadBoundNS) ||
			s.EpochFloorTickNS < 0 || s.EpochFloorTickNS > s.Checkpoint.TickNS || (s.RejectedEpoch != "" && !hexKey(s.RejectedEpoch)) ||
			(s.RejectedEpoch == "" && s.EpochFloorTickNS != 0) {
			return StoreUnavailable
		}
	}
	seen := make(map[string]bool, len(s.Records))
	for _, record := range s.Records {
		if record.Generation == 0 || record.Generation > generation || record.State != "claimed_unknown" || !hexKey(record.Key) || seen[record.Key] || record.TickNS < 0 || record.TickNS > s.Checkpoint.TickNS || record.UncertaintyNS < 0 || record.UncertaintyNS > int64(2*time.Second) || record.NativeReadBoundNS == nil || *record.NativeReadBoundNS < 0 || *record.NativeReadBoundNS > int64(103*time.Millisecond) {
			return StoreUnavailable
		}
		seen[record.Key] = true
	}
	reset := s.Version != 0 && !sameClock(s.Checkpoint, now)
	if s.Version != 0 && !reset {
		if *s.NativeReadBoundNS != nativeBound || s.Checkpoint.UncertaintyNS != now.UncertaintyNS {
			return StoreUnavailable // a stable policy fence cannot change its bounds
		}
		if now.TickNS < s.Checkpoint.TickNS {
			return TimeUnverified // regression is not positive recovery authority
		}
		allowance := *s.NativeReadBoundNS + nativeBound
		if !qualifiedProgress(s.Checkpoint, now, allowance) {
			// Positively observed same-qualified-clock wall discontinuity. Reject
			// this frame, retain every key, and restart all retention conservatively.
			// The persisted source barrier survives restart and prevents repairing
			// or replaying the triggering epoch, even with new receipt timestamps.
			for i := range s.Records {
				s.Records[i].TickNS = now.TickNS
				bound := max(*s.Records[i].NativeReadBoundNS, nativeBound)
				s.Records[i].NativeReadBoundNS = &bound
			}
			s.Checkpoint, s.NativeReadBoundNS = now, &nativeBound
			s.EpochFloorTickNS, s.RejectedEpoch = now.TickNS, sourceEpochKey(provenance.SourceEpoch)
			if !writeClaims(store, s) {
				return StoreUnavailable
			}
			return TimeUnverified
		}
		if s.RejectedEpoch != "" && (provenance.EpochStartedTickNS <= s.EpochFloorTickNS || sourceEpochKey(provenance.SourceEpoch) == s.RejectedEpoch) {
			return TimeUnverified
		}
	}
	if reset {
		s.EpochFloorTickNS, s.RejectedEpoch = 0, ""
	}
	kept := make([]claimRecord, 0, len(s.Records)+1)
	for _, record := range s.Records {
		if reset {
			// Persist a new boot/domain/fence baseline before considering capacity
			// or duplicate. No wall interval is used to reclaim previous claims.
			record.TickNS = now.TickNS
			bound := max(*record.NativeReadBoundNS, nativeBound)
			record.NativeReadBoundNS = &bound
		}
		elapsed := now.TickNS - record.TickNS
		if elapsed >= claimHorizon+*record.NativeReadBoundNS+nativeBound {
			continue
		}
		kept = append(kept, record)
	}
	key := claimKey(registration, fact, provenance.TerminalBinding)
	status := Admitted
	for _, record := range kept {
		if record.Key == key {
			status = Duplicate
			break
		}
	}
	if status == Admitted && len(kept) >= maxClaims {
		status = Capacity
	}
	if status == Admitted {
		kept = append(kept, claimRecord{Key: key, TickNS: now.TickNS, UncertaintyNS: now.UncertaintyNS, NativeReadBoundNS: &nativeBound, State: "claimed_unknown", Generation: generation})
	}
	s.Version, s.Checkpoint, s.NativeReadBoundNS, s.Records = 2, now, &nativeBound, kept
	if !writeClaims(store, s) {
		return StoreUnavailable
	}
	return status
}

func hexKey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
