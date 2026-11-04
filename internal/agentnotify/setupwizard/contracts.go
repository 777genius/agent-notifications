package setupwizard

import (
	"context"
	"errors"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/ports"
)

var ErrRefused = errors.New("setup wizard refused")

var ErrAmbiguousInstallation = errors.New("ambiguous_installation")

var ErrAmbiguousBinding = errors.New("ambiguous_binding")

var ErrLiveProfileConflict = errors.New("live_profile_conflict")

type Action string

const (
	ActionInstall   Action = "install"
	ActionUninstall Action = "uninstall"
	ActionInspect   Action = "inspect"
	ActionUpdate    Action = "update"
	ActionRepair    Action = "repair"
)

// Request is copied by Run. Omitted unit flags are nil; install defaults both
// units on, uninstall omitted units selects every managed unit of the agents.
type Request struct {
	Action                                            Action
	Agents                                            []string
	Hooks, AgentNotify                                *bool
	Yes                                               bool
	PackageRoot, PluginRoot, ControlRoot, RuntimeRoot string
	// PackageRoots are per-agent local packages for mixed-revision Repair.
	// Run fills them from the offered root and still-usable recorded sources;
	// they are not CLI flags.
	PackageRoots map[string]string
	// CodexHome and ClaudeConfig are explicit UAP client profile roots.
	// EnvCodexHome and EnvClaudeConfig are a one-shot CLI snapshot of
	// CODEX_HOME / CLAUDE_CONFIG_DIR, not flags. Resume restores intent
	// profiles first; remaining empty roots take this snapshot. Run and
	// Plan do not reread the process environment.
	GlobalConfig, CodexHome, ClaudeConfig string
	PiAgentDir, EnvPiAgentDir             string
	PiAgentNotify                         *bool
	PiDesktop, PiWebhook                  *bool
	PiChannelPreimage                     string
	// Cursor selection is explicit, never filled from discovery or environment.
	// Fixed authority is provided by the qualified composition owner; it does
	// not replace physical capture, installed receipts, or channel consent.
	LocalConfig                         *vscode.LocalConfig
	CursorConfig                        string
	CursorAuthority                     *cursorinstall.Authority
	CursorAgentNotify                   *bool
	EnvCodexHome, EnvClaudeConfig       string
	ClientExecutable, ScopeRoot, Helper string
	ClientExecutables                   map[string]string
	PackageSHA256                       string
	// TreeDigest is the canonical package-tree digest from Prepare. It is
	// distinct from PackageSHA256 (archive bytes).
	TreeDigest, HelperDigest, HelperVersion string
	InstallationID, Primary                 string
	// BindingIDs are reserved per client during Plan/identity without
	// staging. Run and the durable intent reuse them.
	BindingIDs map[string]string
	// MigrationBindings freezes the exact historical and replacement consumer
	// identities before the confirmed intent is published. It is restored from
	// that intent on retry, never reconstructed from a changed host environment.
	MigrationBindings map[string]MigrationBinding
	// StateMigration is the read-only registry snapshot shown at confirmation.
	// Run refuses changed bytes instead of approving a different migration.
	StateMigration *uapinstaller.StateMigrationPlan
	// StateMigrationProgress reports the separate, completed registry upgrade.
	StateMigrationProgress func(StateMigrationOutcome)
	// RemovalBindings freezes the exact portable consumer that uninstall will
	// revoke. A retry restores it from the confirmed intent after kernel revoke.
	RemovalBindings map[string]RemovalBinding
	// DataReceiptIDs are known UAP PLUGIN_DATA receipts. Uninstall/publish
	// copies live receipts onto the durable intent; resume rejects a different ID.
	DataReceiptIDs                      map[string]string
	MCPConfig                           map[string]string
	ClaudeHooks, CodexHooks             *bool
	ClaudeAgentNotify, CodexAgentNotify *bool
	// ExternalUninstalled is host attestation that Codex already removed the
	// native plugin, or never activated it. --yes does not set this.
	ExternalUninstalled bool
	// BootstrapExpectedGeneration fences an automatic client selection made
	// before release acquisition. It is internal to the bootstrap orchestrator:
	// an intervening opt-out must not become an implicit Add.
	BootstrapExpectedGeneration *uint64
	// BootstrapExpectedPolicy is captured after the last relevant projection check.
	BootstrapExpectedPolicy *installruntime.Identity
	// BootstrapMCP is consumed by the command admission boundary. Plan/Run use
	// the verified generation, then existing reservation/config CAS protects apply.
	BootstrapMCP *BootstrapMCPSelection
	// BootstrapStateMigrationOwned is set only by admission of the confirmed
	// bootstrap handoff. Its parent owns registry migration even when a client
	// composition clears BootstrapMCP; direct /init generation fences do not.
	BootstrapStateMigrationOwned bool
	// BootstrapReadyClients only narrows effects after the complete immutable
	// bootstrap projection has been revalidated. A failed primary registration
	// must not prevent an independent, already confirmed client from activating.
	BootstrapReadyClients []string
	// ClaudeRunner overrides Claude activation probing. Production leaves it
	// nil so the OS process runner is used. Isolated tests inject a listing
	// fixture; the field is never parsed from CLI flags.
	ClaudeRunner ports.CommandRunner
	// ReleaseVersion is the accepted master revision without a leading v.
	// DefaultReleaseVersion is a one-shot CLI snapshot of this binary's
	// compiled consumer version, not an explicit flag. Resume restores
	// intent.SourceRevision first; remaining empty values take this snapshot.
	ReleaseVersion, DefaultReleaseVersion string
	// ReleaseDownloadRoot is the directory that contains v{version}/ assets.
	// Empty disables host fetch so tests that omit --package stay offline.
	ReleaseDownloadRoot string
	// PackageFetcher downloads one URL. Production uses HTTPS; tests inject
	// a local server. Never parsed from CLI flags.
	PackageFetcher func(context.Context, string) ([]byte, error)
	// Progress reports large confirmed phases to the host. JSON stdout stays
	// one result; the CLI writes these lines to stderr. Nil is silent.
	Progress func(string)
	// ClientProgress is presentation-only, finite per-client lifecycle activity.
	// No provider output, paths, or executable recovery commands flow through it.
	ClientProgress func(client, phase, outcome string)
	// DiscoverAgents supplies Claude/Codex executable presence for the TTY
	// picker. Production sets this from Engine.Discover. Nil skips presence
	// labels. The function must not execute found files.
	DiscoverAgents func() []AgentCapability
	// LiveUnits reports managed hooks/notify for the TTY mixed-opt-out
	// display. Nil uses LiveClientUnits from control-root state.
	LiveUnits func([]string) []ClientUnits
}

type TargetResult struct {
	// Lifecycle facts and the current external attempt are independent.
	Materialization string `json:"materialization,omitempty"`
	Activation      string `json:"activation,omitempty"`
	Verification    string `json:"verification,omitempty"`
	GroupPhase      string `json:"groupPhase,omitempty"`
	FailureStage    string `json:"failureStage,omitempty"`
	Client          string `json:"client"`
	Unit            string `json:"unit"`
	Outcome         string `json:"outcome"`
	Reason          string `json:"reason,omitempty"`
	Profile         string `json:"profile,omitempty"`
	TreeDigest      string `json:"treeDigest,omitempty"`
	// ConfigPath is the owned MCP file inspect used for a direct-mcp
	// target. Empty on hooks/notify rows so JSON omits it.
	ConfigPath string `json:"configPath,omitempty"`
	// Warnings are post-commit maintenance diagnostics, not installation failure.
	Warnings []string `json:"warnings,omitempty"`
}

// ReadinessFact is independent of binary download. Inspect and mutation both
// report these fields; not_checked/not_verified are not installation failure.
type ReadinessFact struct {
	Client     string `json:"client"`
	Runtime    string `json:"runtime"`
	Hooks      string `json:"hooks"`
	MCP        string `json:"mcp"`
	Permission string `json:"permission"`
	Restart    string `json:"restart"`
	Delivery   string `json:"delivery"`
}

type NextAction struct {
	Kind    string   `json:"kind"`
	Agents  []string `json:"agents,omitempty"`
	Command []string `json:"command,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

type Result struct {
	StateMigration *StateMigrationOutcome `json:"stateMigration,omitempty"`
	Action         string                 `json:"action"`
	Outcome        string                 `json:"outcome"`
	Reason         string                 `json:"reason,omitempty"`
	InstallationID string                 `json:"installationID,omitempty"`
	Generation     uint64                 `json:"generation,omitempty"`
	Command        []string               `json:"command,omitempty"`
	Targets        []TargetResult         `json:"targets,omitempty"`
	Readiness      []ReadinessFact        `json:"readiness,omitempty"`
	NextActions    []NextAction           `json:"nextActions,omitempty"`
	// DataRetained is true after the last live binding is removed while
	// PLUGIN_DATA remains. Absent inspect rows are not a license to run.
	DataRetained bool `json:"dataRetained,omitempty"`

	// reservation belongs to this invocation, never a later ledger snapshot.
	reservation *installruntime.PendingMutation
}

func (r Result) ExitCode() int {
	// Read-only inspect reports readiness in result fields. A successful
	// report is exit 0 even when Outcome is incomplete (§9.2).
	if r.Action == string(ActionInspect) && r.Outcome != "invalid" {
		return 0
	}
	switch r.Outcome {
	case "completed", "unchanged":
		return 0
	case "cancelled":
		return 0
	case "invalid":
		return 2
	default:
		return 1
	}
}
