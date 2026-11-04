package portablesetup

import (
	"fmt"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/pi"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/ports"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
)

// NewRegistry is the explicit Notifications composition of the supported
// native clients. The UAP SDK intentionally rejects a nil registry.
func NewRegistry() (*clients.Registry, error) {
	return clients.NewRegistry(claude.New(), codex.New(), pi.New())
}

// ClientID translates the portable registration vocabulary at the public engine boundary.
func ClientID(integration portable.Integration) string {
	if integration == portable.CopilotVSCode {
		return string(domain.ClientVSCode)
	}
	return string(integration)
}

// Integration translates public engine results back to portable registrations.
func Integration(clientID string) portable.Integration {
	if clientID == string(domain.ClientVSCode) {
		return portable.CopilotVSCode
	}
	return portable.Integration(clientID)
}

// Identity is the operator-selected existing-installer surface. BindingID and
// DataRoot are completed from the committed UAP plan, not guessed from HOME.
type Identity struct {
	InstallationID, ComponentID, Owner, ScopeRoot, ControlRoot, GlobalConfig, RuntimeRoot, Primary string
}

// UAPRoots are explicit UAP state locations. None are derived from cwd/HOME.
type UAPRoots struct {
	StateFile, LockFile, OperationsDir, PluginDataBase, ManagedRoot string
	HelperExecutable, HelperVersion                                 string
	ClaudeRunner                                                    ports.CommandRunner
	CodexRunner                                                     ports.CommandRunner
	RequireLiveProfiles                                             bool
}

// MaterializeRequest selects one client. Integration is never taken from clientInfo.
type MaterializeRequest struct {
	Identity            Identity
	LocalConfig         *vscode.LocalConfig
	Integration         portable.Integration
	ExpectedGeneration  uint64
	PackageRoot         string
	ClientConfigRoot    string
	ClientExecutable    string
	SourceRevision      string
	SourceDigest        string
	TreeDigest          string
	HelperDigest        string
	HelperVersion       string
	Discovery           Discovery
	OperationID         string
	HelperExecutable    string
	ExternalUninstalled bool
	// HandoffAction is the frozen host action (update or repair) for a
	// migration projection. Only RefreshProjection and RestoreMigrationProjection
	// may pass it to the handoff; their UAP operations remain distinct.
	HandoffAction              string
	restoreMigrationProjection bool
	// HoldOnly publishes the uninstall reservation and returns without locator
	// revoke or UAP mutation. Wizard uses it to keep a Codex removal pending
	// until the host attests ExternalUninstalled.
	HoldOnly bool
	// KeepReservation leaves the kernel pending mutation in place. Wizard
	// publishes one SetupIntent for the whole confirmed operation and clears
	// it after the last target, including when several clients share it.
	KeepReservation bool
	// Operation selects install, update, or repair. Empty means install.
	Operation uapinstaller.Operation
}

type Materializer struct {
	PiChoices piauthority.Choices
	// One Install invocation records whether the committed package callback ran.
	// An unchanged UAP package can still need an explicitly requested Pi choice.
	piHandoffDone *bool
	Kernel        Service
	Store         statev2.Store
	Roots         UAPRoots
	// Registry is the explicit composition for this materializer and every
	// engine it constructs. Nil retains the Claude/Codex composition.
	Registry *clients.Registry
	Progress func(client, phase, outcome string)
}

func NewMaterializer(roots UAPRoots) (Materializer, error) {
	for _, p := range []string{roots.StateFile, roots.LockFile, roots.OperationsDir, roots.PluginDataBase, roots.ManagedRoot, roots.HelperExecutable} {
		if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return Materializer{}, fmt.Errorf("%w: UAP roots must be explicit absolute paths", ErrPreflight)
		}
	}
	if roots.HelperVersion == "" {
		roots.HelperVersion = "agent-notify-portable-v1"
	}
	return Materializer{Kernel: Service{}, Store: statev2.Store{Path: roots.StateFile}, Roots: roots}, nil
}
