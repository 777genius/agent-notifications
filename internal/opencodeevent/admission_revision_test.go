package opencodeevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func qualifiedFixture(a *Admission, r *AdmissionRequest, c *fixtureAuthority) {
	// Independent composition values: native R=103ms, complete T=1.2s.
	a.TimePolicy.NativeReadBoundNS = 103_000_000
	a.TimePolicy.ComparisonBoundNS = 1_200_000_000
	c.sample.UncertaintyNS = 1_200_000_000
	c.sample.Fence = a.TimePolicy.Fence(c.sample.BootID, c.sample.Domain)
	r.Provenance = freshProvenance(c.sample)
}

func readClaimFixture(t *testing.T, ctx context.Context, a Admission, r AdmissionRequest) []byte {
	t.Helper()
	reg := *r.Expected.Installation.Ledger.Consumers["opencode-notifications"].OpenCode
	store, err := installruntime.AcquireOpenCodeStore(ctx, a.ControlRoot, reg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func retainedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var state struct{ Records []struct{ Key string } }
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(state.Records))
	for i, row := range state.Records {
		keys[i] = row.Key
	}
	return keys
}

// Red on the exact reviewed source: complete T used to hide 1s native drift.
// Legal T=1.2s still permits normal consecutive claims; native R+R does not.
func TestAdmissionNativeReadDrift(t *testing.T) {
	ctx, a, r, c := admissionStoreFixture(t)
	qualifiedFixture(&a, &r, c)
	tryAdmissionCommand(t, ctx, a, r, Admitted)
	c.sample.TickNS += int64(time.Second)
	c.sample.WallNS += int64(time.Second)
	r.Provenance = freshProvenance(c.sample)
	r.Fact.Request = "second-normal"
	tryAdmissionCommand(t, ctx, a, r, Admitted)
	keys := retainedKeys(t, readClaimFixture(t, ctx, a, r))
	c.sample.TickNS += int64(time.Second)
	c.sample.WallNS += int64(2 * time.Second)
	r.Provenance = freshProvenance(c.sample)
	r.Fact.Request = "native-drift-trigger"
	tryAdmissionCommand(t, ctx, a, r, TimeUnverified)
	after := retainedKeys(t, readClaimFixture(t, ctx, a, r))
	if len(after) != 2 || after[0] != keys[0] || after[1] != keys[1] {
		t.Fatal("drift changed business claims")
	}
}

// Independent fence digest and exact native tolerance catch changes to the
// E2 byte contract, max(Rprev,Rnow), or hidden total-T progress tolerance.
func TestAdmissionNativePolicyGoldenAndProgressBoundary(t *testing.T) {
	ctx, a, r, c := admissionStoreFixture(t)
	qualifiedFixture(&a, &r, c)
	if c.sample.Fence != "cdd5dadec803fe5c5779886f4ef964085e5d5878e75b3418e40cfefc13c9b648" {
		t.Fatal("qualified policy fence differs from external golden")
	}
	tryAdmissionCommand(t, ctx, a, r, Admitted)
	c.sample.TickNS += 1_000_000_000
	c.sample.WallNS += 1_206_000_000
	r.Provenance = freshProvenance(c.sample)
	r.Fact.Request = "at-native-bound"
	tryAdmissionCommand(t, ctx, a, r, Admitted)
	c.sample.TickNS += 1_000_000_000
	c.sample.WallNS += 1_206_000_001
	r.Provenance = freshProvenance(c.sample)
	r.Fact.Request = "over-native-bound"
	tryAdmissionCommand(t, ctx, a, r, TimeUnverified)
}

// Native retention is 24h+103ms+103ms, independent of total T=1.2s.
// External full-capacity rows catch premature eviction and overlong retention.
func TestAdmissionNativeRetentionBoundary(t *testing.T) {
	ctx, a, r, c := admissionStoreFixture(t)
	qualifiedFixture(&a, &r, c)
	// Each admission is a separate command; setup and prior assertions must
	// not consume its budget. Snapshot reads also have their own live context.
	admit := func(want AdmissionStatus) {
		commandCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		request := r
		request.CommandStarted = time.Now()
		tryAdmission(t, commandCtx, a, request, want)
	}
	keys := func() []string {
		readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return retainedKeys(t, readClaimFixture(t, readCtx, a, r))
	}
	rows := make([]map[string]any, 4096)
	for i := range rows {
		key := sha256.Sum256([]byte{byte(i >> 8), byte(i)})
		rows[i] = map[string]any{"Key": hex.EncodeToString(key[:]), "TickNS": c.sample.TickNS, "UncertaintyNS": 1_200_000_000, "NativeReadBoundNS": 103_000_000, "State": "claimed_unknown", "Generation": 1}
	}
	raw, _ := json.Marshal(map[string]any{"Version": 2, "Checkpoint": c.sample, "NativeReadBoundNS": 103_000_000, "Records": rows})
	reg := *r.Expected.Installation.Ledger.Consumers["opencode-notifications"].OpenCode
	store, err := installruntime.AcquireOpenCodeStore(ctx, a.ControlRoot, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Write(raw); err != nil {
		t.Fatal(err)
	}
	store.Close()
	c.sample.TickNS += int64(24*time.Hour) + 205_999_999
	c.sample.WallNS += int64(24*time.Hour) + 205_999_999
	r.Provenance = freshProvenance(c.sample)
	admit(Capacity)
	if len(keys()) != 4096 {
		t.Fatal("early native pruning")
	}
	c.sample.TickNS++
	c.sample.WallNS++
	r.Provenance = freshProvenance(c.sample)
	admit(Admitted)
	if len(keys()) != 1 {
		t.Fatal("verified native capacity did not recover")
	}
}

// Red on reviewed source: a persistent wall jump suppressed this incarnation
// forever. Reopen the durable store, reject the old epoch, preserve dedup, and
// retain old claims for a full native 24h+206ms after the conservative baseline.
func TestAdmissionPersistentWallRecovery(t *testing.T) {
	ctx, a, r, c := admissionStoreFixture(t)
	qualifiedFixture(&a, &r, c)
	tryAdmissionCommand(t, ctx, a, r, Admitted)
	initial := r.Fact
	initialKey := retainedKeys(t, readClaimFixture(t, ctx, a, r))[0]
	c.sample.TickNS += int64(time.Second)
	c.sample.WallNS += int64(48*time.Hour + time.Second)
	baseline := c.sample.TickNS
	r.Provenance = freshProvenance(c.sample)
	r.Fact.Request = "wall-jump-trigger"
	tryAdmissionCommand(t, ctx, a, r, TimeUnverified)
	triggerEpoch := r.Provenance.SourceEpoch
	if keys := retainedKeys(t, readClaimFixture(t, ctx, a, r)); len(keys) != 1 || keys[0] != initialKey {
		t.Fatal("wall jump pruned or claimed")
	}
	// A restarted domain instance reads the persisted checkpoint/barrier.
	a = Admission{ControlRoot: a.ControlRoot, Clock: &fixtureAuthority{c.sample}, TimePolicy: a.TimePolicy}
	c = a.Clock.(*fixtureAuthority)
	c.sample.TickNS += int64(3 * time.Second)
	c.sample.WallNS += int64(3 * time.Second)
	r.Provenance = freshProvenance(c.sample)
	freshEpoch := r.Provenance.SourceEpoch
	r.Provenance.SourceEpoch = triggerEpoch
	before := string(readClaimFixture(t, ctx, a, r))
	tryAdmissionCommand(t, ctx, a, r, TimeUnverified)
	if string(readClaimFixture(t, ctx, a, r)) != before {
		t.Fatal("old epoch repaired itself")
	}
	r.Provenance.SourceEpoch = freshEpoch
	r.Provenance.EpochStartedTickNS = baseline
	tryAdmissionCommand(t, ctx, a, r, TimeUnverified)
	r.Provenance = freshProvenance(c.sample)
	r.Fact.Request = "fresh-new-source"
	tryAdmissionCommand(t, ctx, a, r, Admitted)
	r.Fact = initial
	tryAdmissionCommand(t, ctx, a, r, Duplicate)
	remaining := baseline + int64(24*time.Hour) + 205_999_999 - c.sample.TickNS
	c.sample.TickNS += remaining
	c.sample.WallNS += remaining
	r.Provenance = freshProvenance(c.sample)
	tryAdmissionCommand(t, ctx, a, r, Duplicate)
	c.sample.TickNS++
	c.sample.WallNS++
	r.Provenance = freshProvenance(c.sample)
	tryAdmissionCommand(t, ctx, a, r, Admitted)
}

type unavailableAuthority struct{}

func (unavailableAuthority) Snapshot(context.Context) (ClockSample, error) {
	return ClockSample{}, errors.New("TEST-unavailable")
}

// Regression, missing authority and sender/native policy mismatch must leave
// the checkpoint untouched; none proves a recoverable wall discontinuity.
func TestAdmissionNoRepairWithoutPositiveAuthority(t *testing.T) {
	for _, mode := range []string{"regression", "unavailable", "unqualified", "senderEpochFence"} {
		t.Run(mode, func(t *testing.T) {
			ctx, a, r, c := admissionStoreFixture(t)
			qualifiedFixture(&a, &r, c)
			tryAdmissionCommand(t, ctx, a, r, Admitted)
			before := string(readClaimFixture(t, ctx, a, r))
			switch mode {
			case "regression":
				c.sample.TickNS -= int64(time.Second)
				c.sample.WallNS += int64(48 * time.Hour)
			case "unavailable":
				a.Clock = unavailableAuthority{}
			case "unqualified":
				c.sample.UncertaintyNS++
			case "senderEpochFence":
				c.sample.Fence = "random-JS-epoch"
			}
			r.Provenance = freshProvenance(c.sample)
			r.Fact.Request = "no-repair"
			tryAdmissionCommand(t, ctx, a, r, TimeUnverified)
			if string(readClaimFixture(t, ctx, a, r)) != before {
				t.Fatal("unverified authority repaired durable state")
			}
		})
	}
}

// Missing persisted native qualification cannot be inferred from old total T,
// even after a coordinate change. Legacy payloads stay retained and suppressed.
func TestAdmissionMissingNativeStoreAuthority(t *testing.T) {
	for _, field := range []string{"checkpointR", "claimR", "legacyPayload"} {
		t.Run(field, func(t *testing.T) {
			ctx, a, r, c := admissionStoreFixture(t)
			qualifiedFixture(&a, &r, c)
			tryAdmissionCommand(t, ctx, a, r, Admitted)
			var state map[string]any
			if err := json.Unmarshal(readClaimFixture(t, ctx, a, r), &state); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "checkpointR":
				delete(state, "NativeReadBoundNS")
			case "claimR":
				delete(state["Records"].([]any)[0].(map[string]any), "NativeReadBoundNS")
			case "legacyPayload":
				state["Version"] = 1
			}
			raw, _ := json.Marshal(state)
			reg := *r.Expected.Installation.Ledger.Consumers["opencode-notifications"].OpenCode
			store, err := installruntime.AcquireOpenCodeStore(ctx, a.ControlRoot, reg)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Write(raw); err != nil {
				t.Fatal(err)
			}
			store.Close()
			c.sample.BootID = "TEST-next-qualified-boot"
			c.sample.Fence = a.TimePolicy.Fence(c.sample.BootID, c.sample.Domain)
			r.Provenance = freshProvenance(c.sample)
			tryAdmissionCommand(t, ctx, a, r, StoreUnavailable)
			if string(readClaimFixture(t, ctx, a, r)) != string(raw) {
				t.Fatal("missing native authority repaired itself")
			}
		})
	}
}

// Neutral V1 remains separately compatible. Strict private errors require the
// actual typed final message/event; absence/mismatch/unverified type cannot claim.
func TestAdmissionRealTerminalError(t *testing.T) {
	for _, kind := range []TerminalIdentityKind{V1FinalMessage, V2TerminalEvent} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, a, r, _ := admissionStoreFixture(t)
			r.Fact.Kind, r.Fact.Request, r.Fact.Terminal = "terminal_error", "", "actual-final-native-message"
			r.Provenance.TerminalBinding = NativeTerminalIdentity{Kind: kind, ID: r.Fact.Terminal}
			valid := r.Provenance.TerminalBinding
			for _, invalid := range []NativeTerminalIdentity{{}, {Kind: kind, ID: "different-native-binding"}, {Kind: "unverified", ID: r.Fact.Terminal}} {
				r.Provenance.TerminalBinding = invalid
				tryAdmissionCommand(t, ctx, a, r, InvalidFact)
				if string(readClaimFixture(t, ctx, a, r)) != "{}" {
					t.Fatal("invalid terminal binding wrote claim")
				}
			}
			r.Provenance.TerminalBinding = valid
			tryAdmissionCommand(t, ctx, a, r, Admitted)
			tryAdmissionCommand(t, ctx, a, r, Duplicate)
			keys := retainedKeys(t, readClaimFixture(t, ctx, a, r))
			// Golden from independent Python hmac/struct, including real terminal type.
			want := map[TerminalIdentityKind]string{V1FinalMessage: "034d91dab0e74ebcc49e087e3359d716d37355a27f19e85cf996cc3ed399c949", V2TerminalEvent: "0dcee2987093b7b8e9858b0f92dd5a1006c93f2e15bbce306471e9b19d7b88c0"}[kind]
			if len(keys) != 1 || keys[0] != want {
				t.Fatalf("terminal claim=%v, want %s", keys, want)
			}
			r.Fact.Terminal = "another-real-terminal"
			r.Provenance.TerminalBinding.ID = r.Fact.Terminal
			tryAdmissionCommand(t, ctx, a, r, Admitted)
		})
	}
}
