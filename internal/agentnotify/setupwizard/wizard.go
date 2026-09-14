// Package setupwizard is the P6 application service for setup-notifications wizard.
// Run does not prompt. FillInteractive is a thin TTY adapter that only fills Request.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"

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
	ClientExecutables                                 map[string]string
	InstallationID, Primary                           string
	MCPConfig                                         map[string]string
	ClaudeHooks, CodexHooks                           *bool
	ClaudeAgentNotify, CodexAgentNotify               *bool
	// ClaudeRunner overrides Claude activation probing. Production leaves it
	// nil so the OS process runner is used. Isolated tests inject a listing
	// fixture; the field is never parsed from CLI flags.
	ClaudeRunner providers.CommandRunner
}

type TargetResult struct {
	Client, Unit, Outcome, Reason string
}

// ReadinessFact is independent of binary download. Inspect and mutation both
// report these fields; not_verified/unsupported are not installation failure.
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
	Action      string          `json:"action"`
	Outcome     string          `json:"outcome"`
	Reason      string          `json:"reason,omitempty"`
	Generation  uint64          `json:"generation,omitempty"`
	Command     []string        `json:"command,omitempty"`
	Targets     []TargetResult  `json:"targets,omitempty"`
	Readiness   []ReadinessFact `json:"readiness,omitempty"`
	NextActions []NextAction    `json:"nextActions,omitempty"`
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
	out, err := run(ctx, req)
	return attachCommand(req, out), err
}

func run(ctx context.Context, req Request) (Result, error) {
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
	if req.Action == ActionInspect {
		got, err := inspect(ctx, req, agents, snap, runtimeRoot, out)
		return attachReadiness(agents, got, false), err
	}
	hookAgents, notifyAgents := selectedUnits(req, agents)
	if len(hookAgents) == 0 && len(notifyAgents) == 0 {
		out.Outcome, out.Reason = "cancelled", "empty_units"
		return out, nil
	}
	var got Result
	if req.Action == ActionInstall {
		got, err = install(ctx, req, snap, runtimeRoot, hookAgents, notifyAgents, out)
		return attachReadiness(agents, got, true), err
	}
	got, err = uninstall(ctx, req, snap, runtimeRoot, hookAgents, notifyAgents, out)
	return attachReadiness(agents, got, false), err
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

func install(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, out Result) (Result, error) {
	if len(hookAgents) > 0 {
		var err error
		out, err = applyHooks(ctx, req, hookAgents, snap, false, out)
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
	if len(notifyAgents) == 0 {
		out.Outcome = "completed"
		return out, nil
	}
	if !explicitAbs(req.PackageRoot) {
		out.Outcome, out.Reason = "incomplete", "package_required"
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
	for _, agent := range notifyAgents {
		configPath := clientConfig(req, agent)
		if !explicitAbs(configPath) {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
			out.Outcome, out.Reason = "incomplete", "client_config_required"
			return out, ErrRefused
		}
		executable := clientExecutable(req, agent)
		if !explicitAbs(executable) {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_executable_required"})
			out.Outcome, out.Reason = "incomplete", "client_executable_required"
			return out, ErrRefused
		}
		materialize := portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			PackageRoot: req.PackageRoot, ClientConfigRoot: configPath, ClientExecutable: executable,
			Discovery:   discovery(req, agent, runtimeRoot, snap),
			OperationID: "wizard-install-" + string(agent),
		}
		if others, err := mat.OtherLiveClients(id.InstallationID, string(agent)); err != nil {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
			out.Outcome, out.Reason = "incomplete", "portable_inspect_failed"
			return out, err
		} else if len(others) > 0 {
			if err := mat.GuardSecondClient(ctx, materialize); err != nil {
				if portablesetup.IsUpdateRequired(err) {
					return updateRequired(req, agent, others, out, err)
				}
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
				out.Outcome, out.Reason = "incomplete", "portable_install_failed"
				return out, err
			}
		}
		got, err := mat.Install(ctx, materialize)
		if err != nil {
			if portablesetup.IsUpdateRequired(err) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
				return updateRequired(req, agent, others, out, err)
			}
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

func uninstall(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, out Result) (Result, error) {
	if len(hookAgents) > 0 {
		var err error
		out, err = applyHooks(ctx, req, hookAgents, snap, true, out)
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
	if len(notifyAgents) == 0 {
		out.Outcome = "completed"
		return out, nil
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
	for _, agent := range notifyAgents {
		configPath := clientConfig(req, agent)
		if !explicitAbs(configPath) {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
			out.Outcome, out.Reason = "incomplete", "client_config_required"
			return out, ErrRefused
		}
		executable := clientExecutable(req, agent)
		if !explicitAbs(executable) {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_executable_required"})
			out.Outcome, out.Reason = "incomplete", "client_executable_required"
			return out, ErrRefused
		}
		err := mat.Remove(ctx, portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: configPath, ClientExecutable: executable,
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

func updateRequired(req Request, adding portable.Integration, others []string, out Result, err error) (Result, error) {
	out.Targets = append(out.Targets, TargetResult{Client: string(adding), Unit: "agent-notify", Outcome: "incomplete", Reason: "update_required"})
	out.Outcome, out.Reason = "incomplete", "update_required"
	updateReq := req
	updateReq.Action = ActionUpdate
	updateReq.Agents = others
	addReq := req
	addReq.Agents = []string{string(adding)}
	out.NextActions = []NextAction{
		{Kind: "update", Agents: others, Command: RetryCommand(updateReq), Reason: "update_existing_before_add"},
		{Kind: "install", Agents: []string{string(adding)}, Command: RetryCommand(addReq), Reason: "add_after_update"},
	}
	return out, err
}

func materializer(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string) (portablesetup.Materializer, error) {
	helper := req.Helper
	if helper == "" {
		helper = filepath.Join(runtimeRoot, primaryName(req))
	}
	if !explicitAbs(helper) {
		return portablesetup.Materializer{}, fmt.Errorf("%w: helper must be explicit", ErrRefused)
	}
	runner := req.ClaudeRunner
	if runner == nil {
		runner = processadapter.OS{}
	}
	uapRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap")
	return portablesetup.NewMaterializer(portablesetup.UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: helper,
		ClaudeRunner:     runner,
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

func selectedUnits(req Request, agents []portable.Integration) (hooks, notify []portable.Integration) {
	defaultOn := req.Action != ActionInspect
	for _, agent := range agents {
		if agentUnit(req.Hooks, perClientHooks(req, agent), defaultOn) {
			hooks = append(hooks, agent)
		}
		if agentUnit(req.AgentNotify, perClientNotify(req, agent), defaultOn) {
			notify = append(notify, agent)
		}
	}
	return hooks, notify
}

func perClientHooks(req Request, agent portable.Integration) *bool {
	switch agent {
	case portable.Claude:
		return req.ClaudeHooks
	case portable.Codex:
		return req.CodexHooks
	default:
		return nil
	}
}

func perClientNotify(req Request, agent portable.Integration) *bool {
	switch agent {
	case portable.Claude:
		return req.ClaudeAgentNotify
	case portable.Codex:
		return req.CodexAgentNotify
	default:
		return nil
	}
}

func agentUnit(global, perClient *bool, defaultOn bool) bool {
	if perClient != nil {
		return *perClient
	}
	return unitOn(global, defaultOn)
}

func unitOn(flag *bool, defaultOn bool) bool {
	if flag == nil {
		return defaultOn
	}
	return *flag
}

func attachReadiness(agents []portable.Integration, out Result, mutationInstall bool) Result {
	if out.Outcome == "invalid" || out.Outcome == "cancelled" {
		return out
	}
	runtime := "installed"
	if out.Reason == "managed_runtime_required" {
		runtime = "absent"
	}
	for _, agent := range agents {
		fact := ReadinessFact{
			Client:     string(agent),
			Runtime:    runtime,
			Hooks:      "absent",
			MCP:        "absent",
			Permission: "unsupported",
			Restart:    "not_required",
			Delivery:   "not_verified",
		}
		for _, target := range out.Targets {
			if target.Client != string(agent) {
				continue
			}
			switch target.Unit {
			case "hooks":
				fact.Hooks = target.Outcome
			case "agent-notify":
				switch target.Outcome {
				case "completed", "installed":
					fact.MCP = "installed"
				default:
					fact.MCP = target.Outcome
				}
			}
		}
		if mutationInstall && fact.MCP == "installed" && out.Outcome == "completed" {
			fact.Restart = "pending"
		}
		out.Readiness = append(out.Readiness, fact)
	}
	return out
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}
