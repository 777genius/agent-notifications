// Package journal implements durable admission only. Callers perform preflight
// between Lookup and Admit, and effects after Admit returns Fresh, outside locks.
// An error from Admit never permits an effect, even if a write reached disk.
package journal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const Protocol = "agent-notify/journal/v1"
const MinRetention uint64 = 7 * 24 * 60 * 60

var (
	ErrRepair      = errors.New("state_repair_required")
	ErrFull        = errors.New("journal_full")
	ErrConflict    = errors.New("idempotency_conflict")
	ErrRate        = errors.New("rate_limited")
	ErrCAS         = errors.New("attempt_mismatch")
	ErrUnavailable = errors.New("journal_unavailable")
	ErrInvalid     = errors.New("invalid_journal_argument")
)

type KeyKind string

const (
	Explicit   KeyKind = "explicit"
	ClientCall KeyKind = "client_call"
	Generated  KeyKind = "generated"
)

type Key struct {
	Source, Session string
	Kind            KeyKind
	Request         string
}

// Digest is computed by the service from versioned normalized caller payload,
// never from mutable delivery configuration. No payload is accepted or stored.
type Digest [32]byte

func hash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (k Key) scoped(ns string) string {
	return hash(Protocol, ns, k.Source, k.Session, string(k.Kind), k.Request)
}
func (k Key) session(ns string) string { return hash(Protocol, ns, k.Source, k.Session) }
func validText(s string, n int, required bool) bool {
	return (!required || s != "") && len(s) <= n && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func (k Key) valid() bool {
	return validText(k.Source, 256, true) && validText(k.Session, 256, true) && validText(k.Request, 256, true) && validKind(k.Kind)
}
func validKind(k KeyKind) bool { return k == Explicit || k == ClientCall || k == Generated }
func isHex(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func token() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}

// Snapshot is a minimal immutable decision, not a callback dependency. The
// service maps these DTOs to the final notification receipt contract.
type Target struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Application string `json:"application"`
	Identity    string `json:"identity"`
}
type Navigation struct {
	Capability string `json:"capability"`
	Precision  string `json:"precision"`
	Scope      string `json:"scope"`
	Reason     string `json:"reason"`
}
type Snapshot struct {
	Target     Target     `json:"target"`
	Policy     string     `json:"policy"`
	Navigation Navigation `json:"navigation"`
}
type Receipt struct {
	Status            string      `json:"status"`
	Reason            string      `json:"reason"`
	Backend           string      `json:"backend"`
	RequestID         *string     `json:"request_id"`
	TrackingID        string      `json:"tracking_id"`
	KeyKind           KeyKind     `json:"key_kind"`
	Decision          Snapshot    `json:"decision"`
	OutcomeNavigation *Navigation `json:"outcome_navigation,omitempty"`
}

// MaxRate is the existing maximum record/JSON-array budget. Rate capacity does
// not reserve storage: the independent record, byte and retention limits apply.
const MaxRate = 10000

// RatePolicy is a trusted configuration snapshot, never model payload. Windows
// are fixed at 60 seconds (session/runtime) and 2 seconds (runtime burst).
// The whole-zero value selects defaults; individual zero values never disable
// a limiter. Enablement and configuration generation fencing belong to callers.
type RatePolicy struct {
	SessionPerMinute int
	RuntimePerMinute int
	Burst            int
}

// Normalize validates a snapshot and resolves the backward-compatible default.
// Session and burst limits cannot exceed the enclosing runtime minute limit.
func (p RatePolicy) Normalize() (RatePolicy, error) {
	if p == (RatePolicy{}) {
		return RatePolicy{6, 30, 3}, nil
	}
	if p.SessionPerMinute < 1 || p.RuntimePerMinute < 1 || p.Burst < 1 ||
		p.SessionPerMinute > MaxRate || p.RuntimePerMinute > MaxRate || p.Burst > MaxRate ||
		p.SessionPerMinute > p.RuntimePerMinute || p.Burst > p.RuntimePerMinute {
		return p, ErrInvalid
	}
	return p, nil
}

type Admission struct {
	Rates      RatePolicy
	Key        Key
	Digest     Digest
	TrackingID string
	Decision   Snapshot
}
type Record struct {
	Session string  `json:"session"`
	Digest  string  `json:"digest"`
	Attempt string  `json:"attempt"`
	Created uint64  `json:"created"`
	State   string  `json:"state"`
	Receipt Receipt `json:"receipt"`
}
type Result struct {
	// ScopedKey and Attempt let the service derive a stable native notification ID.
	ScopedKey string
	Found     bool
	Fresh     bool
	Record    Record
}

// Dispatching is always exposed as unknown/pending_submission. It is never a
// lease, and neither Lookup nor Admit takes over an existing attempt.
func result(key string, r Record, fresh bool) Result {
	return Result{ScopedKey: key, Found: true, Fresh: fresh, Record: r}
}

type Sample struct {
	Boot      string
	Seconds   uint64
	Available bool
}

// Clock must return promptly and be safe for concurrent calls. Unavailable
// samples freeze collection and rate recovery; nil has the same behavior.
type Clock interface{ Sample() Sample }
type ClockFunc func() Sample

func (f ClockFunc) Sample() Sample { return f() }

type Limits struct {
	Records   int    `json:"records"`
	Bytes     int    `json:"bytes"`
	Retention uint64 `json:"retention"`
}

func (l Limits) normalize() (Limits, error) {
	if l.Records == 0 {
		l.Records = 10000
	}
	if l.Bytes == 0 {
		l.Bytes = 8 << 20
	}
	if l.Retention == 0 {
		l.Retention = MinRetention
	}
	if l.Records < 1 || l.Records > 10000 || l.Bytes < 1024 || l.Bytes > 8<<20 || l.Retention < MinRetention || l.Retention > 365*86400 {
		return l, ErrInvalid
	}
	return l, nil
}

type Options struct {
	Root   string
	Clock  Clock
	Limits Limits
}
type Store struct {
	root      string
	clock     Clock
	limits    Limits
	namespace string
	fault     func(string) error
}
type elapsed struct {
	Logical uint64 `json:"logical"`
	Boot    string `json:"boot"`
	Seconds uint64 `json:"seconds"`
}
type event struct {
	At      uint64 `json:"at"`
	Session string `json:"session"`
}
type disk struct {
	Version   int               `json:"version"`
	Namespace string            `json:"namespace"`
	Limits    Limits            `json:"limits"`
	Clock     elapsed           `json:"clock"`
	Events    []event           `json:"events"`
	Records   map[string]Record `json:"records"`
}

func (s *Store) advance(d *disk) (bool, error) {
	x := Sample{}
	if s.clock != nil {
		x = s.clock.Sample()
	}
	if !x.Available || !validText(x.Boot, 256, true) {
		d.Clock.Boot = ""
		d.Clock.Seconds = 0
		return false, nil
	}
	if d.Clock.Boot != x.Boot {
		d.Clock.Boot = x.Boot
		d.Clock.Seconds = x.Seconds
		return false, nil
	}
	if x.Seconds < d.Clock.Seconds {
		return false, nil
	} // retain high water, do not double-credit recovery
	delta := x.Seconds - d.Clock.Seconds
	// Whole-second endpoints prove at least delta-1 whole elapsed seconds.
	// Discard rounding uncertainty on each interval so GC/rates cannot run early.
	if delta > 0 {
		delta--
	}
	if delta > math.MaxUint64-d.Clock.Logical {
		return false, ErrFull
	}
	d.Clock.Logical += delta
	d.Clock.Seconds = x.Seconds
	return true, nil
}

func persistClockOnRefusal(d *disk, before elapsed, err error) (bool, error) {
	if d.Clock.Boot == "" || d.Clock.Boot == before.Boot {
		d.Clock = before
		return false, err
	}
	return true, err
}
func (s *Store) Namespace() string { return s.namespace }
func newStore(o Options) (*Store, error) {
	l, e := o.Limits.normalize()
	if e != nil {
		return nil, e
	}
	return &Store{root: o.Root, clock: o.Clock, limits: l}, nil
}

// Initialize is setup-only, for an explicitly never-initialized private root.
// The registry must remember initialization outside caches. Normal consumers
// must use Open; missing expected state must never trigger Initialize.
func Initialize(ctx context.Context, o Options) (*Store, error) {
	s, e := newStore(o)
	if e != nil {
		return nil, e
	}
	if e = s.bootstrap(ctx); e != nil {
		return nil, e
	}
	return s, nil
}

// Open requires a complete existing namespace/journal/lock set. It never repairs
// or creates missing state, including when the entire expected root was lost.
func Open(ctx context.Context, o Options) (*Store, error) {
	s, e := newStore(o)
	if e != nil {
		return nil, e
	}
	if e = s.transaction(ctx, func(d *disk) (bool, error) { s.namespace = d.Namespace; return false, nil }); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) Lookup(ctx context.Context, k Key, digest Digest) (out Result, err error) {
	if !k.valid() {
		return out, ErrInvalid
	}
	err = s.transaction(ctx, func(d *disk) (bool, error) { var e error; out, e = lookup(d, k, digest); return false, e })
	return
}
func lookup(d *disk, k Key, digest Digest) (Result, error) {
	r, ok := d.Records[k.scoped(d.Namespace)]
	if !ok {
		return Result{}, nil
	}
	if r.Digest != hex.EncodeToString(digest[:]) {
		return Result{}, ErrConflict
	}
	return result(k.scoped(d.Namespace), r, false), nil
}
func (s *Store) Admit(ctx context.Context, a Admission) (out Result, err error) {
	if !a.Key.valid() {
		return out, ErrInvalid
	}
	err = s.transaction(ctx, func(d *disk) (bool, error) {
		var e error
		out, e = lookup(d, a.Key, a.Digest)
		if e != nil || out.Found {
			return false, e
		}
		// Replay precedes all mutable admission policy/decision validation.
		rates, e := a.Rates.Normalize()
		if e != nil || !validText(a.TrackingID, 256, true) || !validSnapshot(a.Decision) {
			return false, ErrInvalid
		}
		clock := d.Clock
		proven, e := s.advance(d)
		if e != nil {
			return false, e
		}
		now := d.Clock.Logical
		if proven {
			for k, r := range d.Records {
				if now-r.Created >= d.Limits.Retention {
					delete(d.Records, k)
				}
			}
		}
		// Existing keys replay until another admission/Collect commits their removal.
		if len(d.Records) >= d.Limits.Records {
			return persistClockOnRefusal(d, clock, ErrFull)
		}
		events := d.Events[:0]
		session := a.Key.session(d.Namespace)
		perSession, burst := 0, 0
		for _, v := range d.Events {
			if now-v.At < 60 {
				events = append(events, v)
				if v.Session == session {
					perSession++
				}
				if now-v.At < 2 {
					burst++
				}
			}
		}
		if len(events) >= rates.RuntimePerMinute || perSession >= rates.SessionPerMinute || burst >= rates.Burst {
			return persistClockOnRefusal(d, clock, ErrRate)
		}
		d.Events = append(events, event{now, session})
		attempt, e := token()
		if e != nil {
			return false, e
		}
		receipt := Receipt{Status: "unknown", Reason: "pending_submission", TrackingID: a.TrackingID, KeyKind: a.Key.Kind, Decision: a.Decision}
		if a.Key.Kind == Explicit {
			id := a.Key.Request
			receipt.RequestID = &id
		}
		r := Record{Session: session, Digest: hex.EncodeToString(a.Digest[:]), Attempt: attempt, Created: now, State: "dispatching", Receipt: receipt}
		d.Records[a.Key.scoped(d.Namespace)] = r
		out = result(a.Key.scoped(d.Namespace), r, true)
		return true, nil
	})
	if err != nil {
		out = Result{}
	}
	return
}

// Finalize only changes status/reason/backend; original identity and decision
// are immutable. On any error after the effect the caller reports unknown and
// must not retry delivery. A completed token cannot be finalized twice.
func (s *Store) Finalize(ctx context.Context, k Key, attempt, status, reason, backend string) error {
	return s.FinalizeOutcome(ctx, k, attempt, status, reason, backend, nil)
}

// FinalizeOutcome atomically records terminal navigation separately from the
// immutable admission. Nil preserves the legacy Finalize representation.
// This is draft unreleased v1 state: existing records remain readable, but old
// readers reject the optional field/new suppressed enum. Activation must fence
// older writers and rollback paths; never reset history or change namespace.
func (s *Store) FinalizeOutcome(ctx context.Context, k Key, attempt, status, reason, backend string, navigation *Navigation) error {
	if navigation != nil {
		n := *navigation
		navigation = &n
		if !validOutcomeNavigation(n) {
			return ErrInvalid
		}
	}
	if !k.valid() || !isHex(attempt) || !validTerminal(status) || !validText(reason, 128, true) || !validText(backend, 128, false) {
		return ErrInvalid
	}
	return s.transaction(ctx, func(d *disk) (bool, error) {
		id := k.scoped(d.Namespace)
		r, ok := d.Records[id]
		if !ok || r.Attempt != attempt || r.State != "dispatching" {
			return false, ErrCAS
		}
		if navigation != nil && !validNavigationTransition(r.Receipt.Decision.Navigation, *navigation) {
			return false, ErrInvalid
		}
		r.Receipt.OutcomeNavigation = navigation
		r.State = status
		r.Receipt.Status = status
		r.Receipt.Reason = reason
		r.Receipt.Backend = backend
		d.Records[id] = r
		return true, nil
	})
}

// Collect never affects OS notifications or callbacks. Unavailable/regressed
// clocks freeze GC. The logical clock baseline is persisted even when frozen.
func (s *Store) Collect(ctx context.Context) error {
	return s.transaction(ctx, func(d *disk) (bool, error) {
		proven, e := s.advance(d)
		if e != nil {
			return false, e
		}
		if proven {
			for k, r := range d.Records {
				if d.Clock.Logical-r.Created >= d.Limits.Retention {
					delete(d.Records, k)
				}
			}
		}
		return true, nil
	})
}
func validTerminal(s string) bool {
	return s == "submitted" || s == "rejected" || s == "unknown" || s == "suppressed"
}
func validOutcomeNavigation(n Navigation) bool {
	if !validText(n.Scope, 1024, false) || !validText(n.Reason, 1024, false) {
		return false
	}
	return (n.Capability == "available" && n.Precision == "chat_id" && n.Scope != "") || ((n.Capability == "unavailable" || n.Capability == "disabled") && n.Precision == "none")
}
func validNavigationTransition(before, after Navigation) bool {
	return after.Capability != "available" || (before.Capability == "available" && before.Precision == after.Precision && before.Scope == after.Scope)
}
func validSnapshot(s Snapshot) bool {
	for _, v := range []string{s.Target.Kind, s.Target.ID, s.Target.Application, s.Target.Identity, s.Policy, s.Navigation.Capability, s.Navigation.Precision, s.Navigation.Scope, s.Navigation.Reason} {
		if !validText(v, 1024, false) {
			return false
		}
	}
	return true
}
func (d *disk) validate(s *Store) error {
	if d.Version != 1 || !isHex(d.Namespace) || d.Limits != s.limits || d.Records == nil || d.Events == nil || len(d.Records) > d.Limits.Records || len(d.Events) > d.Limits.Records || !validText(d.Clock.Boot, 256, false) || (d.Clock.Boot == "" && d.Clock.Seconds != 0) {
		return ErrRepair
	}
	for _, v := range d.Events {
		if v.At > d.Clock.Logical || !isHex(v.Session) {
			return ErrRepair
		}
	}
	var prev uint64
	for _, v := range d.Events {
		if v.At < prev {
			return ErrRepair
		}
		prev = v.At
	}
	// Live rate events correspond one-for-one to retained admissions. Removing
	// counters while recent records remain is corruption, not a rate reset.
	type rateKey struct {
		at      uint64
		session string
	}
	counts := map[rateKey]int{}
	for _, v := range d.Events {
		if d.Clock.Logical-v.At < 60 {
			counts[rateKey{v.At, v.Session}]++
		}
	}
	attempts := map[string]bool{}
	for k, r := range d.Records {
		if attempts[r.Attempt] {
			return ErrRepair
		}
		attempts[r.Attempt] = true
		if r.Created <= d.Clock.Logical && d.Clock.Logical-r.Created < 60 {
			counts[rateKey{r.Created, r.Session}]--
		}
		p := r.Receipt
		if !isHex(r.Session) || !isHex(k) || !isHex(r.Digest) || !isHex(r.Attempt) || r.Created > d.Clock.Logical || !validKind(p.KeyKind) || !validText(p.TrackingID, 256, true) || !validSnapshot(p.Decision) || !validText(p.Reason, 128, true) || !validText(p.Backend, 128, false) {
			return ErrRepair
		}
		if p.OutcomeNavigation != nil && (!validOutcomeNavigation(*p.OutcomeNavigation) || !validNavigationTransition(p.Decision.Navigation, *p.OutcomeNavigation)) {
			return ErrRepair
		}
		if p.KeyKind == Explicit {
			if p.RequestID == nil || !validText(*p.RequestID, 256, true) {
				return ErrRepair
			}
		} else if p.RequestID != nil {
			return ErrRepair
		}
		if r.State == "dispatching" {
			if p.Status != "unknown" || p.Reason != "pending_submission" || p.Backend != "" || p.OutcomeNavigation != nil {
				return ErrRepair
			}
		} else if !validTerminal(r.State) || p.Status != r.State {
			return ErrRepair
		}
	}
	for _, n := range counts {
		if n != 0 {
			return ErrRepair
		}
	}
	// Policy is mutable and is not persisted in the v1 representation. Validate
	// structural budgets and the exact live event/record multiset above, never
	// today's quotas against yesterday's admissions. This is O(records+events).
	return nil
}
func wrap(e error) error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrRepair, e)
}
