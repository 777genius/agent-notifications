// Package portablesetup binds a UAP materialization to the existing-installer
// kernel and private locator. It does not invent client identity from HOME/cwd.
package portablesetup

import (
	"context"
	"errors"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

var (
	ErrPreflight           = errors.New("portable setup refused")
	ErrUpdateRequired      = errors.New("existing clients require explicit update before add")
	ErrIntentConflict      = errors.New("pending setup intent conflict")
	ErrConcurrentChange    = errors.New("managed installation changed concurrently")
	ErrExternalUninstall   = errors.New("external uninstall required")
	ErrAlreadyAbsent       = errors.New("portable binding is already absent")
	ErrSourceIdentityDrift = errors.New("confirmed source identity drifted")
)

// Envelope is the root MCP command projected for one explicit integration.
type Envelope struct {
	Command string
	Args    []string
	Cwd     string
}

// Receipt is the committed UAP binding/data identity. Tests may supply a
// synthetic listing runner; that is not installed-client activation evidence.
type Receipt struct {
	BindingID, DataReceiptID, DataRoot string
	LocatorArg                         string
}

type Stager interface {
	Stage(ctx context.Context, env Envelope) (Receipt, error)
}
type Activator interface {
	Activate(ctx context.Context, receipt Receipt) error
}
type Remover interface {
	Remove(ctx context.Context, bindingID string) error
}

// Discovery names the exact owned global MCP/user-skill surface to retire
// before portable projection. Empty ConfigPath skips handoff. Unowned
// conflicts refuse before any portable mutation.
type Discovery struct {
	ConfigPath string
	Command    string
	Skill      *clientsetup.SkillProjection
}

type Request struct {
	Binding            portable.Binding
	ExpectedGeneration uint64
	DataReceiptID      string
	IntentTargets      []IntentTarget
	Envelope           Envelope
	Discovery          Discovery
	Reservation        *installruntime.PendingMutation
	SourceRevision     string
	SourceDigest       string
	TreeDigest         string
	HelperDigest       string
	HelperVersion      string
	// Profile is the resolved client config root for this target. Empty keeps
	// the published handoff intent without a profile path.
	Profile string
}

type Service struct {
	// ExpectedPolicy belongs to one verified bootstrap admission, including all
	// coordinated handoff and binding commits. Nil preserves legacy callers.
	ExpectedPolicy *installruntime.Identity
	Stager         Stager
	Activator      Activator
	Remover        Remover
}
