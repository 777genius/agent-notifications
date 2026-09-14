// Package setupwizard is the P6 application service for setup-notifications wizard.
// It does not prompt. Missing noninteractive choices return a structured result.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

var ErrRefused = errors.New("setup wizard refused")

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
	GlobalConfig, CodexHome, ClaudeConfig             string
	ClientExecutable, ScopeRoot, Helper               string
	InstallationID, Primary                           string
	MCPConfig                                         map[string]string
}

type TargetResult struct {
	Client, Unit, Outcome, Reason string
}

type Result struct {
	Action     string         `json:"action"`
	Outcome    string         `json:"outcome"`
	Reason     string         `json:"reason,omitempty"`
	Generation uint64         `json:"generation,omitempty"`
	Targets    []TargetResult `json:"targets,omitempty"`
}

func (r Result) ExitCode() int {
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

func Run(ctx context.Context, req Request) (Result, error) {
	out := Result{Action: string(req.Action)}
	if ctx == nil {
		out.Outcome, out.Reason = "invalid", "context_required"
		return out, ErrRefused
	}
	switch req.Action {
	case ActionInstall, ActionUninstall, ActionInspect:
	case ActionUpdate, ActionRepair:
		out.Outcome, out.Reason = "incomplete", "action_not_published"
		return out, fmt.Errorf("%w: %s", ErrRefused, req.Action)
	default:
		out.Outcome, out.Reason = "invalid", "invalid_action"
		return out, ErrRefused
	}
	agents, err := normalizeAgents(req.Agents)
	if err != nil {
		out.Outcome, out.Reason = "invalid", err.Error()
		return out, ErrRefused
	}
	if len(agents) == 0 {
		out.Outcome, out.Reason = "cancelled", "empty_selection"
		return out, nil
	}
	if req.Action != ActionInspect && !req.Yes {
		out.Outcome, out.Reason = "invalid", "noninteractive_requires_yes"
		return out, ErrRefused
	}
	if !explicitAbs(req.ControlRoot) {
		out.Outcome, out.Reason = "invalid", "control_root_required"
		return out, ErrRefused
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", "managed_runtime_required"
		return out, err
	}
	out.Generation = snap.Ledger.Generation
	runtimeRoot := req.RuntimeRoot
	if runtimeRoot == "" {
		runtimeRoot = snap.Ledger.RuntimeRoot
	}
	if !explicitAbs(runtimeRoot) {
		out.Outcome, out.Reason = "invalid", "runtime_root_required"
		return out, ErrRefused
	}
	hooks := unitOn(req.Hooks, req.Action != ActionInspect)
	notify := unitOn(req.AgentNotify, req.Action != ActionInspect)
	if req.Action == ActionInspect {
		hooks, notify = true, true
	}
	if !hooks && !notify {
		out.Outcome, out.Reason = "cancelled", "empty_units"
		return out, nil
	}
	switch req.Action {
	case ActionInspect:
		return inspect(ctx, req, agents, snap, runtimeRoot, out)
	case ActionInstall:
		return install(ctx, req, agents, snap, runtimeRoot, hooks, notify, out)
	default:
		return uninstall(ctx, req, agents, snap, runtimeRoot, hooks, notify, out)
	}
}

func inspect(ctx context.Context, req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, runtimeRoot string, out Result) (Result, error) {
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	state, err := mat.Store.Load()
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	out.Outcome = "completed"
	for _, agent := range agents {
		found := false
		for _, installation := range state.Installations {
			for _, binding := range installation.Clients {
				if binding.ClientID != string(agent) {
					continue
				}
				found = true
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "installed", Reason: binding.ClientBindingID})
			}
		}
		if !found {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "absent"})
		}
		out = inspectHooks(req, agent, out)
		mcpPath := ""
		if req.MCPConfig != nil {
			mcpPath = req.MCPConfig[string(agent)]
		}
		if mcpPath == "" {
			continue
		}
		command := filepath.Join(runtimeRoot, primaryName(req))
		facts, err := clientsetup.Inspect(ctx, clientsetup.Request{
			ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Command: command, ConfigPath: mcpPath,
			Provider: discoveryProvider(agent), Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
		})
		if err != nil {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "direct-mcp", Outcome: "unknown", Reason: err.Error()})
			continue
		}
		outcome := "absent"
		if facts.Registered {
			outcome = "installed"
		}
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "direct-mcp", Outcome: outcome})
	}
	return out, nil
}

func install(ctx context.Context, req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, runtimeRoot string, hooks, notify bool, out Result) (Result, error) {
	if hooks {
		var err error
		out, err = applyHooks(ctx, req, agents, snap, false, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		generation, err := rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		snap.Ledger.Generation = generation
	}
	if !notify {
		out.Outcome = "completed"
		return out, nil
	}
	if !explicitAbs(req.PackageRoot) {
		out.Outcome, out.Reason = "incomplete", "package_required"
		return out, ErrRefused
	}
	if req.ClientExecutable == "" || !filepath.IsAbs(req.ClientExecutable) {
		out.Outcome, out.Reason = "incomplete", "client_executable_required"
		return out, ErrRefused
	}
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	id, err := identity(req, snap, runtimeRoot, mat, true)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	generation := snap.Ledger.Generation
	for _, agent := range agents {
		configPath := clientConfig(req, agent)
		if !explicitAbs(configPath) {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
			out.Outcome, out.Reason = "incomplete", "client_config_required"
			return out, ErrRefused
		}
		got, err := mat.Install(ctx, portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			PackageRoot: req.PackageRoot, ClientConfigRoot: configPath, ClientExecutable: req.ClientExecutable,
			Discovery:   discovery(req, agent, runtimeRoot, snap),
			OperationID: "wizard-install-" + string(agent),
		})
		if err != nil {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
			out.Outcome, out.Reason = "incomplete", "portable_install_failed"
			return out, err
		}
		generation, err = rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "completed", Reason: got.BindingID})
		id.InstallationID = got.InstallationID
	}
	out.Outcome = "completed"
	return out, nil
}

func uninstall(ctx context.Context, req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, runtimeRoot string, hooks, notify bool, out Result) (Result, error) {
	if hooks {
		var err error
		out, err = applyHooks(ctx, req, agents, snap, true, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		generation, err := rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		snap.Ledger.Generation = generation
	}
	if !notify {
		out.Outcome = "completed"
		return out, nil
	}
	if req.ClientExecutable == "" || !filepath.IsAbs(req.ClientExecutable) {
		out.Outcome, out.Reason = "incomplete", "client_executable_required"
		return out, ErrRefused
	}
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	id, err := identity(req, snap, runtimeRoot, mat, false)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	if id.InstallationID == "" {
		state, loadErr := mat.Store.Load()
		if loadErr != nil {
			out.Outcome, out.Reason = "incomplete", loadErr.Error()
			return out, loadErr
		}
		if len(state.Installations) == 1 {
			id.InstallationID = state.Installations[0].InstallationID
		}
	}
	if id.InstallationID == "" {
		out.Outcome, out.Reason = "unchanged", "portable_absent"
		return out, nil
	}
	generation := snap.Ledger.Generation
	removed := 0
	for _, agent := range agents {
		configPath := clientConfig(req, agent)
		if !explicitAbs(configPath) {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
			out.Outcome, out.Reason = "incomplete", "client_config_required"
			return out, ErrRefused
		}
		err := mat.Remove(ctx, portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: configPath, ClientExecutable: req.ClientExecutable,
			Discovery:   discovery(req, agent, runtimeRoot, snap),
			OperationID: "wizard-remove-" + string(agent), ExternalUninstalled: true,
		})
		if err != nil {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
			out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
			return out, err
		}
		removed++
		generation, err = rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "completed"})
	}
	if removed == 0 {
		out.Outcome = "unchanged"
		return out, nil
	}
	out.Outcome = "completed"
	return out, nil
}

func materializer(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string) (portablesetup.Materializer, error) {
	helper := req.Helper
	if helper == "" {
		helper = filepath.Join(runtimeRoot, primaryName(req))
	}
	if !explicitAbs(helper) {
		return portablesetup.Materializer{}, fmt.Errorf("%w: helper must be explicit", ErrRefused)
	}
	uapRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap")
	return portablesetup.NewMaterializer(portablesetup.UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: helper,
	})
}

func identity(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, mat portablesetup.Materializer, generate bool) (portablesetup.Identity, error) {
	id := req.InstallationID
	if id == "" {
		state, err := mat.Store.Load()
		if err != nil {
			return portablesetup.Identity{}, err
		}
		if len(state.Installations) == 1 {
			id = state.Installations[0].InstallationID
		} else if generate {
			generated, err := domain.NewInstallationID()
			if err != nil {
				return portablesetup.Identity{}, err
			}
			id = generated
		}
	}
	scope := req.ScopeRoot
	if scope == "" {
		scope = req.ControlRoot
	}
	global := req.GlobalConfig
	if global == "" {
		global = filepath.Join(runtimeRoot, "global", "config.json")
	}
	return portablesetup.Identity{
		InstallationID: id, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner,
		ScopeRoot: scope, ControlRoot: req.ControlRoot, GlobalConfig: global,
		RuntimeRoot: runtimeRoot, Primary: primaryName(req),
	}, nil
}

func primaryName(req Request) string {
	if req.Primary != "" {
		return req.Primary
	}
	return "primary"
}

func discovery(req Request, agent portable.Integration, runtimeRoot string, _ installruntime.InstalledSnapshot) portablesetup.Discovery {
	path := ""
	if req.MCPConfig != nil {
		path = req.MCPConfig[string(agent)]
	}
	if path == "" {
		return portablesetup.Discovery{}
	}
	return portablesetup.Discovery{ConfigPath: path, Command: filepath.Join(runtimeRoot, primaryName(req))}
}

func clientConfig(req Request, agent portable.Integration) string {
	switch agent {
	case portable.Codex:
		return req.CodexHome
	case portable.Claude:
		return req.ClaudeConfig
	default:
		return ""
	}
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

func rereadGeneration(controlRoot string) (uint64, error) {
	snap, err := installruntime.ReadInstalledSnapshot(controlRoot)
	if err != nil {
		return 0, err
	}
	return snap.Ledger.Generation, nil
}

func normalizeAgents(agents []string) ([]portable.Integration, error) {
	var out []portable.Integration
	seen := map[portable.Integration]bool{}
	for _, item := range agents {
		var id portable.Integration
		switch item {
		case "claude":
			id = portable.Claude
		case "codex":
			id = portable.Codex
		default:
			return nil, errors.New("invalid_agents")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func unitOn(flag *bool, defaultOn bool) bool {
	if flag == nil {
		return defaultOn
	}
	return *flag
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}
