package uapinstaller

// Operation is the process-local lifecycle verb. P1 publishes install and remove.
// Update and repair are rejected until those checkpoints exist.
type Operation string

const (
	OpInstall Operation = "install"
	OpRemove  Operation = "remove"
	OpUpdate  Operation = "update"
	OpRepair  Operation = "repair"
)

// Outcome is the coarse public result. Mapping is not a bool.
type Outcome string

const (
	OutcomeUnchanged  Outcome = "unchanged"
	OutcomeCompleted  Outcome = "completed"
	OutcomeIncomplete Outcome = "incomplete"
	OutcomeRecovery   Outcome = "recovery_required"
	OutcomeConflict   Outcome = "conflict"
	OutcomeCancelled  Outcome = "cancelled"
)

// Request is copied by Prepare. Subsequent caller edits do not change the handle.
type Request struct {
	Operation          Operation
	PackageRoot        string
	ClientID           string
	ClientConfigRoot   string
	ClientExecutable   string
	InstallationID     string
	OperationID        string
	Selector           string
	RequiredComponents []string
	// ExternalUninstalled is host attestation that the selected client's
	// native plugin was already removed, or was never activated. Confirmed
	// Apply does not invent this fact.
	ExternalUninstalled bool
}

// Decision is host UI confirmation, outside mutation locks.
type Decision struct {
	Confirmed bool
}

// BindingFacts is the typed committed-binding view for host seams.
type BindingFacts struct {
	InstallationID, ClientID, BindingID, Scope string
	TargetPath, DataRoot, DataReceiptID        string
	OperationID, TreeDigest                    string
}

// Plan is an immutable copy for presentation. Operational paths are included
// because the embedding host already chose explicit roots.
type Plan struct {
	Operation       Operation
	SourceRoot      string
	TreeDigest      string
	DigestAlgorithm string
	ClientID        string
	ConfigRoot      string
	TargetPath      string
	InstallationID  string
	BindingID       string
	HelperVersion   string
	HelperDigest    string
	RequiredMissing []string
	NoChange        bool
}

// Result is returned together with an error when part of the work already happened.
type Result struct {
	Operation      Operation
	InstallationID string
	Outcome        Outcome
	Binding        BindingFacts
	ManualActions  []string
	Reason         string
	NoChange       bool
	DataRetained   bool
	Client         ClientResult
	NextActions    []NextAction
	// Recovery classifies observed receipts after Recover. Apply leaves it empty.
	Recovery RecoveryReport
}

// RecoveryReport is the §5.8 resolved/remaining/unknown receipt view.
// It is populated even when Recover returns an error.
type RecoveryReport struct {
	Resolved  []PendingReceipt
	Remaining []PendingReceipt
	Unknown   []PendingReceipt
}

// NextAction is a structured follow-up. It is not a bool and not a retry token.
type NextAction struct {
	Kind   string
	Reason string
}

// ClientResult is the public per-client lifecycle view. Mapping is not a bool.
type ClientResult struct {
	ClientID, BindingID                                       string
	Materialization, Activation, Authentication, Verification string
	RequiredComponents                                        []string
}

// Assessment is a digest-bound content verdict. It is not a filesystem plan.
type Assessment struct {
	TreeDigest string
	Outcome    AssessmentOutcome
	Reason     string
}

// AssessmentOutcome is the host-visible scanner verdict.
type AssessmentOutcome string

const (
	AssessmentAllow       AssessmentOutcome = "allow"
	AssessmentBlock       AssessmentOutcome = "block"
	AssessmentUnavailable AssessmentOutcome = "unavailable"
)

// ProgressPhase is a coarse installer phase. Percent complete is not invented.
type ProgressPhase string

const (
	ProgressPrepare   ProgressPhase = "prepare"
	ProgressPreflight ProgressPhase = "preflight"
	ProgressStage     ProgressPhase = "stage"
	ProgressCommit    ProgressPhase = "commit"
	ProgressActivate  ProgressPhase = "activate"
	ProgressVerify    ProgressPhase = "verify"
	ProgressComplete  ProgressPhase = "complete"
)

// ProgressEvent is an observational checkpoint. The observer does not decide.
type ProgressEvent struct {
	Phase ProgressPhase
}

// Inspection is a read-only view of owned UAP state. Recovery facts are
// limited identities, not raw JSON and not an executable plan.
type Inspection struct {
	StateRoot     string
	Installations []InspectedInstallation
	Recovery      RecoveryObservation
}

// RecoveryObservation is the §5.8 read-only pending-transaction view.
type RecoveryObservation struct {
	Required bool
	Journals []PendingJournal
	Receipts []PendingReceipt
	Reason   string
}

// PendingJournal is one open directory-swap journal.
type PendingJournal struct {
	OperationID, Digest, BindingID, InstallationID, TargetPath, Phase string
}

// PendingReceipt is an unfinished state receipt, including state_committed
// after the matching journal was already removed.
type PendingReceipt struct {
	OperationID, BindingID, InstallationID, TargetPath, Phase string
	JournalPresent                                            bool
}

// InspectedInstallation is a public subset of one UAP installation.
type InspectedInstallation struct {
	InstallationID string
	TreeDigest     string
	Bindings       []InspectedBinding
	DataRetained   bool
	DataRoots      []string
}

// InspectedBinding is a public subset of one client binding.
type InspectedBinding struct {
	ClientID, BindingID, Scope, TargetPath, DataRoot, Profile string
	Materialization, Activation, Authentication, Verification string
}

// ClientMetadata is read-only provider surface. Discover does not execute files.
type ClientMetadata struct {
	ClientID          string
	Scopes            []string
	ExecutablePresent bool
	ExecutablePath    string
	Bindings          []InspectedBinding
}
