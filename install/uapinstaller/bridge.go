// Package uapinstaller is a compatibility import path for Notifications.
// The installer implementation is owned by the UAP SDK; this package only
// re-exports its public contract while retaining the historical sidecar name.
package uapinstaller

import (
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

type (
	Operation             = installer.Operation
	Request               = installer.Request
	ClientTarget          = installer.ClientTarget
	TargetFacts           = installer.TargetFacts
	Decision              = installer.Decision
	BindingFacts          = installer.BindingFacts
	Plan                  = installer.Plan
	PlanTarget            = installer.PlanTarget
	Result                = installer.Result
	RecoveryReport        = installer.RecoveryReport
	NextAction            = installer.NextAction
	ClientResult          = installer.ClientResult
	Assessment            = installer.Assessment
	AssessmentOutcome     = installer.AssessmentOutcome
	ProgressPhase         = installer.ProgressPhase
	ProgressEvent         = installer.ProgressEvent
	Inspection            = installer.Inspection
	RecoveryObservation   = installer.RecoveryObservation
	PendingJournal        = installer.PendingJournal
	PendingReceipt        = installer.PendingReceipt
	InspectedInstallation = installer.InspectedInstallation
	InspectedBinding      = installer.InspectedBinding
	ClientMetadata        = installer.ClientMetadata
	Config                = installer.Config
	Engine                = installer.Engine
	PreparedOperation     = installer.PreparedOperation
	IdentityRequest       = installer.IdentityRequest
	IdentityReservation   = installer.IdentityReservation
)

const (
	OpInstall             = installer.OpInstall
	OpRemove              = installer.OpRemove
	OpUpdate              = installer.OpUpdate
	OpRepair              = installer.OpRepair
	OutcomeUnchanged      = installer.OutcomeUnchanged
	OutcomeCompleted      = installer.OutcomeCompleted
	OutcomeIncomplete     = installer.OutcomeIncomplete
	OutcomeRecovery       = installer.OutcomeRecovery
	OutcomeConflict       = installer.OutcomeConflict
	OutcomeCancelled      = installer.OutcomeCancelled
	AssessmentAllow       = installer.AssessmentAllow
	AssessmentBlock       = installer.AssessmentBlock
	AssessmentUnavailable = installer.AssessmentUnavailable
	ProgressPrepare       = installer.ProgressPrepare
	ProgressPreflight     = installer.ProgressPreflight
	ProgressStage         = installer.ProgressStage
	ProgressCommit        = installer.ProgressCommit
	ProgressActivate      = installer.ProgressActivate
	ProgressVerify        = installer.ProgressVerify
	ProgressComplete      = installer.ProgressComplete
	LiveProfilesFile      = "live-profiles.json"
)

var (
	ErrInvalidConfig            = installer.ErrInvalidConfig
	ErrUnsupported              = installer.ErrUnsupported
	ErrInvalidHandle            = installer.ErrInvalidHandle
	ErrHandleClosed             = installer.ErrHandleClosed
	ErrHandleBusy               = installer.ErrHandleBusy
	ErrAlreadyApplied           = installer.ErrAlreadyApplied
	ErrCancelled                = installer.ErrCancelled
	ErrRecoveryRequired         = installer.ErrRecoveryRequired
	ErrPlanChanged              = installer.ErrPlanChanged
	ErrInvalidRequest           = installer.ErrInvalidRequest
	ErrAmbiguousInstallations   = installer.ErrAmbiguousInstallations
	ErrIncomplete               = installer.ErrIncomplete
	ErrUpdateRequired           = installer.ErrUpdateRequired
	ErrNotInstalled             = installer.ErrNotInstalled
	ErrAssessmentRejected       = installer.ErrAssessmentRejected
	ErrCompatibilityUnavailable = installer.ErrCompatibilityUnavailable
	ErrTargetFactsUnavailable   = installer.ErrTargetFactsUnavailable
)

func New(cfg Config) (*Engine, error) {
	// Notifications' historical compatibility path did not expose registry
	// composition. Keep that source-compatible surface while all new host
	// composition passes an explicit registry to the UAP SDK.
	if cfg.Registry == nil {
		registry, err := clients.NewRegistry(claude.New(), codex.New())
		if err != nil {
			return nil, err
		}
		cfg.Registry = registry
	}
	if !cfg.TrustedLocalPackages && cfg.Assess == nil {
		cfg.TrustedLocalPackages = true
	}
	return installer.New(cfg)
}
