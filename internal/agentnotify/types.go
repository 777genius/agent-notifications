// Package agentnotify orchestrates explicit notifications over injected ports.
// Composition owns opening the journal and delivery resources; constructors and
// replay never initialize, repair, collect, load configuration or probe the OS.
package agentnotify

import (
	"context"
	"errors"
	"reflect"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

type Payload struct {
	Title      string                  `json:"title"`
	Body       string                  `json:"body"`
	Category   string                  `json:"category"`
	RequestID  *string                 `json:"request_id"`
	Navigation notification.Navigation `json:"navigation"`
}

type Receipt struct {
	RequestID  *string                       `json:"request_id"`
	TrackingID string                        `json:"tracking_id"`
	KeyKind    journal.KeyKind               `json:"key_kind"`
	Status     string                        `json:"status"`
	Reason     string                        `json:"reason"`
	Replayed   bool                          `json:"replayed"`
	Backend    string                        `json:"backend"`
	Navigation notification.NavigationResult `json:"navigation"`
	RetrySafe  bool                          `json:"retry_safe"`
}

// AdmissionPort is configured by composition. Rates/capacity are atomic journal
// responsibilities, never a second service limiter. Each admission carries the
// validated authoritative policy snapshot for that request. FinalizeOutcome is
// required so adapters cannot silently discard terminal navigation; Store.Finalize
// remains available to legacy journal callers.
type AdmissionPort interface {
	Lookup(context.Context, journal.Key, journal.Digest) (journal.Result, error)
	Admit(context.Context, journal.Admission) (journal.Result, error)
	FinalizeOutcome(context.Context, journal.Key, string, string, string, string, *journal.Navigation) error
}

// Policy is an immutable per-call value. The fixed delivery DTO has no policy
// generation field: composition must integrate authoritative generation fencing
// in Delivery before activation; readiness alone cannot fence disable/uninstall.
type Policy struct {
	Rates    journal.RatePolicy
	Delivery notification.PolicySnapshot
	Route    origin.RoutePolicy
}
type PolicyPort interface {
	Load(context.Context, origin.Context) (Policy, error)
}
type PolicyFunc func(context.Context, origin.Context) (Policy, error)

func (f PolicyFunc) Load(c context.Context, o origin.Context) (Policy, error) { return f(c, o) }

type TargetPort interface {
	Resolve(context.Context, origin.Context, origin.RoutePolicy) (origin.Target, error)
}
type TargetFunc func(context.Context, origin.Context, origin.RoutePolicy) (origin.Target, error)

func (f TargetFunc) Resolve(c context.Context, o origin.Context, p origin.RoutePolicy) (origin.Target, error) {
	return f(c, o, p)
}

// Clock supplies qualified boot-continuous seconds (including suspend).
// Unavailable/changed epochs fail closed. Implementations must return promptly.
type Clock interface{ Now() notification.Deadline }
type ClockFunc func() notification.Deadline

func (f ClockFunc) Now() notification.Deadline { return f() }

type Dependencies struct {
	Admission AdmissionPort
	Policy    PolicyPort
	Target    TargetPort
	Readiness notification.ReadinessPort
	Delivery  notification.DeliveryPort
	Clock     Clock
}

// ErrConfigurationRequired marks absent operator configuration without leaking paths.
var ErrConfigurationRequired = errors.New("configuration_required")

var ErrDependencies = errors.New("invalid_dependencies")

func nilPort(x any) bool {
	if x == nil {
		return true
	}
	v := reflect.ValueOf(x)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func validDependencies(d Dependencies) bool {
	for _, p := range []any{d.Admission, d.Policy, d.Target, d.Readiness, d.Delivery, d.Clock} {
		if nilPort(p) {
			return false
		}
	}
	return true
}
