package agentnotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

type Service struct {
	deps    Dependencies
	mu      sync.Mutex
	closed  bool
	active  int
	drained chan struct{}
}

func New(d Dependencies) (*Service, error) {
	if !validDependencies(d) {
		return nil, ErrDependencies
	}
	return &Service{deps: d, drained: make(chan struct{})}, nil
}

// Close prevents new submissions and drains admitted/preflight work. It is
// idempotent, does not cancel an accepted OS send, and never closes injected
// resources. A future transport owns those resources once per service lifetime.
func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.drained == nil {
		return ErrDependencies
	}
	if ctx == nil {
		return errors.New("invalid_context")
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.active == 0 {
			close(s.drained)
		}
	}
	done := s.drained
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) enter() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "service_closed"
	}
	if s.active >= 2 {
		return "busy"
	}
	s.active++
	return ""
}
func (s *Service) leave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.closed && s.active == 0 {
		close(s.drained)
	}
}

// remaining never manufactures a fresh transport budget. The transport MUST
// capture this deadline before decoding/validation. Decoded fields have a 16KiB
// budget; raw JSON framing limits belong to the transport (MCP: 64KiB).
func (s *Service) remaining(ctx context.Context, d notification.Deadline) (time.Duration, string) {
	if ctx == nil {
		return 0, "invalid_context"
	}
	if ctx.Err() != nil {
		return 0, "canceled"
	}
	now := s.deps.Clock.Now()
	if !origin.Text(d.BootID, 256, true) || now.BootID != d.BootID || math.IsNaN(d.NotAfter) || math.IsInf(d.NotAfter, 0) || math.IsNaN(now.NotAfter) || math.IsInf(now.NotAfter, 0) || now.NotAfter < 0 {
		return 0, "invalid_deadline"
	}
	delta := d.NotAfter - now.NotAfter
	if delta <= 0 {
		return 0, "deadline_exceeded"
	}
	if delta > 15 {
		return 0, "invalid_deadline"
	}
	return time.Duration(delta * float64(time.Second)), ""
}

// Notify accepts typed payload and a separately constructed origin. No future,
// goroutine, queue or retry is created; delivery is invoked at most once after
// durable admission. Ports must honor context cancellation and the boot deadline.
func (s *Service) Notify(ctx context.Context, p Payload, o origin.Context, deadline notification.Deadline) Receipt {
	out := Receipt{Status: "rejected", RetrySafe: true, Navigation: notification.NavigationResult{Capability: "unavailable", Precision: "none"}}
	if p.RequestID != nil {
		id := *p.RequestID
		p.RequestID = &id
		out.RequestID = &id
	}
	if s == nil || !validDependencies(s.deps) {
		out.Reason = "invalid_dependencies"
		return out
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		out.Reason = "service_closed"
		return out
	}
	budget, reason := s.remaining(ctx, deadline)
	if reason != "" {
		out.Reason = reason
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	p, reason = validate(p, o)
	if reason != "" {
		out.Reason = reason
		return out
	}
	tracking, e := trackingID()
	if e != nil {
		out.Reason = "entropy_unavailable"
		return out
	}
	out.TrackingID = tracking
	source, session := o.Scope()
	key := journal.Key{Source: source, Session: session, Kind: journal.Generated, Request: tracking}
	if p.RequestID != nil {
		key.Kind = journal.Explicit
		key.Request = *p.RequestID
	} else if o.CallID != "" {
		key.Kind = journal.ClientCall
		key.Request = o.CallID
	}
	out.KeyKind = key.Kind
	dg := digest(p)
	found, e := s.deps.Admission.Lookup(ctx, key, dg)
	if e != nil {
		return journalFailure(out, e)
	}
	if found.Found {
		return replay(found)
	}
	if reason = s.enter(); reason != "" {
		out.Reason = reason
		return out
	}
	defer s.leave()
	if _, reason = s.remaining(ctx, deadline); reason != "" {
		out.Reason = reason
		return out
	}
	policy, e := s.deps.Policy.Load(ctx, o)
	if e != nil || !policy.Delivery.Valid {
		out.Reason = "configuration_invalid"
		if errors.Is(e, ErrConfigurationRequired) {
			out.Reason = "configuration_required"
		}
		return out
	}
	policy.Rates, e = policy.Rates.Normalize()
	if e != nil {
		out.Reason = "configuration_invalid"
		return out
	}
	if !policy.Delivery.ExplicitEnabled || !policy.Delivery.DesktopEnabled {
		out.Status = "suppressed"
		out.Reason = "disabled"
		return out
	}
	if o.Locality == origin.Remote || o.Interface == origin.Headless {
		out.Reason = "local_gui_unavailable"
		return out
	}
	if ((o.Interface == origin.InterfaceUnknown || o.Locality == origin.LocalityUnknown) && ((p.Navigation != notification.None && !policy.Route.LocalRouting) || !policy.Route.AllowUnknownCaller)) || (o.Provenance == origin.CallerAsserted && ((p.Navigation != notification.None && !policy.Route.LocalRouting) || !policy.Route.AllowCallerAsserted)) {
		out.Reason = "local_routing_unavailable"
		return out
	}
	target := origin.Target{Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "navigation_none"}}
	if p.Navigation != notification.None {
		if !policy.Delivery.ClickToFocus {
			target.Navigation = notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "click_to_focus_disabled"}
		} else if o.Hidden {
			target.Navigation = notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "hidden_target"}
		} else if !policy.Route.LocalRouting || ((o.Interface == origin.InterfaceUnknown || o.Locality == origin.LocalityUnknown) && !policy.Route.AllowUnknownCaller) || (o.Provenance == origin.CallerAsserted && !policy.Route.AllowCallerAsserted) {
			target.Navigation = notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "local_routing_unavailable"}
		} else {
			target, e = s.deps.Target.Resolve(ctx, o, policy.Route)
			if e != nil {
				out.Reason = "target_unavailable"
				return out
			}
		}
		if !validTarget(target) {
			out.Reason = "invalid_target"
			return out
		}
		if target.Navigation.Capability != "available" {
			target.Desktop = notification.DesktopTarget{}
		}
		if p.Navigation == notification.Required && target.Navigation.Capability != "available" {
			out.Reason = "navigation_unavailable"
			out.Navigation = target.Navigation
			return out
		}
	}
	req := notification.Request{CorrelationID: correlationID("preflight", tracking), Content: notification.Content{Title: p.Title, Body: p.Body, Category: p.Category}, Deadline: deadline, Policy: policy.Delivery, Navigation: p.Navigation, Target: target.Desktop, Silent: !policy.Delivery.SoundEnabled}
	if _, reason = s.remaining(ctx, deadline); reason != "" {
		out.Reason = reason
		return out
	}
	// CheckReadiness must have released its installation lease when it returns.
	// Admission and Deliver therefore never nest component and journal locks.
	ready := s.deps.Readiness.CheckReadiness(ctx, req)
	out.Navigation = target.Navigation
	qualified, valid := reconcileNavigation(target.Navigation, ready.Navigation)
	if ready.CorrelationID != req.CorrelationID || !valid || !origin.Text(ready.Reason, 128, true) || !origin.Text(ready.Backend, 128, false) || (ready.Status != "ready" && ready.Status != "rejected" && ready.Status != "suppressed") {
		out.Reason = "invalid_readiness_receipt"
		return out
	}
	out.Navigation = qualified
	if ready.Status != "ready" {
		out.Reason = "readiness_unavailable"
		if origin.Text(ready.Reason, 128, true) {
			out.Reason = ready.Reason
		}
		if ready.Status == "suppressed" {
			out.Status = "suppressed"
		}
		return out
	}
	if _, reason = s.remaining(ctx, deadline); reason != "" {
		out.Reason = reason
		return out
	}
	if p.Navigation == notification.Required && qualified.Capability != "available" {
		out.Reason = "navigation_unavailable"
		return out
	}
	decision := snapshot(policy, target)
	decision.Navigation = journal.Navigation(qualified)
	if qualified.Capability != "available" {
		req.Target = notification.DesktopTarget{}
	}

	admitted, e := s.deps.Admission.Admit(ctx, journal.Admission{Key: key, Digest: dg, TrackingID: tracking, Decision: decision, Rates: policy.Rates})
	if e != nil {
		out = journalFailure(out, e)
		out.RetrySafe = false
		return out
	}
	if !admitted.Fresh {
		if admitted.Found {
			return replay(admitted)
		}
		out.Reason = "journal_unavailable"
		out.RetrySafe = false
		return out
	}
	out = fromJournal(admitted.Record.Receipt, false)
	// Durable pending cannot be undone by cancellation. Skipping handoff here is
	// conservative: the stored pending receipt is never automatically resent.
	if _, reason = s.remaining(ctx, deadline); reason != "" {
		return out
	}
	req.CorrelationID = correlationID(admitted.ScopedKey, admitted.Record.Attempt)
	delivered := s.deps.Delivery.Deliver(ctx, req)
	status, why, backend := delivered.Status, delivered.Reason, delivered.Backend
	finalNavigation, valid := reconcileNavigation(qualified, delivered.Navigation)
	if delivered.CorrelationID != req.CorrelationID || (status != "submitted" && status != "rejected" && status != "unknown" && status != "suppressed") || !valid || !origin.Text(why, 128, true) || !origin.Text(backend, 128, false) {
		status, why, backend = "unknown", "invalid_delivery_receipt", ""
		finalNavigation = notification.NavigationResult{Capability: "unavailable", Precision: "none", Reason: "invalid_delivery_receipt"}
	}
	// Finalization has a separate bounded durability budget, never a new send
	// budget. Positive OS acceptance survives late caller cancellation.
	finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer finalCancel()
	if e = s.deps.Admission.FinalizeOutcome(finalCtx, key, admitted.Record.Attempt, status, why, backend, (*journal.Navigation)(&finalNavigation)); e != nil {
		out.Status = "unknown"
		out.Reason = "finalization_failed"
		return out
	}
	out.Status = status
	out.Reason = why
	out.Backend = backend
	out.Navigation = finalNavigation
	return out
}

// Ports may qualify or reduce a resolved capability, never create a new action.
func reconcileNavigation(before, after notification.NavigationResult) (notification.NavigationResult, bool) {
	if !origin.Text(after.Scope, 1024, false) || !origin.Text(after.Reason, 1024, false) {
		return after, false
	}
	switch after.Capability {
	case "available":
		return after, after.Precision == "chat_id" && after.Scope != "" && before.Capability == "available" && before.Precision == after.Precision && before.Scope == after.Scope
	case "unavailable", "disabled":
		return after, after.Precision == "none"
	default:
		return after, false
	}
}
func validTarget(t origin.Target) bool {
	n := t.Navigation
	if n.Capability != "available" && n.Capability != "unavailable" && n.Capability != "disabled" {
		return false
	}
	for _, v := range []string{n.Scope, n.Reason} {
		if !origin.Text(v, 1024, false) {
			return false
		}
	}
	if n.Capability != "available" {
		return n.Precision == "none"
	}
	return n.Scope != "" && n.Precision == "chat_id" && origin.Text(t.Desktop.ThreadID, 256, true) && origin.Text(t.Desktop.ApplicationPath, 1024, true) && origin.Text(t.Desktop.TeamID, 256, true)
}
func snapshot(p Policy, t origin.Target) journal.Snapshot {
	// Only booleans describing the admitted policy, never raw config/metadata.
	b, _ := json.Marshal(struct {
		Delivery notification.PolicySnapshot
		Rates    journal.RatePolicy
	}{p.Delivery, p.Rates})
	kind := "none"
	if t.Navigation.Capability == "available" {
		kind = "desktop_thread"
	}
	return journal.Snapshot{Target: journal.Target{Kind: kind, ID: t.Desktop.ThreadID, Application: t.Desktop.ApplicationPath, Identity: t.Desktop.TeamID}, Policy: string(b), Navigation: journal.Navigation{Capability: t.Navigation.Capability, Precision: t.Navigation.Precision, Scope: t.Navigation.Scope, Reason: t.Navigation.Reason}}
}
func trackingID() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}

// correlationID uses the UUIDv8 custom layout for a SHA-256-derived identity.
func correlationID(scoped, attempt string) string {
	b := sha256.Sum256([]byte("agent-notify/native/v1\x00" + scoped + "\x00" + attempt))
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func replay(r journal.Result) Receipt { return fromJournal(r.Record.Receipt, true) }
func fromJournal(r journal.Receipt, replayed bool) Receipt {
	n := r.Decision.Navigation
	if r.OutcomeNavigation != nil {
		n = *r.OutcomeNavigation
	}
	var id *string
	if r.RequestID != nil {
		v := *r.RequestID
		id = &v
	}
	return Receipt{RequestID: id, TrackingID: r.TrackingID, KeyKind: r.KeyKind, Status: r.Status, Reason: r.Reason, Replayed: replayed, Backend: r.Backend, Navigation: notification.NavigationResult{Capability: n.Capability, Precision: n.Precision, Scope: n.Scope, Reason: n.Reason}, RetrySafe: false}
}
func journalFailure(out Receipt, e error) Receipt {
	out.Reason = "journal_unavailable"
	switch {
	case errors.Is(e, journal.ErrConflict):
		out.Reason = "idempotency_conflict"
		out.RetrySafe = false
	case errors.Is(e, journal.ErrRate):
		out.Status = "suppressed"
		out.Reason = "rate_limited"
	case errors.Is(e, journal.ErrFull):
		out.Reason = "journal_full"
	case errors.Is(e, journal.ErrRepair):
		out.Reason = "state_repair_required"
	case errors.Is(e, context.Canceled):
		out.Reason = "canceled"
	case errors.Is(e, context.DeadlineExceeded):
		out.Reason = "deadline_exceeded"
	}
	return out
}
