// Package notification defines the agent-independent delivery boundary.
// Validation/policy loading, origin mapping and durable admission belong to callers.
package notification

import "context"

type Content struct {
	Title    string
	Body     string
	Subtitle string
	Category string // info, attention, progress; never an analyzer status
}

type Navigation string

const (
	Required   Navigation = "required"
	BestEffort Navigation = "best_effort"
	None       Navigation = "none"
)

// DesktopTarget is supplied by a trusted integration and setup adapter, never
// decoded from model arguments. App identity is operator-pinned, not inferred
// from cwd, metadata text, or whichever app happens to be foreground.
type DesktopTarget struct {
	ThreadID        string
	ApplicationPath string
	TeamID          string
}

// Deadline is captured at transport admission, before validation or locks.
// Seconds are boot-continuous seconds including suspend, not Unix wall time.
type Deadline struct {
	BootID   string
	NotAfter float64
}

// PolicySnapshot is a value copy of the initial strict policy decision. The
// delivery adapter never reloads legacy defaults or mutates caller policy.
type PolicySnapshot struct {
	Valid           bool
	ExplicitEnabled bool
	DesktopEnabled  bool
	ClickToFocus    bool
	SoundEnabled    bool
}

type Request struct {
	Content       Content
	CorrelationID string // UUID assigned by the durable admission owner
	Deadline      Deadline
	Policy        PolicySnapshot
	Navigation    Navigation // empty defaults to Required
	Target        DesktopTarget
	Silent        bool
}

type NavigationResult struct {
	Capability string // available, unavailable, disabled
	Precision  string // chat_id or none
	Scope      string
	Reason     string
}

type Receipt struct {
	CorrelationID string
	Status        string // rejected, suppressed, submitted, unknown
	Reason        string
	RetrySafe     bool // always false here: delivery does not own replay admission
	Backend       string
	Navigation    NavigationResult
}

type DeliveryPort interface {
	Deliver(context.Context, Request) Receipt
}
