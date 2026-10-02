package opencodeevent

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

type AdmissionStatus string

const (
	Admitted         AdmissionStatus = "admitted"
	InvalidFact      AdmissionStatus = "invalid_fact"
	NotRegistered    AdmissionStatus = "not_registered"
	SnapshotChanged  AdmissionStatus = "snapshot_changed"
	TimeUnverified   AdmissionStatus = "time_authority_unverified"
	Expired          AdmissionStatus = "expired"
	StoreUnavailable AdmissionStatus = "store_unavailable"
	Duplicate        AdmissionStatus = "duplicate"
	Capacity         AdmissionStatus = "capacity"
)

// ClockAuthority belongs to trusted system composition, never the wire sender.
// All ticks and epochs use integer nanoseconds; native qualification is external.
type ClockAuthority interface {
	Snapshot(context.Context) (ClockSample, error)
}
type ClockSample struct {
	BootID, Domain, Kind, Fence   string
	TickNS, WallNS, UncertaintyNS int64
}

// TimePolicy is immutable trusted composition, selected from the qualified
// platform ledger. Wire provenance cannot select or enlarge either bound.
// R is native-only; T includes the independently qualified source/JS terms.
type TimePolicy struct {
	ProfileID, RawKind                   string
	NativeReadBoundNS, ComparisonBoundNS int64
}

func (p TimePolicy) valid() bool {
	return boundedToken(p.ProfileID) &&
		(p.RawKind == "linux-boottime" || p.RawKind == "darwin-monotonic-raw" || p.RawKind == "windows-interrupt-precise") &&
		p.NativeReadBoundNS >= 0 && p.NativeReadBoundNS <= int64(103*time.Millisecond) &&
		p.ComparisonBoundNS >= 2*p.NativeReadBoundNS && p.ComparisonBoundNS <= int64(2*time.Second)
}

// Fence binds the native coordinate and immutable policy, never a JS epoch.
// E2 derives the same digest from validated helper output and its fixed ledger.
func (p TimePolicy) Fence(boot, domain string) string {
	if !p.valid() || !boundedToken(boot) || !boundedToken(domain) {
		return ""
	}
	h := sha256.New()
	for _, s := range []string{"AN/OpenCode/clock-policy/v1", boot, domain, p.RawKind, p.ProfileID} {
		_ = binary.Write(h, binary.BigEndian, uint32(len(s)))
		_, _ = h.Write([]byte(s))
	}
	_ = binary.Write(h, binary.BigEndian, p.NativeReadBoundNS)
	_ = binary.Write(h, binary.BigEndian, p.ComparisonBoundNS)
	return hex.EncodeToString(h.Sum(nil))
}

func (p TimePolicy) accepts(s ClockSample) bool {
	return p.valid() && validSample(s) && s.Kind == "continuous" &&
		s.UncertaintyNS == p.ComparisonBoundNS && s.Fence == p.Fence(s.BootID, s.Domain)
}

type TerminalIdentityKind string

const (
	V1FinalMessage  TerminalIdentityKind = "v1_final_message"
	V2TerminalEvent TerminalIdentityKind = "v2_terminal_event"
)

// NativeTerminalIdentity is supplied only after strict decoder/native binding
// validation. Neutral V1 decoding alone does not establish this authority.
type NativeTerminalIdentity struct {
	Kind TerminalIdentityKind
	ID   string
}
type Provenance struct {
	Clock                                                       ClockSample
	NativeCreatedNS, IngressTickNS, SpawnTickNS, DeadlineTickNS int64
	SourceEpoch                                                 string
	EpochStartedTickNS                                          int64
	TerminalBinding                                             NativeTerminalIdentity
}
type FactIdentity struct {
	Kind, Session, Execution, Terminal, Request string
	Root                                        bool
}
type AdmissionRequest struct {
	Fact                     FactIdentity
	Origin                   string
	Provenance               Provenance
	Expected                 installruntime.PolicySnapshot
	Executable, GOOS, GOARCH string
	CommandStarted           time.Time
}
type AdmissionPort interface {
	Admit(context.Context, AdmissionRequest) (*Handoff, AdmissionStatus)
}
type Admission struct {
	ControlRoot string
	Clock       ClockAuthority
	TimePolicy  TimePolicy
}

// Handoff is one durable claim for all channels. Close after bounded IO actually
// returns. Cancellation never reopens the claim or releases an active native ref.
type Handoff struct {
	ctx      context.Context
	cancel   context.CancelFunc
	lease    *opencodeinstall.RegistrationLease
	deadline notification.Deadline
}

func (h *Handoff) Context() context.Context        { return h.ctx }
func (h *Handoff) Channels() (bool, bool)          { return h.lease.Channels() }
func (h *Handoff) Deadline() notification.Deadline { return h.deadline }
func (h *Handoff) Close()                          { h.cancel(); h.lease.Close() }

func boundedToken(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func validSample(s ClockSample) bool {
	return boundedToken(s.BootID) && boundedToken(s.Domain) && boundedToken(s.Fence) &&
		(s.Kind == "continuous" || s.Kind == "uptime") && s.TickNS >= 0 && s.WallNS > 0 &&
		s.UncertaintyNS >= 0 && s.UncertaintyNS <= int64(2*time.Second)
}
func sameClock(a, b ClockSample) bool {
	return a.BootID == b.BootID && a.Domain == b.Domain && a.Kind == b.Kind && a.Fence == b.Fence
}

func qualifiedProgress(a, b ClockSample, allowance int64) bool {
	if b.TickNS < a.TickNS {
		return false
	}
	if allowance > int64(2*time.Second) {
		return false
	}
	elapsed, wall := b.TickNS-a.TickNS, b.WallNS-a.WallNS
	if wall < 0 {
		return wall >= -allowance && elapsed <= allowance+wall
	}
	drift := wall - elapsed // differences of nonnegative int64 values cannot overflow
	return drift >= -allowance && drift <= allowance
}
func validFact(f FactIdentity) bool {
	validID := func(s string) bool { return len(s) > 0 && len(s) <= 256 }
	if !f.Root || !validID(f.Session) || !validID(f.Execution) {
		return false
	}
	switch f.Kind {
	case "turn_idle_verified":
		return validID(f.Terminal) && f.Request == ""
	case "terminal_error":
		return validID(f.Terminal) && f.Request == ""
	case "question_asked", "permission_asked":
		return validID(f.Request) && f.Terminal == ""
	}
	return false
}

func freshness(now ClockSample, p Provenance) (time.Duration, AdmissionStatus) {
	if !validSample(now) || !validSample(p.Clock) || !sameClock(now, p.Clock) ||
		!boundedToken(p.SourceEpoch) || p.EpochStartedTickNS < 0 || p.EpochStartedTickNS > p.Clock.TickNS ||
		p.Clock.UncertaintyNS > now.UncertaintyNS || p.NativeCreatedNS <= 0 ||
		p.IngressTickNS < p.Clock.TickNS || p.SpawnTickNS < p.IngressTickNS || now.TickNS < p.SpawnTickNS ||
		p.DeadlineTickNS <= p.SpawnTickNS || p.DeadlineTickNS-p.SpawnTickNS > int64(20*time.Second) {
		return 0, TimeUnverified
	}
	// The trusted adapter supplies the total qualified comparison allowance.
	// A sender assertion can only restrict provenance; it never grants tolerance.
	uncertainty := now.UncertaintyNS
	// Subtract nonnegative anchors first to keep every arithmetic operation bounded.
	if !qualifiedProgress(p.Clock, now, uncertainty) {
		return 0, TimeUnverified
	}
	age := now.WallNS - p.NativeCreatedNS
	if age < -int64(2*time.Second)+uncertainty || age > int64(60*time.Second)-uncertainty ||
		p.SpawnTickNS-p.IngressTickNS > int64(30*time.Second)-uncertainty ||
		now.TickNS-p.IngressTickNS > int64(50*time.Second)-uncertainty ||
		now.TickNS-p.SpawnTickNS > int64(20*time.Second)-uncertainty ||
		now.TickNS >= p.DeadlineTickNS-uncertainty {
		return 0, Expired
	}
	return time.Duration(p.DeadlineTickNS - now.TickNS - uncertainty), Admitted
}

func claimKey(r installruntime.OpenCodeRegistration, f FactIdentity, terminal NativeTerminalIdentity) string {
	salt, _ := hex.DecodeString(r.Salt)
	h := hmac.New(sha256.New, salt)
	// Length prefixes avoid ambiguous concatenation and accidental cross-domain use.
	for _, s := range []string{"AN/OpenCode/admission/v1", r.Origin, f.Session, f.Execution, f.Kind, f.Terminal, f.Request} {
		_ = binary.Write(h, binary.BigEndian, uint32(len(s)))
		_, _ = h.Write([]byte(s))
	}
	if f.Kind == "terminal_error" {
		_ = binary.Write(h, binary.BigEndian, uint32(len(terminal.Kind)))
		_, _ = h.Write([]byte(terminal.Kind))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a Admission) Admit(ctx context.Context, r AdmissionRequest) (*Handoff, AdmissionStatus) {
	if !validFact(r.Fact) || (r.Fact.Kind == "terminal_error" &&
		(r.Provenance.TerminalBinding.ID != r.Fact.Terminal ||
			(r.Provenance.TerminalBinding.Kind != V1FinalMessage && r.Provenance.TerminalBinding.Kind != V2TerminalEvent))) {
		return nil, InvalidFact
	}
	if r.CommandStarted.IsZero() || time.Since(r.CommandStarted) < 0 || time.Since(r.CommandStarted) >= 20*time.Second || ctx.Err() != nil {
		return nil, Expired
	}
	ctx, cancel := context.WithDeadline(ctx, r.CommandStarted.Add(20*time.Second))
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()
	if !hexKey(r.Origin) {
		return nil, NotRegistered
	}
	if a.Clock == nil || !a.TimePolicy.valid() {
		return nil, TimeUnverified
	}
	now, err := a.Clock.Snapshot(ctx)
	if err != nil || !a.TimePolicy.accepts(now) {
		return nil, TimeUnverified
	}
	remaining, status := freshness(now, r.Provenance)
	if status != Admitted {
		return nil, status
	}
	ctx, senderCancel := context.WithTimeout(ctx, remaining)
	defer func() {
		if !success {
			senderCancel()
		}
	}()
	lease, err := opencodeinstall.AcquireRegistration(ctx, a.ControlRoot, r.Expected, r.Executable, r.GOOS, r.GOARCH, r.Origin)
	if err != nil {
		if ctx.Err() != nil {
			return nil, Expired
		}
		return nil, SnapshotChanged
	}
	defer func() {
		if !success {
			lease.Close()
		}
	}()
	now, err = a.Clock.Snapshot(ctx)
	if err != nil || !a.TimePolicy.accepts(now) {
		return nil, TimeUnverified
	}
	remaining, status = freshness(now, r.Provenance)
	if status != Admitted {
		return nil, status
	}
	bounded, boundedCancel := context.WithTimeout(ctx, remaining)
	defer func() {
		if !success {
			boundedCancel()
		}
	}()
	registration := lease.Registration()
	store, err := installruntime.AcquireOpenCodeStore(bounded, a.ControlRoot, registration)
	if err != nil {
		if bounded.Err() != nil {
			return nil, Expired
		}
		return nil, StoreUnavailable
	}
	defer store.Close()
	// Store contention consumes the original budget. Re-sample immediately before
	// the claim; no native freshness or sender deadline can be restamped here.
	now, err = a.Clock.Snapshot(bounded)
	if err != nil || !a.TimePolicy.accepts(now) {
		return nil, TimeUnverified
	}
	remaining, status = freshness(now, r.Provenance)
	if status != Admitted {
		return nil, status
	}
	if bounded.Err() != nil {
		return nil, Expired
	}
	status = claim(store, registration, r.Fact, r.Provenance, now, a.TimePolicy.NativeReadBoundNS, r.Expected.Installation.Ledger.Generation)
	if status != Admitted {
		return nil, status
	}
	finalCtx, finalCancel := context.WithTimeout(bounded, remaining)
	if finalCtx.Err() != nil {
		finalCancel()
		return nil, Expired
	}
	// Ordinary native notifier accepts <=15s. This only shortens the original
	// command allowance; webhook still uses the same bounded command context.
	nativeRemaining := remaining
	if deadline, ok := finalCtx.Deadline(); ok && time.Until(deadline) < nativeRemaining {
		nativeRemaining = time.Until(deadline)
	}
	if nativeRemaining > 15*time.Second {
		nativeRemaining = 15 * time.Second
	}
	if nativeRemaining <= 0 {
		finalCancel()
		return nil, Expired
	}
	h := &Handoff{ctx: finalCtx, lease: lease, cancel: func() { finalCancel(); boundedCancel(); senderCancel(); cancel() },
		deadline: notification.Deadline{BootID: now.BootID, NotAfter: float64(now.TickNS)/1e9 + nativeRemaining.Seconds()}}
	success = true
	return h, Admitted
}
