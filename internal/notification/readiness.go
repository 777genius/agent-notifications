package notification

import "context"

// Readiness is a read-only snapshot, with no retry/admission/OS acceptance claim.
// Only Status == "ready" permits a new journal admission. The caller captures
// Deadline before this call and reuses it for Deliver, without resetting it.
type Readiness struct {
	CorrelationID string
	Status        string // ready, rejected, suppressed
	Reason        string
	Backend       string
	Navigation    NavigationResult
}
type ReadinessPort interface {
	CheckReadiness(context.Context, Request) Readiness
}
