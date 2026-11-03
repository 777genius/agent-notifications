package portablesetup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const (
	portableServerName = "agent-notifications"
	// legacyPortableServerName is the pre-rename server key still declared by
	// packages and projections made by earlier releases.
	legacyPortableServerName = "agent-notify"
)

// declaredServerName reports the MCP server key the package actually declares
// so projection selects the right entry. Packages predating the server rename
// declare only the legacy key.
func declaredServerName(packageRoot string) string {
	data, err := os.ReadFile(filepath.Join(packageRoot, "mcp.json"))
	if err != nil {
		return portableServerName
	}
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &document) != nil {
		return portableServerName
	}
	_, legacy := document.Servers[legacyPortableServerName]
	_, canonical := document.Servers[portableServerName]
	if legacy && !canonical {
		return legacyPortableServerName
	}
	return portableServerName
}

// composeLocalPackage supplies binding-specific canonical bytes only in an
// owned private snapshot. The shared release tree is never modified. Prepare
// computes the resulting identity; the host freezes it in the existing intent
// TreeDigest and verifies it again on retry.
func (m Materializer) composeLocalPackage(ctx context.Context, req MaterializeRequest, retain bool) (Materializer, MaterializeRequest, func(), error) {
	noop := func() {}
	if req.Integration != portable.CopilotVSCode || req.LocalConfig == nil {
		return m, req, noop, nil
	}

	canonicalStop := req.LocalConfig.NativeStop
	if !canonicalStop {
		state, err := m.Store.Load()
		if err != nil {
			return m, req, noop, err
		}
		if installation, ok := findInstallation(state, req.Identity.InstallationID); ok {
			for _, binding := range installation.Clients {
				if binding.ClientID == ClientID(req.Integration) {
					facts, local := binding.SelectedDelivery.LocalFacts()
					canonicalStop = local && facts.NativeStop
					if local && !canonicalStop {
						_, e := os.Stat(filepath.Join(installation.Source.CanonicalSource, filepath.FromSlash(vscodelocalhooks.PluginPath)))
						canonicalStop = e == nil
					}
				}
			}
		}
	}
	if !canonicalStop {
		return m, req, noop, nil
	}
	eng, err := m.engine(req, new(uint64), nil)
	if err != nil {
		return m, req, noop, err
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: ClientID(req.Integration), InstallationID: req.Identity.InstallationID,
		ClientConfigRoot: req.ClientConfigRoot, DeclaredName: "agent-notify"})
	if err != nil {
		return m, req, noop, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil {
		return m, req, noop, err
	}
	primary, err := portable.ResolvePrimaryExecutable(snap.Ledger, req.Identity.Primary)
	if err != nil {
		return m, req, noop, err
	}
	cfg := *req.LocalConfig
	cfg.HookSpecs = copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: req.Identity.ControlRoot, BindingID: reserved.BindingID}, primary)
	body, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
	if err != nil {
		return m, req, noop, err
	}
	cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	if !cfg.NativeStop {
		cfg.HookSpecs = nil
	}
	draft, err := (packagesnapshot.Builder{}).Build(ctx, req.PackageRoot)
	if err != nil {
		return m, req, noop, err
	}
	defer func() { _ = draft.Close() }()
	// Builder rejects links and seals the snapshot. Only this owned draft is
	// opened for composition; the second snapshot seals the completed package.
	hook := filepath.Join(draft.Root, filepath.FromSlash(vscodelocalhooks.PluginPath))
	if err = os.Chmod(draft.Root, 0700); err != nil {
		return m, req, noop, err
	}
	dir := draft.Root
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(vscodelocalhooks.PluginPath)), "/") {
		dir = filepath.Join(dir, part)
		if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return m, req, noop, err
		}
		if err = os.Chmod(dir, 0700); err != nil {
			return m, req, noop, err
		}
	}

	if _, e := os.Lstat(hook); e == nil {
		if err = os.Chmod(hook, 0600); err != nil {
			return m, req, noop, err
		}
	} else if !os.IsNotExist(e) {
		return m, req, noop, e
	}
	if err = os.WriteFile(hook, body, 0600); err != nil {
		return m, req, noop, err
	}

	builder := packagesnapshot.Builder{}
	if retain {
		builder.TempRoot = filepath.Join(filepath.Dir(filepath.Dir(m.Roots.StateFile)), "acquired")
		if err := os.MkdirAll(builder.TempRoot, 0700); err != nil {
			return m, req, noop, err
		}
	}
	frozen, err := builder.Build(ctx, draft.Root)
	if err != nil {
		return m, req, noop, err
	}
	cleanup := func() { _ = frozen.Close() }
	adapter, err := vscode.NewLocal(cfg)
	if err != nil {
		cleanup()
		return m, req, noop, err
	}
	adapters := m.Registry.All()
	for i, existing := range adapters {
		if existing.ID() == domain.ClientVSCode {
			adapters[i] = adapter
		}
	}
	m.Registry, err = clients.NewRegistry(adapters...)
	if err != nil {
		cleanup()
		return m, req, noop, err
	}

	if retain {
		// Retain the digest-frozen canonical source in the existing acquisition
		// area so installed retries do not depend on transient proof scratch.
		if err := filepath.WalkDir(frozen.Root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return os.Chmod(path, 0700)
			}
			return nil
		}); err != nil {
			cleanup()
			return m, req, noop, err
		}
		cleanup = noop
	}

	req.LocalConfig, req.PackageRoot = &cfg, frozen.Root
	return m, req, cleanup, nil
}

// Required components follow independent Local selections. Native Stop has no
// dependency on the canonical MCP or skill components.
func requiredComponents(req MaterializeRequest) []string {
	if req.Integration == portable.Pi {
		return nil
	}
	if req.Integration != portable.CopilotVSCode {
		return []string{"mcp", "skills"}
	}
	var out []string
	if req.LocalConfig != nil {
		if len(req.LocalConfig.MCPServers) != 0 {
			out = append(out, "mcp")
		}
		if len(req.LocalConfig.Skills) != 0 {
			out = append(out, "skills")
		}
	}
	return out
}

func groupRequiredComponents(reqs []MaterializeRequest) []string {
	var out []string
	for _, component := range []string{"mcp", "skills"} {
		for _, req := range reqs {
			for _, selected := range requiredComponents(req) {
				if selected == component {
					out = append(out, component)
					goto next
				}
			}
		}
	next:
	}
	return out
}

func confirmLocalSelection(req MaterializeRequest, plan uapinstaller.Plan) error {
	if req.Integration != portable.CopilotVSCode {
		return nil
	}
	selected := plan.SelectedDelivery
	bindingID := plan.BindingID
	if len(plan.Targets) != 0 {
		found := false
		for _, target := range plan.Targets {
			if target.ClientID == ClientID(req.Integration) {
				if found {
					return ErrPreflight
				}
				selected, found = target.SelectedDelivery, true
				bindingID = target.BindingID
			}
		}
		if !found {
			return ErrPreflight
		}
	}
	facts, ok := selected.LocalFacts()
	config := req.LocalConfig
	if !ok || config == nil || facts.SettingsPath != config.ProfileSettingsPath || facts.Tuple != config.QualifiedTuple || facts.NativeStop != config.NativeStop || !slices.Equal(facts.MCPServers, config.MCPServers) || !slices.Equal(facts.Skills, config.Skills) {
		return fmt.Errorf("%w: public Local selection differs from confirmed request", ErrPreflight)
	}
	if config.NativeStop {
		snap, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
		if err != nil {
			return err
		}
		primary, err := portable.ResolvePrimaryExecutable(snap.Ledger, req.Identity.Primary)
		if err != nil {
			return err
		}
		body, err := vscodelocalhooks.Render(config.TargetShell, config.HookSpecs)
		if err != nil {
			return err
		}
		if err := vscodelocalhooks.VerifyOwned(body, config.TargetShell, copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: req.Identity.ControlRoot, BindingID: bindingID}, primary)); err != nil {
			return fmt.Errorf("%w: Local Stop does not name the reserved primary/binding", ErrPreflight)
		}
	}
	return nil
}
