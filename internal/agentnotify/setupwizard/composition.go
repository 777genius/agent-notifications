package setupwizard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
)

func materializer(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string) (portablesetup.Materializer, error) {
	helper := req.Helper
	if helper == "" {
		primary := req.Primary
		if primary == "" {
			primary = portable.PlatformPrimary()
		}
		if primary == "primary" {
			var err error
			helper, err = portable.ResolvePrimaryExecutable(snap.Ledger, primary)
			if err != nil {
				return portablesetup.Materializer{}, fmt.Errorf("%w: legacy helper unavailable: %v", ErrRefused, err)
			}
		} else {
			helper = filepath.Join(runtimeRoot, filepath.FromSlash(primary))
		}
	}
	if !explicitAbs(helper) {
		return portablesetup.Materializer{}, fmt.Errorf("%w: helper must be explicit", ErrRefused)
	}
	runner := req.ClaudeRunner
	if runner == nil {
		runner = processadapter.OS{}
	}
	uapRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap")
	mat, err := portablesetup.NewMaterializer(portablesetup.UAPRoots{
		StateFile:           filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:            filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:       filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:      filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:         filepath.Join(uapRoot, "managed"),
		HelperExecutable:    helper,
		ClaudeRunner:        runner,
		CodexRunner:         processadapter.OS{},
		RequireLiveProfiles: true,
	})
	mat.Kernel.ExpectedPolicy = req.BootstrapExpectedPolicy
	mat.PiChoices = piauthority.Choices{Desktop: req.PiDesktop, Webhook: req.PiWebhook, Preimage: req.PiChannelPreimage}
	mat.Progress = req.ClientProgress
	if err != nil {
		return mat, err
	}
	agents, err := normalizeAgents(req.Agents)
	if err != nil {
		return mat, err
	}
	_, notify := selectedUnits(req, agents)
	for _, agent := range notify {
		if agent == portable.CopilotVSCode {
			if req.LocalConfig == nil || req.LocalConfig.QualifiedTuple != vscode.QualifiedDarwinTESTTuple() || !explicitAbs(req.LocalConfig.ProfileSettingsPath) {
				return mat, portablesetup.ErrPreflight
			}
			adapter, err := vscode.NewLocal(*req.LocalConfig)
			if err != nil {
				return mat, err
			}
			registry := mat.Registry
			if registry == nil {
				registry, err = portablesetup.NewRegistry()
				if err != nil {
					return mat, err
				}
			}
			mat.Registry, err = clients.NewRegistry(append(registry.All(), adapter)...)
			if err != nil {
				return mat, err
			}
			continue
		}
		if agent != portable.Cursor {
			continue
		}
		if req.CursorAuthority == nil || !explicitAbs(req.CursorConfig) || req.CursorAuthority.Executable != helper {
			return mat, fmt.Errorf("%w: explicit qualified Cursor composition required", portablesetup.ErrPreflight)
		}
		adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, req.CursorAuthority)
		if err != nil {
			return mat, err
		}
		if _, err := adapter.ResolveProfileRoot(req.CursorConfig); err != nil {
			return mat, err
		}
		registry := mat.Registry
		if registry == nil {
			registry, err = portablesetup.NewRegistry()
		}
		if err != nil {
			return mat, err
		}
		mat.Registry, err = clients.NewRegistry(append(registry.All(), adapter)...)
		if err != nil {
			return mat, err
		}
	}
	return mat, nil
}

func identity(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, mat portablesetup.Materializer, generate bool) (portablesetup.Identity, error) {
	eng, err := installerEngine(mat)
	if err != nil {
		return portablesetup.Identity{}, err
	}
	clientID := ""
	configRoot := ""
	if len(req.Agents) == 1 {
		switch req.Agents[0] {
		case string(portable.Codex), string(portable.Claude), string(portable.Cursor), string(portable.CopilotVSCode), string(portable.Pi):
			clientID = portablesetup.ClientID(portable.Integration(req.Agents[0]))
			configRoot = clientConfig(req, portable.Integration(req.Agents[0]))
		}
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{
		ClientID:         clientID,
		InstallationID:   req.InstallationID,
		Allocate:         generate,
		DeclaredName:     packageDeclaredName(req.PackageRoot),
		ClientConfigRoot: configRoot,
	})
	if err != nil {
		if errors.Is(err, uapinstaller.ErrAmbiguousInstallations) {
			return portablesetup.Identity{}, fmt.Errorf("%w: %w", ErrAmbiguousInstallation, err)
		}
		return portablesetup.Identity{}, err
	}
	id := reserved.InstallationID
	scope := req.ScopeRoot
	if scope == "" {
		scope = req.ControlRoot
	}
	global := req.GlobalConfig
	if global != "" {
		physical, err := installruntime.PhysicalPath(global)
		if err != nil {
			return portablesetup.Identity{}, err
		}
		global = physical
	}
	var frozen *portablesetup.Identity
	if id != "" && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair || req.Action == ActionUninstall) {
		base := portablesetup.Identity{InstallationID: id, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner,
			ScopeRoot: scope, ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, GlobalConfig: global}
		var candidates []portablesetup.Identity
		for _, name := range req.Agents {
			agent := portable.Integration(name)
			if agent != portable.Claude && agent != portable.Codex && agent != portable.Cursor && agent != portable.CopilotVSCode && agent != portable.Pi {
				continue
			}
			if migration, ok := req.MigrationBindings[name]; ok {
				candidates = append(candidates, identityFromBinding(base, migration.Old))
			} else if removal, ok := req.RemovalBindings[name]; ok {
				candidates = append(candidates, identityFromBinding(base, removal.Old))
			} else if liveNotifyClient(mat, id, name) {
				committed, err := targetIdentity(base, req, snap, mat, agent)
				if err != nil {
					return portablesetup.Identity{}, err
				}
				if committed.Primary != "" {
					candidates = append(candidates, committed)
				}
			}
		}
		for _, candidate := range candidates {
			if (global == "" || candidate.GlobalConfig == global) && (req.Primary == "" || candidate.Primary == req.Primary) {
				cp := candidate
				frozen = &cp
				break
			}
		}
		if frozen == nil && len(candidates) != 0 {
			if req.Action == ActionInstall {
				return portablesetup.Identity{}, fmt.Errorf("%w: requested config or primary differs from selected clients", ErrRefused)
			}
			return portablesetup.Identity{}, fmt.Errorf("%w: requested config or primary differs from selected clients", portablesetup.ErrIntentConflict)
		}
	}
	if frozen != nil {
		if global != "" && global != frozen.GlobalConfig {
			return portablesetup.Identity{}, fmt.Errorf("%w: confirmed global config differs", portablesetup.ErrIntentConflict)
		}
		global = frozen.GlobalConfig
		scope = frozen.ScopeRoot
		if req.Primary != "" && req.Primary != frozen.Primary {
			return portablesetup.Identity{}, fmt.Errorf("%w: confirmed primary differs", portablesetup.ErrIntentConflict)
		}
	}
	if id != "" && frozen == nil {
		installed, found, err := portable.InstalledGlobalConfig(snap.Ledger, id, req.ControlRoot)
		if err != nil {
			return portablesetup.Identity{}, err
		}
		if found {
			if global != "" && global != installed {
				return portablesetup.Identity{}, fmt.Errorf("%w: installed global config differs from --global-config", ErrRefused)
			}
			global = installed
		}
	}
	if snap.Ledger.PendingMutation != nil && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair) {
		intent, err := portablesetup.ReadIntent(req.ControlRoot)
		if err != nil || intent.SetupIntentID != snap.Ledger.PendingMutation.ID {
			return portablesetup.Identity{}, fmt.Errorf("%w: pending global config identity unavailable", portablesetup.ErrIntentConflict)
		}
		if intent.GlobalConfig != "" {
			if global != "" && global != intent.GlobalConfig {
				return portablesetup.Identity{}, fmt.Errorf("%w: pending global config differs", portablesetup.ErrIntentConflict)
			}
			global = intent.GlobalConfig
		}
	}
	if global == "" {
		selected, err := config.Resolve(config.SnapshotEnv())
		if err != nil {
			return portablesetup.Identity{}, err
		}
		global = selected.Path
	}
	primary := ""
	if frozen != nil {
		primary = frozen.Primary
	} else {
		primary, err = primaryName(req, snap.Ledger, id)
		if err != nil {
			return portablesetup.Identity{}, err
		}
	}
	return portablesetup.Identity{
		InstallationID: id, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner,
		ScopeRoot: scope, ControlRoot: req.ControlRoot, GlobalConfig: global,
		RuntimeRoot: runtimeRoot, Primary: primary,
	}, nil
}

func installerEngine(mat portablesetup.Materializer) (*uapinstaller.Engine, error) {
	registry := mat.Registry
	if registry == nil {
		var err error
		registry, err = portablesetup.NewRegistry()
		if err != nil {
			return nil, err
		}
	}
	return uapinstaller.New(uapinstaller.Config{
		StateRoot:            filepath.Dir(mat.Roots.StateFile),
		StateFile:            mat.Roots.StateFile,
		LockFile:             mat.Roots.LockFile,
		OperationsDir:        mat.Roots.OperationsDir,
		PluginDataBase:       mat.Roots.PluginDataBase,
		ManagedRoot:          mat.Roots.ManagedRoot,
		HelperExecutable:     mat.Roots.HelperExecutable,
		Registry:             registry,
		TrustedLocalPackages: true,
	})
}

func primaryName(req Request, ledger installruntime.Ledger, installationID string) (string, error) {
	if req.Primary != "" {
		return req.Primary, nil
	}
	if installationID != "" {
		primary, found, err := portable.InstalledPrimary(ledger, installationID, req.ControlRoot)
		if err != nil {
			return "", err
		}
		if found {
			return primary, nil
		}
	}
	return portable.PlatformPrimary(), nil
}

func discovery(req Request, agent portable.Integration, runtimeRoot string, snap installruntime.InstalledSnapshot) portablesetup.Discovery {
	path := discoveryConfigPath(req, agent)
	if path == "" {
		return portablesetup.Discovery{}
	}
	command, err := installruntime.OwnedNotificationCommand(snap.Ledger, runtimeRoot)
	if err != nil {
		// Keep discovery explicit; portablesetup must refuse a managed handoff
		// when the installed command is missing or ambiguous.
		return portablesetup.Discovery{ConfigPath: path}
	}
	return portablesetup.Discovery{ConfigPath: path, Command: command}
}

// discoveryConfigPath is the owned client MCP file to hand off. Explicit
// --mcp-config / --claude-mcp-config win. Otherwise a regular file already
// present in the selected profile is used for Codex. Claude's registration
// file is independent of its UAP plugin profile: it defaults to HOME/.claude.json
// and uses the explicit Claude config root only when one was supplied.
func discoveryConfigPath(req Request, agent portable.Integration) string {
	if req.MCPConfig != nil {
		if path := req.MCPConfig[string(agent)]; path != "" {
			return path
		}
	}
	root := clientConfig(req, agent)
	if agent == portable.Claude && root == "" {
		root = userHomeDir()
	}
	if !explicitAbs(root) {
		return ""
	}
	name := ""
	switch agent {
	case portable.Codex:
		name = "config.toml"
	case portable.Claude:
		name = ".claude.json"
	default:
		return ""
	}
	path := filepath.Join(root, name)
	if !explicitAbs(path) {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return path
}

func userHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home
	}
	if home := strings.TrimSpace(os.Getenv("USERPROFILE")); home != "" {
		return home
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return ""
}

func bindDiscoveredMCP(req *Request, agents []portable.Integration) {
	if req == nil {
		return
	}
	for _, agent := range agents {
		path := discoveryConfigPath(*req, agent)
		if path == "" {
			continue
		}
		if req.MCPConfig == nil {
			req.MCPConfig = map[string]string{}
		}
		if req.MCPConfig[string(agent)] == "" {
			req.MCPConfig[string(agent)] = path
		}
	}
}

func clientConfig(req Request, agent portable.Integration) string {
	switch agent {
	case portable.Codex:
		return req.CodexHome
	case portable.Claude:
		return req.ClaudeConfig
	case portable.CopilotVSCode:
		if req.LocalConfig != nil {
			return filepath.Dir(req.LocalConfig.ProfileSettingsPath)
		}
		return ""
	case portable.Pi:
		return req.PiAgentDir
	case portable.Cursor:
		return req.CursorConfig
	default:
		return ""
	}
}

func clientExecutable(req Request, agent portable.Integration) string {
	if req.ClientExecutables != nil {
		if path := req.ClientExecutables[string(agent)]; path != "" {
			return path
		}
	}
	return req.ClientExecutable
}

func discoveryProvider(agent portable.Integration) registration.Provider {
	switch agent {
	case portable.Codex:
		return registration.Codex
	case portable.Claude:
		return registration.Claude
	default:
		return ""
	}
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}
