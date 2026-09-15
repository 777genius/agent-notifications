// Package setupwizard is the P6 application service for setup-notifications wizard.
// Run does not prompt. FillInteractive is a thin TTY adapter that only fills Request.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

var ErrRefused = errors.New("setup wizard refused")
var ErrAmbiguousInstallation = errors.New("ambiguous_installation")

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
	// CodexHome and ClaudeConfig are explicit UAP client profile roots.
	// The CLI fills omitted values from CODEX_HOME / CLAUDE_CONFIG_DIR once;
	// Run and Plan do not reread the process environment.
	GlobalConfig, CodexHome, ClaudeConfig string
	ClientExecutable, ScopeRoot, Helper   string
	ClientExecutables                     map[string]string
	PackageSHA256                         string
	InstallationID, Primary               string
	MCPConfig                             map[string]string
	ClaudeHooks, CodexHooks               *bool
	ClaudeAgentNotify, CodexAgentNotify   *bool
	// ExternalUninstalled is host attestation that Codex already removed the
	// native plugin, or never activated it. --yes does not set this.
	ExternalUninstalled bool
	// ClaudeRunner overrides Claude activation probing. Production leaves it
	// nil so the OS process runner is used. Isolated tests inject a listing
	// fixture; the field is never parsed from CLI flags.
	ClaudeRunner providers.CommandRunner
	// ReleaseVersion is the accepted master revision without a leading v.
	// Empty means this binary's compiled consumer version, set by the CLI.
	ReleaseVersion string
	// ReleaseDownloadRoot is the directory that contains v{version}/ assets.
	// Empty disables host fetch so tests that omit --package stay offline.
	ReleaseDownloadRoot string
	// PackageFetcher downloads one URL. Production uses HTTPS; tests inject
	// a local server. Never parsed from CLI flags.
	PackageFetcher func(context.Context, string) ([]byte, error)
	// Progress reports large confirmed phases to the host. JSON stdout stays
	// one result; the CLI writes these lines to stderr. Nil is silent.
	Progress func(string)
	// DiscoverAgents supplies Claude/Codex executable presence for the TTY
	// picker. Production sets this from Engine.Discover. Nil skips presence
	// labels. The function must not execute found files.
	DiscoverAgents func() []AgentCapability
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
	out, err := run(ctx, &req)
	return attachCommand(req, out), err
}

// SetupPlan is the read-only preflight shown before TTY confirmation.
// Ready means the application service can mutate after --yes; it is not a
// committed installation result.
type SetupPlan struct {
	Text    string
	Ready   bool
	Request Request
	Result  Result
}

// Plan preflights without publishing intent or applying hooks/MCP.
func Plan(ctx context.Context, req Request) (SetupPlan, error) {
	ev := evaluate(ctx, &req, false)
	text := confirmPlan(req)
	plan := SetupPlan{Text: text, Request: req, Result: attachCommand(req, ev.out)}
	if ev.stop {
		return plan, ev.err
	}
	if req.Action == ActionInstall {
		var releasePackage func()
		defer func() {
			if releasePackage != nil {
				releasePackage()
			}
		}()
		for _, agent := range ev.notifyAgents {
			if !explicitAbs(clientConfig(req, agent)) {
				ev.out.Outcome, ev.out.Reason = "incomplete", "client_config_required"
				plan.Result = attachCommand(req, ev.out)
				return plan, ErrRefused
			}
			if !explicitAbs(clientExecutable(req, agent)) {
				ev.out.Outcome, ev.out.Reason = "incomplete", "client_executable_required"
				plan.Result = attachCommand(req, ev.out)
				return plan, ErrRefused
			}
			if others, err := otherLiveClients(req, ev.snap, ev.runtimeRoot, req.InstallationID, string(agent)); err == nil && len(others) > 0 {
				text += " required-update=" + strings.Join(others, ",")
			}
		}
		if len(ev.notifyAgents) > 0 {
			acquired, release, err := acquirePlanPackage(ctx, req, ev.notifyAgents)
			if err != nil {
				reason := "package_acquisition_failed"
				if strings.Contains(err.Error(), "package_required") {
					reason = "package_required"
				} else if strings.Contains(err.Error(), "recorded_package_unavailable") {
					reason = "recorded_package_unavailable"
				}
				ev.out.Outcome, ev.out.Reason = "incomplete", reason
				plan.Result = attachCommand(req, ev.out)
				return plan, err
			}
			releasePackage = release
			for _, agent := range ev.notifyAgents {
				preview, err := previewNotifyPlan(ctx, acquired, ev.snap, ev.runtimeRoot, agent)
				if err != nil {
					if mapped, handled := mapAmbiguous(err, ev.out); handled {
						plan.Result = attachCommand(req, mapped)
						return plan, err
					}
					ev.out.Outcome, ev.out.Reason = "incomplete", "portable_preflight_failed"
					plan.Result = attachCommand(req, ev.out)
					return plan, err
				}
				if preview.TreeDigest != "" {
					text += " source-digest=" + preview.TreeDigest
				}
				if preview.HelperDigest != "" {
					text += " helper-digest=" + preview.HelperDigest
				}
				if preview.HelperVersion != "" {
					text += " helper-version=" + preview.HelperVersion
				}
				if preview.TreeDigest != "" || preview.HelperDigest != "" {
					break
				}
			}
		}
	}
	if view, err := inspectUAPState(ctx, req); err == nil && view.Recovery.Required {
		ids := recoveryIDs(view)
		reason := strings.Join(ids, ",")
		if reason == "" {
			reason = view.Recovery.Reason
		}
		if len(ids) > 0 {
			text += " recovery-pending=" + strings.Join(ids, ",")
		} else if view.Recovery.Reason != "" {
			text += " recovery-pending=untrusted"
		}
		ev.out.NextActions = append(ev.out.NextActions, NextAction{Kind: "recover", Reason: reason})
	}
	ev.out.Outcome, ev.out.Reason = "ready", ""
	plan.Ready = true
	plan.Text = text
	plan.Result = ev.out
	return plan, nil
}

func run(ctx context.Context, req *Request) (Result, error) {
	ev := evaluate(ctx, req, true)
	if ev.stop {
		return ev.out, ev.err
	}
	if req.Action == ActionInspect {
		got, err := inspect(ctx, *req, ev.agents, ev.snap, ev.runtimeRoot, ev.out)
		return attachReadiness(*req, ev.agents, got, false), err
	}
	var got Result
	var err error
	if req.Action == ActionInstall {
		got, err = install(ctx, *req, ev.snap, ev.runtimeRoot, ev.hookAgents, ev.notifyAgents, ev.out)
		got, err = finishWizardIntent(ctx, *req, ev.runtimeRoot, got, err)
		return attachReadiness(*req, ev.agents, got, true), err
	}
	got, err = uninstall(ctx, *req, ev.snap, ev.runtimeRoot, ev.hookAgents, ev.notifyAgents, ev.out)
	got, err = finishWizardIntent(ctx, *req, ev.runtimeRoot, got, err)
	return attachReadiness(*req, ev.agents, got, false), err
}

type evaluated struct {
	agents                   []portable.Integration
	snap                     installruntime.InstalledSnapshot
	runtimeRoot              string
	hookAgents, notifyAgents []portable.Integration
	out                      Result
	err                      error
	stop                     bool
}

func evaluate(ctx context.Context, req *Request, requireYes bool) evaluated {
	out := Result{Action: string(req.Action)}
	if ctx == nil {
		out.Outcome, out.Reason = "invalid", "context_required"
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	switch req.Action {
	case ActionInstall, ActionUninstall, ActionInspect:
	case ActionUpdate, ActionRepair:
		out.Outcome, out.Reason = "incomplete", "action_not_published"
		return evaluated{out: out, err: fmt.Errorf("%w: %s", ErrRefused, req.Action), stop: true}
	default:
		out.Outcome, out.Reason = "invalid", "invalid_action"
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	agents, err := normalizeAgents(req.Agents)
	if err != nil {
		out.Outcome, out.Reason = "invalid", err.Error()
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	if req.Action == ActionInspect && len(agents) == 0 {
		out.Outcome, out.Reason = "cancelled", "empty_selection"
		return evaluated{out: out, stop: true}
	}
	var snap installruntime.InstalledSnapshot
	haveSnap := false
	if explicitAbs(req.ControlRoot) {
		snap, err = installruntime.ReadInstalledSnapshot(req.ControlRoot)
		if err == nil {
			haveSnap = true
			out.Generation = snap.Ledger.Generation
		}
	}
	if req.Action != ActionInspect {
		resumed := false
		if haveSnap {
			restored, nextAgents, nextOut, didResume, resumeErr := resumeFromPendingIntent(*req, agents, snap, out)
			*req = restored
			agents, out, err = nextAgents, nextOut, resumeErr
			if err != nil {
				return evaluated{out: out, err: err, stop: true}
			}
			if out.Outcome == "conflict" {
				return evaluated{out: out, err: ErrRefused, stop: true}
			}
			resumed = didResume
		}
		if requireYes && !req.Yes && !resumed {
			out.Outcome, out.Reason = "invalid", "noninteractive_requires_yes"
			return evaluated{out: out, err: ErrRefused, stop: true}
		}
	}
	if !explicitAbs(req.ControlRoot) {
		out.Outcome, out.Reason = "invalid", "control_root_required"
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	if !haveSnap {
		out.Outcome, out.Reason = "incomplete", "managed_runtime_required"
		return evaluated{out: out, err: err, stop: true}
	}
	runtimeRoot := req.RuntimeRoot
	if runtimeRoot == "" {
		runtimeRoot = snap.Ledger.RuntimeRoot
	}
	if !explicitAbs(runtimeRoot) {
		out.Outcome, out.Reason = "invalid", "runtime_root_required"
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	if len(agents) == 0 {
		out.Outcome, out.Reason = "cancelled", "empty_selection"
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, stop: true}
	}
	if req.Action == ActionInspect {
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out}
	}
	hookAgents, notifyAgents := selectedUnits(*req, agents)
	if len(hookAgents) == 0 && len(notifyAgents) == 0 {
		out.Outcome, out.Reason = "cancelled", "empty_units"
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, stop: true}
	}
	return evaluated{
		agents: agents, snap: snap, runtimeRoot: runtimeRoot,
		hookAgents: hookAgents, notifyAgents: notifyAgents, out: out,
	}
}

func otherLiveClients(req Request, snap installruntime.InstalledSnapshot, runtimeRoot, installationID, adding string) ([]string, error) {
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return nil, err
	}
	return mat.OtherLiveClients(installationID, adding)
}

func liveNotifyClient(mat portablesetup.Materializer, installationID, clientID string) bool {
	if installationID == "" || clientID == "" {
		return false
	}
	state, err := mat.Store.Load()
	if err != nil {
		return false
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID {
				return true
			}
		}
	}
	return false
}

func previewNotifyPlan(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, agent portable.Integration) (uapinstaller.Plan, error) {
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	id, err := identity(req, snap, runtimeRoot, mat, true)
	if err != nil {
		return uapinstaller.Plan{}, err
	}
	return mat.PreviewPlan(ctx, portablesetup.MaterializeRequest{
		Identity: id, Integration: agent, PackageRoot: req.PackageRoot,
		ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
		OperationID: "wizard-plan-" + string(agent),
	})
}

func acquirePlanPackage(ctx context.Context, req Request, notifyAgents []portable.Integration) (Request, func(), error) {
	cleanup := func() {}
	if len(notifyAgents) == 0 {
		return req, cleanup, nil
	}
	recordedPath, recordedVersion := desiredPackage(req)
	if !explicitAbs(req.PackageRoot) && recordedPath == "" && recordedVersion != "" && req.ReleaseDownloadRoot == "" && req.PackageFetcher == nil {
		return req, cleanup, fmt.Errorf("%w: recorded_package_unavailable", ErrRefused)
	}
	packageRoot, release, err := resolvePackageRoot(ctx, req, recordedPath, recordedVersion)
	if err != nil {
		return req, cleanup, err
	}
	req.PackageRoot = packageRoot
	return req, release, nil
}

func inspectUAPState(ctx context.Context, req Request) (uapinstaller.Inspection, error) {
	if !explicitAbs(req.ControlRoot) {
		return uapinstaller.Inspection{}, nil
	}
	stateRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state")
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: stateRoot})
	if err != nil {
		return uapinstaller.Inspection{}, err
	}
	return eng.Inspect(ctx)
}

// DiscoverAgents reports Claude/Codex user-scope metadata and executable
// presence without creating UAP state or executing found files.
func DiscoverAgents(req Request) []AgentCapability {
	stateRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state")
	if !explicitAbs(req.ControlRoot) {
		stateRoot = filepath.Join(os.TempDir(), "uapinstaller-discover-absent")
	}
	eng, err := uapinstaller.New(uapinstaller.Config{
		StateRoot:         stateRoot,
		ClientExecutables: req.ClientExecutables,
	})
	if err != nil {
		return nil
	}
	found := eng.Discover()
	out := make([]AgentCapability, 0, len(found))
	for _, item := range found {
		out = append(out, AgentCapability{
			ID: item.ClientID, Present: item.ExecutablePresent, Path: item.ExecutablePath,
			Bound: len(item.Bindings) > 0,
		})
	}
	return out
}

func recoveryIDs(view uapinstaller.Inspection) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, journal := range view.Recovery.Journals {
		add(journal.OperationID)
	}
	for _, receipt := range view.Recovery.Receipts {
		add(receipt.OperationID)
	}
	return ids
}

func resumeFromPendingIntent(req Request, agents []portable.Integration, snap installruntime.InstalledSnapshot, out Result) (Request, []portable.Integration, Result, bool, error) {
	pending := snap.Ledger.PendingMutation
	if pending == nil || pending.Owner != "existing-installer" {
		return req, agents, out, false, nil
	}
	intent, err := portablesetup.ReadIntent(req.ControlRoot)
	if err != nil || intent.SetupIntentID != pending.ID {
		out.Outcome, out.Reason = "incomplete", "pending_intent_unreadable"
		if err == nil {
			err = ErrRefused
		}
		return req, agents, out, false, err
	}
	if intent.Action != string(req.Action) {
		conflict, _ := pendingIntentConflict(req, portablesetup.ErrIntentConflict, out)
		return req, agents, conflict, false, ErrRefused
	}
	req, agents, err = restoreOmittedFromIntent(req, agents, intent)
	if err != nil {
		if errors.Is(err, portablesetup.ErrIntentConflict) {
			conflict, _ := pendingIntentConflict(req, err, out)
			return req, agents, conflict, false, ErrRefused
		}
		out.Outcome, out.Reason = "invalid", err.Error()
		return req, agents, out, false, ErrRefused
	}
	return req, agents, out, len(agents) > 0, nil
}

func restoreOmittedFromIntent(req Request, agents []portable.Integration, intent portablesetup.Intent) (Request, []portable.Integration, error) {
	intentAgents := intentClients(intent)
	if len(agents) == 0 {
		if len(intentAgents) == 0 {
			return req, agents, nil
		}
		req.Agents = intentAgents
		next, err := normalizeAgents(intentAgents)
		if err != nil {
			return req, agents, err
		}
		agents = next
	} else if len(intentAgents) > 0 {
		names := make([]string, len(agents))
		for i, agent := range agents {
			names[i] = string(agent)
		}
		if !sameStringSet(names, intentAgents) {
			return req, agents, portablesetup.ErrIntentConflict
		}
	}
	if req.PackageSHA256 == "" {
		req.PackageSHA256 = intent.SourceDigest
	} else if intent.SourceDigest != "" && req.PackageSHA256 != intent.SourceDigest {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = intent.SourceRevision
	} else if intent.SourceRevision != "" && req.ReleaseVersion != intent.SourceRevision {
		return req, agents, portablesetup.ErrIntentConflict
	}
	for _, target := range intent.Targets {
		if target.InstallationID != "" {
			if req.InstallationID == "" {
				req.InstallationID = target.InstallationID
			} else if req.InstallationID != target.InstallationID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.Profile == "" {
			continue
		}
		switch target.Client {
		case "codex":
			if req.CodexHome == "" {
				req.CodexHome = target.Profile
			} else if req.CodexHome != target.Profile {
				return req, agents, portablesetup.ErrIntentConflict
			}
		case "claude":
			if req.ClaudeConfig == "" {
				req.ClaudeConfig = target.Profile
			} else if req.ClaudeConfig != target.Profile {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
	}
	req, err := restoreUnitsFromIntent(req, agents, intent)
	if err != nil {
		return req, agents, err
	}
	return req, agents, nil
}

func restoreUnitsFromIntent(req Request, agents []portable.Integration, intent portablesetup.Intent) (Request, error) {
	hooks, notify, specified := intentUnitSelection(intent)
	if !specified {
		return req, nil
	}
	if unitFlagsOmitted(req) {
		req.Hooks = boolPtr(hooks)
		req.AgentNotify = boolPtr(notify)
		return req, nil
	}
	gotHooks, gotNotify := selectedUnits(req, agents)
	if hooks != (len(gotHooks) > 0) || notify != (len(gotNotify) > 0) {
		return req, portablesetup.ErrIntentConflict
	}
	return req, nil
}

func intentClients(intent portablesetup.Intent) []string {
	var out []string
	seen := map[string]bool{}
	for _, target := range intent.Targets {
		if target.Client == "" || seen[target.Client] {
			continue
		}
		seen[target.Client] = true
		out = append(out, target.Client)
	}
	return out
}

func intentUnitSelection(intent portablesetup.Intent) (hooks, notify, specified bool) {
	for _, target := range intent.Targets {
		if len(target.Units) > 0 {
			specified = true
		}
		for _, unit := range target.Units {
			switch unit {
			case "hooks":
				hooks = true
			case "direct-mcp", "agent-notify", "mcp", "skills":
				notify = true
			}
		}
	}
	return hooks, notify, specified
}

func unitFlagsOmitted(req Request) bool {
	return req.Hooks == nil && req.AgentNotify == nil && req.ClaudeHooks == nil && req.CodexHooks == nil && req.ClaudeAgentNotify == nil && req.CodexAgentNotify == nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, item := range a {
		seen[item]++
	}
	for _, item := range b {
		if seen[item] == 0 {
			return false
		}
		seen[item]--
	}
	return true
}

func boolPtr(v bool) *bool {
	return &v
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
	view, err := inspectUAPState(ctx, req)
	if err != nil {
		out.Outcome, out.Reason = "incomplete", "recovery_required"
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: err.Error()})
		return out, err
	}
	if view.Recovery.Required {
		out.Outcome, out.Reason = "incomplete", "recovery_required"
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "recover", Reason: strings.Join(recoveryIDs(view), ","),
		})
	}
	return out, nil
}

func install(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, out Result) (Result, error) {
	var releasePackage func()
	defer func() {
		if releasePackage != nil {
			releasePackage()
		}
	}()
	id := portablesetup.Identity{}
	mat := portablesetup.Materializer{}
	if len(notifyAgents) > 0 {
		recordedPath, recordedVersion := desiredPackage(req)
		reportProgress(req, "prepare")
		if !explicitAbs(req.PackageRoot) && recordedPath == "" && recordedVersion != "" && req.ReleaseDownloadRoot == "" && req.PackageFetcher == nil {
			out.Outcome, out.Reason = "incomplete", "recorded_package_unavailable"
			return out, ErrRefused
		}
		packageRoot, release, err := resolvePackageRoot(ctx, req, recordedPath, recordedVersion)
		if err != nil {
			reason := "package_acquisition_failed"
			if strings.Contains(err.Error(), "package_required") {
				reason = "package_required"
			}
			out.Outcome, out.Reason = "incomplete", reason
			return out, err
		}
		releasePackage = release
		req.PackageRoot = packageRoot
		mat, err = materializer(req, snap, runtimeRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		id, err = identity(req, snap, runtimeRoot, mat, true)
		if err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		req.InstallationID = id.InstallationID
		for _, agent := range notifyAgents {
			if !explicitAbs(clientConfig(req, agent)) {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
				out.Outcome, out.Reason = "incomplete", "client_config_required"
				return out, ErrRefused
			}
			if !explicitAbs(clientExecutable(req, agent)) {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_executable_required"})
				out.Outcome, out.Reason = "incomplete", "client_executable_required"
				return out, ErrRefused
			}
			materialize := portablesetup.MaterializeRequest{
				Identity: id, Integration: agent, ExpectedGeneration: snap.Ledger.Generation,
				PackageRoot: req.PackageRoot, ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
				SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
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
		}
	}
	reportProgress(req, "preflight")
	snap, err := publishWizardIntent(ctx, req, snap, runtimeRoot, hookAgents, notifyAgents, true)
	if err != nil {
		if conflict, handled := pendingIntentConflict(req, err, out); handled {
			return conflict, err
		}
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	out.Generation = snap.Ledger.Generation
	if len(hookAgents) > 0 {
		reportProgress(req, "hooks")
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
		reportProgress(req, "complete")
		return out, nil
	}
	reportProgress(req, "agent-notify")
	generation := snap.Ledger.Generation
	for _, agent := range notifyAgents {
		materialize := portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			PackageRoot: req.PackageRoot, ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
			SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
			Discovery:       discovery(req, agent, runtimeRoot, snap),
			OperationID:     "wizard-install-" + string(agent),
			KeepReservation: true,
		}
		got, err := mat.Install(ctx, materialize)
		if err != nil {
			if portablesetup.IsUpdateRequired(err) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
				return updateRequired(req, agent, others, out, err)
			}
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			return portableInstallFailed(agent, req, out, err), err
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
	reportProgress(req, "complete")
	return out, nil
}

func uninstall(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, out Result) (Result, error) {
	mat := portablesetup.Materializer{}
	id := portablesetup.Identity{}
	if len(notifyAgents) > 0 {
		var err error
		mat, err = materializer(req, snap, runtimeRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		id, err = identity(req, snap, runtimeRoot, mat, false)
		if err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
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
		req.InstallationID = id.InstallationID
	}
	retainedEmpty := false
	if id.InstallationID != "" {
		var emptyErr error
		retainedEmpty, emptyErr = mat.RetainedEmpty(id.InstallationID)
		if emptyErr != nil {
			out.Outcome, out.Reason = "incomplete", emptyErr.Error()
			return out, emptyErr
		}
	}
	if unitFlagsOmitted(req) {
		var managedHooks []portable.Integration
		for _, agent := range hookAgents {
			if hooksManaged(req, agent) {
				managedHooks = append(managedHooks, agent)
			}
		}
		hookAgents = managedHooks
		if id.InstallationID != "" && !retainedEmpty {
			var managedNotify []portable.Integration
			for _, agent := range notifyAgents {
				if liveNotifyClient(mat, id.InstallationID, string(agent)) {
					managedNotify = append(managedNotify, agent)
				}
			}
			notifyAgents = managedNotify
		}
		if len(hookAgents) == 0 && len(notifyAgents) == 0 {
			out.Outcome, out.Reason = "cancelled", "empty_units"
			reportProgress(req, "complete")
			return out, nil
		}
	}
	portablePresent := id.InstallationID != "" && !retainedEmpty
	if portablePresent {
		for _, agent := range notifyAgents {
			if !explicitAbs(clientConfig(req, agent)) {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_config_required"})
				out.Outcome, out.Reason = "incomplete", "client_config_required"
				return out, ErrRefused
			}
			if !explicitAbs(clientExecutable(req, agent)) {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "client_executable_required"})
				out.Outcome, out.Reason = "incomplete", "client_executable_required"
				return out, ErrRefused
			}
		}
	}
	reportProgress(req, "preflight")
	snap, err := publishWizardIntent(ctx, req, snap, runtimeRoot, hookAgents, notifyAgents, portablePresent)
	if err != nil {
		if conflict, handled := pendingIntentConflict(req, err, out); handled {
			return conflict, err
		}
		out.Outcome, out.Reason = "incomplete", err.Error()
		return out, err
	}
	out.Generation = snap.Ledger.Generation
	if len(hookAgents) > 0 {
		reportProgress(req, "hooks")
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
		reportProgress(req, "complete")
		return out, nil
	}
	if id.InstallationID == "" {
		out.Outcome, out.Reason = "unchanged", "portable_absent"
		reportProgress(req, "complete")
		return out, nil
	}
	if retainedEmpty {
		for _, agent := range notifyAgents {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
		}
		if out.Outcome == "" {
			out.Outcome, out.Reason = "unchanged", "already_absent"
		}
		reportProgress(req, "complete")
		return out, nil
	}
	reportProgress(req, "agent-notify")
	generation := snap.Ledger.Generation
	removed := 0
	for _, agent := range notifyAgents {
		if agent == portable.Codex && !req.ExternalUninstalled {
			if attestCodexExternalUninstall(ctx, clientExecutable(req, agent), req.CodexHome) {
				req.ExternalUninstalled = true
			}
		}
		remove := portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
			// User uninstall does not restore a retired direct MCP.
			OperationID: "wizard-remove-" + string(agent), ExternalUninstalled: req.ExternalUninstalled,
			HoldOnly:        agent == portable.Codex && !req.ExternalUninstalled,
			KeepReservation: true,
		}
		err := mat.Remove(ctx, remove)
		if err != nil {
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			if errors.Is(err, portablesetup.ErrAlreadyAbsent) {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
				continue
			}
			if errors.Is(err, portablesetup.ErrExternalUninstall) {
				retry := req
				retry.ExternalUninstalled = true
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "external_uninstall_required"})
				out.Outcome, out.Reason = "incomplete", "external_uninstall_required"
				out.Command = RetryCommand(retry)
				out.NextActions = []NextAction{{
					Kind: "external-uninstall", Agents: []string{string(agent)},
					Command: RetryCommand(retry), Reason: "attest_codex_plugin_removed",
				}}
				return out, err
			}
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
		if out.Reason == "" {
			out.Reason = "already_absent"
		}
		out.Outcome = "unchanged"
		reportProgress(req, "complete")
		return out, nil
	}
	out.Outcome = "completed"
	reportProgress(req, "complete")
	return out, nil
}

func portableInstallFailed(agent portable.Integration, req Request, out Result, err error) Result {
	out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
	out.Outcome, out.Reason = "incomplete", "portable_install_failed"
	var persisted portablesetup.ResultError
	if errors.As(err, &persisted) && persisted.Result.Client.Materialization != "" && persisted.Result.Client.Materialization != string(domain.MaterializationAbsent) {
		retry := req
		retry.Action = ActionInstall
		retry.Yes = true
		if explicitAbs(req.ControlRoot) {
			if intent, readErr := portablesetup.ReadIntent(req.ControlRoot); readErr == nil {
				retry = retryRequestFromIntent(retry, intent)
			}
		}
		out.Reason = "activation_incomplete"
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "activate", Agents: []string{string(agent)}, Reason: persisted.Result.Reason,
			Command: RetryCommand(retry),
		})
	}
	return out
}

func pendingIntentConflict(req Request, err error, out Result) (Result, bool) {
	if !errors.Is(err, portablesetup.ErrIntentConflict) {
		return out, false
	}
	out.Outcome, out.Reason = "conflict", "pending_intent_conflict"
	intent, readErr := portablesetup.ReadIntent(req.ControlRoot)
	if readErr != nil {
		return out, true
	}
	out.Command = RetryCommand(retryRequestFromIntent(req, intent))
	return out, true
}

func retryRequestFromIntent(req Request, intent portablesetup.Intent) Request {
	retry := req
	retry.Action = Action(intent.Action)
	if clients := intentClients(intent); len(clients) > 0 {
		retry.Agents = clients
	}
	if intent.SourceDigest != "" {
		retry.PackageSHA256 = intent.SourceDigest
	}
	if intent.SourceRevision != "" {
		retry.ReleaseVersion = intent.SourceRevision
	}
	if hooks, notify, specified := intentUnitSelection(intent); specified {
		retry.Hooks = boolPtr(hooks)
		retry.AgentNotify = boolPtr(notify)
		retry.ClaudeHooks, retry.CodexHooks = nil, nil
		retry.ClaudeAgentNotify, retry.CodexAgentNotify = nil, nil
	}
	for _, target := range intent.Targets {
		if target.InstallationID != "" {
			retry.InstallationID = target.InstallationID
		}
		switch target.Client {
		case "codex":
			if target.Profile != "" {
				retry.CodexHome = target.Profile
			}
		case "claude":
			if target.Profile != "" {
				retry.ClaudeConfig = target.Profile
			}
		}
	}
	retry.Yes = true
	return retry
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
		switch len(state.Installations) {
		case 1:
			id = state.Installations[0].InstallationID
		case 0:
			if generate {
				generated, err := domain.NewInstallationID()
				if err != nil {
					return portablesetup.Identity{}, err
				}
				id = generated
			}
		default:
			return portablesetup.Identity{}, fmt.Errorf("%w: %d installations; pass --installation-id", ErrAmbiguousInstallation, len(state.Installations))
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

func mapAmbiguous(err error, out Result) (Result, bool) {
	if !errors.Is(err, ErrAmbiguousInstallation) {
		return out, false
	}
	out.Outcome, out.Reason = "conflict", "ambiguous_installation"
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "inspect", Reason: err.Error(),
		Command: []string{"setup-notifications", "wizard", "--action", "inspect", "--json"},
	})
	return out, true
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

func wizardWillMutate(hookAgents, notifyAgents []portable.Integration, portablePresent bool, install bool) bool {
	for _, agent := range hookAgents {
		if agent == portable.Codex {
			return true
		}
	}
	if len(notifyAgents) == 0 {
		return false
	}
	if install {
		return true
	}
	return portablePresent
}

func wizardIntentTargets(req Request, hookAgents, notifyAgents []portable.Integration) []portablesetup.IntentTarget {
	byClient := map[string]*portablesetup.IntentTarget{}
	var order []string
	add := func(agent portable.Integration, unit string) {
		id := string(agent)
		target, ok := byClient[id]
		if !ok {
			target = &portablesetup.IntentTarget{
				Client:         id,
				InstallationID: req.InstallationID,
				Profile:        clientConfig(req, agent),
			}
			byClient[id] = target
			order = append(order, id)
		}
		target.Units = append(target.Units, unit)
	}
	for _, agent := range hookAgents {
		add(agent, "hooks")
	}
	for _, agent := range notifyAgents {
		add(agent, "agent-notify")
	}
	out := make([]portablesetup.IntentTarget, 0, len(order))
	for _, id := range order {
		out = append(out, *byClient[id])
	}
	return out
}

func publishWizardIntent(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, portablePresent bool) (installruntime.InstalledSnapshot, error) {
	if snap.Ledger.PendingMutation != nil {
		return snap, nil
	}
	if !wizardWillMutate(hookAgents, notifyAgents, portablePresent, req.Action == ActionInstall) {
		return snap, nil
	}
	targets := wizardIntentTargets(req, hookAgents, notifyAgents)
	if len(targets) == 0 {
		return snap, nil
	}
	if _, _, err := (portablesetup.Service{}).PublishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{
		ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner,
		ExpectedGeneration: snap.Ledger.Generation, Action: string(req.Action), Stage: "confirmed",
		SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256, Targets: targets,
	}); err != nil {
		return snap, err
	}
	return installruntime.ReadInstalledSnapshot(req.ControlRoot)
}

func finishWizardIntent(ctx context.Context, req Request, runtimeRoot string, out Result, err error) (Result, error) {
	if out.Outcome != "completed" && out.Outcome != "unchanged" {
		return out, err
	}
	snap, readErr := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if readErr != nil {
		out.Outcome, out.Reason = "incomplete", readErr.Error()
		return out, readErr
	}
	if snap.Ledger.PendingMutation == nil {
		return out, err
	}
	cp := *snap.Ledger.PendingMutation
	if finishErr := (portablesetup.Service{}).FinishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{
		ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner,
	}, &cp); finishErr != nil {
		out.Outcome, out.Reason = "incomplete", "intent_cleanup_failed"
		return out, finishErr
	}
	if gen, readErr := rereadGeneration(req.ControlRoot); readErr == nil {
		out.Generation = gen
	}
	return out, err
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

func attachReadiness(req Request, agents []portable.Integration, out Result, mutationInstall bool) Result {
	if out.Outcome == "invalid" || out.Outcome == "cancelled" {
		return out
	}
	runtime := "installed"
	if out.Reason == "managed_runtime_required" {
		runtime = "absent"
	}
	restartPending := false
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
			restartPending = true
		}
		out.Readiness = append(out.Readiness, fact)
	}
	if out.Outcome == "completed" || out.Outcome == "unchanged" {
		out = offerPostSetupActions(req, agents, out, restartPending)
	}
	return out
}

func offerPostSetupActions(req Request, agents []portable.Integration, out Result, restartPending bool) Result {
	if req.Action == ActionUninstall {
		return out
	}
	names := make([]string, len(agents))
	for i, agent := range agents {
		names[i] = string(agent)
	}
	has := func(kind string) bool {
		for _, next := range out.NextActions {
			if next.Kind == kind {
				return true
			}
		}
		return false
	}
	if restartPending && !has("restart-client") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "restart-client", Agents: names, Reason: "pending_client_restart",
		})
	}
	if req.Action != ActionInspect && explicitAbs(req.ControlRoot) && out.Generation != 0 && !has("request-permission") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind:   "request-permission",
			Agents: names,
			Command: []string{
				"setup-notifications", "request-permission",
				"--control-root", req.ControlRoot,
				"--expected-generation", fmt.Sprintf("%d", out.Generation),
			},
			Reason: "permission_explicit_phase",
		})
	}
	if !has("test-notification") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "test-notification", Agents: names, Command: []string{"notify"},
			Reason: "delivery_not_verified",
		})
	}
	return out
}

func reportProgress(req Request, phase string) {
	if req.Progress != nil {
		req.Progress(phase)
	}
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}
