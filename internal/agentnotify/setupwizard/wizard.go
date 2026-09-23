// Package setupwizard is the P6 application service for setup-notifications wizard.
// Run does not prompt. FillInteractive is a thin TTY adapter that only fills Request.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/shared"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/ports"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
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
	EnvCodexHome, EnvClaudeConfig         string
	ClientExecutable, ScopeRoot, Helper   string
	ClientExecutables                     map[string]string
	PackageSHA256                         string
	// TreeDigest is the canonical package-tree digest from Prepare. It is
	// distinct from PackageSHA256 (archive bytes).
	TreeDigest, HelperDigest, HelperVersion string
	InstallationID, Primary                 string
	// BindingIDs are reserved per client during Plan/identity without
	// staging. Run and the durable intent reuse them.
	BindingIDs map[string]string
	// DataReceiptIDs are known UAP PLUGIN_DATA receipts. Uninstall/publish
	// copies live receipts onto the durable intent; resume rejects a different ID.
	DataReceiptIDs                      map[string]string
	MCPConfig                           map[string]string
	ClaudeHooks, CodexHooks             *bool
	ClaudeAgentNotify, CodexAgentNotify *bool
	// ExternalUninstalled is host attestation that Codex already removed the
	// native plugin, or never activated it. --yes does not set this.
	ExternalUninstalled bool
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
	// DiscoverAgents supplies Claude/Codex executable presence for the TTY
	// picker. Production sets this from Engine.Discover. Nil skips presence
	// labels. The function must not execute found files.
	DiscoverAgents func() []AgentCapability
	// LiveUnits reports managed hooks/notify for the TTY mixed-opt-out
	// display. Nil uses LiveClientUnits from control-root state.
	LiveUnits func([]string) []ClientUnits
}

type TargetResult struct {
	Client     string `json:"client"`
	Unit       string `json:"unit"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	Profile    string `json:"profile,omitempty"`
	TreeDigest string `json:"treeDigest,omitempty"`
	// ConfigPath is the owned MCP file inspect used for a direct-mcp
	// target. Empty on hooks/notify rows so JSON omits it.
	ConfigPath string `json:"configPath,omitempty"`
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
	Action         string          `json:"action"`
	Outcome        string          `json:"outcome"`
	Reason         string          `json:"reason,omitempty"`
	InstallationID string          `json:"installationID,omitempty"`
	Generation     uint64          `json:"generation,omitempty"`
	Command        []string        `json:"command,omitempty"`
	Targets        []TargetResult  `json:"targets,omitempty"`
	Readiness      []ReadinessFact `json:"readiness,omitempty"`
	NextActions    []NextAction    `json:"nextActions,omitempty"`
	// DataRetained is true after the last live binding is removed while
	// PLUGIN_DATA remains. Absent inspect rows are not a license to run.
	DataRetained bool `json:"dataRetained,omitempty"`
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
	if req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair {
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
		}
		var recoveryPending bool
		text, recoveryPending = annotatePendingRecovery(ctx, req, ev.snap, text, &ev.out)
		if len(ev.notifyAgents) > 0 && !recoveryPending {
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
			mat, err := materializer(acquired, ev.snap, ev.runtimeRoot)
			if err != nil {
				ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
				plan.Result = attachCommand(req, ev.out)
				return plan, err
			}
			id, err := identity(acquired, ev.snap, ev.runtimeRoot, mat, req.Action == ActionInstall)
			if err != nil {
				if mapped, handled := mapAmbiguous(err, ev.out); handled {
					plan.Result = attachCommand(req, mapped)
					return plan, err
				}
				ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
				plan.Result = attachCommand(req, ev.out)
				return plan, err
			}
			req.InstallationID = id.InstallationID
			req.GlobalConfig = id.GlobalConfig
			acquired.InstallationID = id.InstallationID
			if req.Action == ActionUpdate || req.Action == ActionRepair {
				if mapped, bindErr := requireLiveNotifyBindings(req, mat, id, ev.notifyAgents, ev.out); bindErr != nil {
					plan.Result = attachCommand(req, mapped)
					return plan, bindErr
				}
			}
			if req.Action == ActionRepair {
				bindRepairPackages(ctx, &acquired, ev.snap, ev.runtimeRoot, ev.notifyAgents)
				req.PackageRoots = copyPackageRoots(acquired.PackageRoots)
			}
			if !retainedMetadataUpdate(mat, id, req.Action) {
				if err := reserveClientBindings(&req, mat, ev.notifyAgents); err != nil {
					if mapped, handled := mapAmbiguous(err, ev.out); handled {
						plan.Result = attachCommand(req, mapped)
						return plan, err
					}
					ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
					plan.Result = attachCommand(req, ev.out)
					return plan, err
				}
			}
			acquired.BindingIDs = copyBindingIDs(req.BindingIDs)
			plan.Request = req
			text += " installation-id=" + id.InstallationID
			for _, agent := range ev.notifyAgents {
				if bid := req.BindingIDs[string(agent)]; bid != "" {
					text += " " + string(agent) + "-binding-id=" + bid
				}
			}
			if retainedMetadataUpdate(mat, id, req.Action) {
				if conflict := retainedPackageIdentityConflict(req.ControlRoot, id.InstallationID, acquired.PackageRoot); conflict {
					ev.out.Outcome, ev.out.Reason = "conflict", "package_identity"
					plan.Result = attachCommand(req, ev.out)
					plan.Text = text + " package-identity-conflict"
					return plan, ErrRefused
				}
				digest, err := retainedUpdateTreeDigest(ctx, mat, acquired.PackageRoot)
				if err != nil {
					ev.out.Outcome, ev.out.Reason = "incomplete", err.Error()
					plan.Result = attachCommand(req, ev.out)
					return plan, err
				}
				if err := bindIdentityField(&req.TreeDigest, digest); err != nil {
					ev.out.Outcome, ev.out.Reason = "incomplete", "source_identity_drift"
					plan.Result = attachCommand(req, ev.out)
					return plan, err
				}
				text = annotateRetainedMetadataUpdate(text, ev.notifyAgents)
				ev.out.NextActions = append(ev.out.NextActions, NextAction{
					Kind: "data_compatibility", Reason: domain.PluginDataCompatibilityWarning,
				})
			} else {
				failed, reason, err := bindNotifyPreviews(ctx, &req, acquired, ev.snap, ev.runtimeRoot, ev.notifyAgents)
				if err != nil {
					if reason == "" {
						mapped, mappedErr := mapPreviewFailure(ctx, req, failed, mat, id, err, ev.out)
						plan.Text = annotateRequiredUpdate(text, mapped)
						if retained, emptyErr := mat.RetainedEmpty(id.InstallationID); emptyErr == nil && retained {
							plan.Text += " data_retained=true data-compatibility-warning"
						}
						plan.Result = attachCommand(req, mapped)
						return plan, mappedErr
					}
					ev.out.Outcome, ev.out.Reason = "incomplete", reason
					plan.Result = attachCommand(req, ev.out)
					return plan, err
				}
			}
			if req.Action == ActionUpdate || req.Action == ActionRepair {
				for _, agent := range ev.notifyAgents {
					if digest := liveBindingTreeDigest(mat, id.InstallationID, string(agent)); digest != "" {
						text += " " + string(agent) + "-source-digest=" + digest
					}
				}
			}
			if req.TreeDigest != "" {
				text += " source-digest=" + req.TreeDigest
			}
			if req.HelperDigest != "" {
				text += " helper-digest=" + req.HelperDigest
			}
			if req.HelperVersion != "" {
				text += " helper-version=" + req.HelperVersion
			}
			acquired.TreeDigest = req.TreeDigest
			acquired.HelperDigest = req.HelperDigest
			acquired.HelperVersion = req.HelperVersion
		}
	} else {
		text, _ = annotatePendingRecovery(ctx, req, ev.snap, text, &ev.out)
	}
	if req.Action == ActionUninstall {
		if prereqs := uninstallManualPrerequisites(ctx, req, ev.snap, ev.runtimeRoot, ev.notifyAgents); len(prereqs) > 0 {
			text = strings.Replace(text, "required=none", "required=external-uninstall", 1)
			text += " required-external-uninstall=" + strings.Join(prereqs, ",")
			ev.out.NextActions = append(ev.out.NextActions, NextAction{
				Kind: "external-uninstall", Agents: prereqs, Reason: "attest_codex_plugin_removed",
			})
		}
	}
	ev.out.Outcome, ev.out.Reason = "ready", ""
	ev.out.InstallationID = req.InstallationID
	plan.Ready = true
	plan.Text = text
	plan.Request = req
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
	if req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair {
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
	case ActionInstall, ActionUninstall, ActionInspect, ActionUpdate, ActionRepair:
	default:
		out.Outcome, out.Reason = "invalid", "invalid_action"
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	agents, err := normalizeAgents(req.Agents)
	if err != nil {
		out.Outcome, out.Reason = "invalid", err.Error()
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	// Inspect with omitted --agents reports both wizard clients (§9.2).
	if req.Action == ActionInspect && len(agents) == 0 {
		agents = []portable.Integration{portable.Claude, portable.Codex}
		req.Agents = []string{string(portable.Claude), string(portable.Codex)}
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
	applyHostSnapshots(req)
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
		if req.RuntimeRoot == "" && snap.Ledger.RuntimeRoot == "" {
			out.Outcome, out.Reason = "incomplete", "managed_runtime_required"
			return evaluated{out: out, err: err, stop: true}
		}
		out.Outcome, out.Reason = "invalid", "runtime_root_required"
		return evaluated{out: out, err: ErrRefused, stop: true}
	}
	if len(agents) == 0 {
		// TTY empty choice is canceled in FillInteractive before Run.
		// Noninteractive mutation needs explicit --agents, or a matching
		// pending intent that already restored them (§9.2).
		out.Outcome, out.Reason = "invalid", "agents_required"
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, err: ErrRefused, stop: true}
	}
	bindDiscoveredMCP(req, agents)
	if req.Action == ActionInspect {
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out}
	}
	if req.Action == ActionUpdate || req.Action == ActionRepair {
		*req = preserveLiveUnits(ctx, *req, agents)
	}
	hookAgents, notifyAgents := selectedUnits(*req, agents)
	if len(hookAgents) == 0 && len(notifyAgents) == 0 {
		out.Outcome, out.Reason = "cancelled", "empty_units"
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, stop: true}
	}
	if agent, missing := repairRequestsMissingHooks(*req, hookAgents); missing {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "hooks", Outcome: "incomplete", Reason: "install_required"})
		out.Outcome, out.Reason = "incomplete", "install_required"
		installReq := *req
		installReq.Action = ActionInstall
		out.NextActions = []NextAction{{
			Kind: "install", Agents: []string{string(agent)}, Command: RetryCommand(installReq), Reason: "repair_does_not_add_units",
		}}
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, err: ErrRefused, stop: true}
	}
	return evaluated{
		agents: agents, snap: snap, runtimeRoot: runtimeRoot,
		hookAgents: hookAgents, notifyAgents: notifyAgents, out: out,
	}
}

func liveBindingTreeDigest(mat portablesetup.Materializer, installationID, clientID string) string {
	if installationID == "" || clientID == "" {
		return ""
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID && binding.PackageRevision != nil {
				return binding.PackageRevision.TreeDigest
			}
		}
	}
	return ""
}

func completedNotifyTarget(mat portablesetup.Materializer, installationID, client, bindingID string) TargetResult {
	return TargetResult{
		Client:     client,
		Unit:       "agent-notify",
		Outcome:    "completed",
		Reason:     bindingID,
		TreeDigest: liveBindingTreeDigest(mat, installationID, client),
	}
}

func sameLiveRepairRevision(mat portablesetup.Materializer, installationID string, agents []portable.Integration) bool {
	digest := ""
	for i, agent := range agents {
		got := liveBindingTreeDigest(mat, installationID, string(agent))
		if got == "" {
			return false
		}
		if i == 0 {
			digest = got
			continue
		}
		if got != digest {
			return false
		}
	}
	return digest != ""
}

func exactRepairRevision(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "repair must use the exact applied package revision") ||
		strings.Contains(msg, "resolved repair package differs from the installed revision")
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

// liveManagedTargetsPresent is true when every selected live binding still has
// its managed artifact. Missing targets skip ApplyGroup Repair so sequential
// Repair can rematerialize without the unset NativeObserver sibling check.
func liveManagedTargetsPresent(mat portablesetup.Materializer, installationID string, agents []portable.Integration) bool {
	if installationID == "" || len(agents) == 0 {
		return false
	}
	state, err := mat.Store.Load()
	if err != nil {
		return false
	}
	for _, agent := range agents {
		found := false
		for _, installation := range state.Installations {
			if installation.InstallationID != installationID {
				continue
			}
			for _, binding := range installation.Clients {
				if binding.ClientID != string(agent) {
					continue
				}
				if binding.TargetLocator == "" {
					return false
				}
				if _, err := os.Lstat(binding.TargetLocator); err != nil {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func holdCodexUninstall(ctx context.Context, req *Request, mat portablesetup.Materializer, id portablesetup.Identity, generation uint64, notifyAgents []portable.Integration, runtimeRoot string, out Result) (Result, error) {
	if req.ExternalUninstalled || id.InstallationID == "" {
		return out, nil
	}
	for _, agent := range notifyAgents {
		if agent != portable.Codex || !liveNotifyClient(mat, id.InstallationID, string(agent)) {
			continue
		}
		if attestCodexExternalUninstall(ctx, clientExecutable(*req, agent), req.CodexHome, managedCodexPluginSpec(mat, id.InstallationID)) {
			req.ExternalUninstalled = true
			_ = persistExternalUninstalled(ctx, *req, runtimeRoot)
			return out, nil
		}
		err := mat.Remove(ctx, portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: clientConfig(*req, agent), ClientExecutable: clientExecutable(*req, agent),
			OperationID: wizardMutationID(ActionUninstall, agent, generation), ExternalUninstalled: req.ExternalUninstalled,
			HoldOnly: true, KeepReservation: true,
		})
		if err == nil {
			return out, nil
		}
		if conflict, handled := pendingIntentConflict(*req, err, out); handled {
			return conflict, err
		}
		if errors.Is(err, portablesetup.ErrAlreadyAbsent) {
			return out, nil
		}
		if errors.Is(err, portablesetup.ErrExternalUninstall) {
			return externalUninstallRequired(*req, agent, out), err
		}
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
		out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
		return out, err
	}
	return out, nil
}

func externalUninstallRequired(req Request, agent portable.Integration, out Result) Result {
	retry := req
	retry.ExternalUninstalled = true
	out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "external_uninstall_required"})
	out.Outcome, out.Reason = "incomplete", "external_uninstall_required"
	out.Command = RetryCommand(retry)
	out.NextActions = []NextAction{{
		Kind: "external-uninstall", Agents: []string{string(agent)},
		Command: RetryCommand(retry), Reason: "attest_codex_plugin_removed",
	}}
	return out
}

func uninstallManualPrerequisites(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, notifyAgents []portable.Integration) []string {
	if req.ExternalUninstalled {
		return nil
	}
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return nil
	}
	id, err := identity(req, snap, runtimeRoot, mat, false)
	if err != nil {
		return nil
	}
	if id.InstallationID == "" {
		state, loadErr := mat.Store.Load()
		if loadErr != nil || len(state.Installations) != 1 {
			return nil
		}
		id.InstallationID = state.Installations[0].InstallationID
	}
	var prereqs []string
	expectedSpec := managedCodexPluginSpec(mat, id.InstallationID)
	for _, agent := range notifyAgents {
		if agent != portable.Codex || !liveNotifyClient(mat, id.InstallationID, string(agent)) {
			continue
		}
		status := codexListUnknown
		if explicitAbs(clientExecutable(req, agent)) {
			status, _ = observeCodexPluginList(ctx, clientExecutable(req, agent), clientConfig(req, agent), expectedSpec)
		}
		if status != codexListAbsent {
			prereqs = append(prereqs, string(agent))
		}
	}
	return prereqs
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
	if err := guardLiveProfile(mat, id.InstallationID, string(agent), clientConfig(req, agent)); err != nil {
		return uapinstaller.Plan{}, err
	}
	return mat.PreviewPlan(ctx, portablesetup.MaterializeRequest{
		Identity: id, Integration: agent, PackageRoot: clientPackageRoot(req, agent),
		ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
		OperationID: wizardMutationID("plan-"+req.Action, agent, 0), Operation: wizardPackageOp(req.Action),
	})
}

func wizardMutationID(action Action, agent portable.Integration, generation uint64) string {
	if action == "" {
		action = ActionInstall
	}
	return fmt.Sprintf("wizard-%s-%s-%d", action, agent, generation)
}

func wizardPackageOp(action Action) uapinstaller.Operation {
	switch action {
	case ActionUpdate:
		return uapinstaller.OpUpdate
	case ActionRepair:
		return uapinstaller.OpRepair
	default:
		return uapinstaller.OpInstall
	}
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
	registry, err := portablesetup.NewRegistry()
	if err != nil {
		return uapinstaller.Inspection{}, err
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: stateRoot, Registry: registry})
	if err != nil {
		return uapinstaller.Inspection{}, err
	}
	return eng.Inspect(ctx)
}

// annotatePendingRecovery reports kernel and UAP recovery without recovering.
// Plan skips source-dependent Prepare while a journal is pending (§7.4.2, §9.2).
func annotatePendingRecovery(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, text string, out *Result) (string, bool) {
	if out == nil {
		return text, false
	}
	pending := false
	if snap.Recovery {
		text += " recovery-pending=kernel-journal"
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: "kernel-journal"})
		pending = true
	}
	view, err := inspectUAPState(ctx, req)
	if err != nil || !view.Recovery.Required {
		return text, pending
	}
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
	out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: reason})
	return text, true
}

func recoverWizardJournals(ctx context.Context, mat portablesetup.Materializer, id portablesetup.Identity, req Request, agents []portable.Integration) error {
	if len(agents) == 0 {
		return nil
	}
	agent := agents[0]
	return mat.RecoverJournals(ctx, portablesetup.MaterializeRequest{
		Identity:         id,
		Integration:      agent,
		PackageRoot:      req.PackageRoot,
		ClientConfigRoot: clientConfig(req, agent),
		ClientExecutable: clientExecutable(req, agent),
		HelperExecutable: req.Helper,
	})
}

// DiscoverAgents reports Claude/Codex user-scope metadata and executable
// presence without creating UAP state or executing found files.
func DiscoverAgents(req Request) []AgentCapability {
	stateRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state")
	if !explicitAbs(req.ControlRoot) {
		stateRoot = filepath.Join(os.TempDir(), "uapinstaller-discover-absent")
	}
	registry, err := portablesetup.NewRegistry()
	if err != nil {
		return nil
	}
	eng, err := uapinstaller.New(uapinstaller.Config{
		StateRoot: stateRoot, ClientExecutables: req.ClientExecutables, Registry: registry,
	})
	if err != nil {
		return nil
	}
	found := eng.Discover()
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].ClientID == found[j].ClientID {
			return false
		}
		return found[i].ClientID == "claude"
	})
	out := make([]AgentCapability, 0, len(found))
	for _, item := range found {
		out = append(out, AgentCapability{
			ID: item.ClientID, Present: item.ExecutablePresent, Path: item.ExecutablePath,
			Bound: len(item.Bindings) > 0, Profile: firstBindingProfile(item.Bindings),
		})
	}
	return out
}

func firstBindingProfile(bindings []uapinstaller.InspectedBinding) string {
	for _, binding := range bindings {
		if profile, err := recordedLiveProfile(binding.DataRoot, binding.ClientID); err == nil && profile != "" {
			return profile
		}
	}
	return ""
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
	if req.TreeDigest == "" {
		req.TreeDigest = intent.TreeDigest
	} else if intent.TreeDigest != "" && req.TreeDigest != intent.TreeDigest {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.HelperDigest == "" {
		req.HelperDigest = intent.HelperDigest
	} else if intent.HelperDigest != "" && req.HelperDigest != intent.HelperDigest {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.HelperVersion == "" {
		req.HelperVersion = intent.HelperVersion
	} else if intent.HelperVersion != "" && req.HelperVersion != intent.HelperVersion {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = intent.SourceRevision
	} else if intent.SourceRevision != "" && req.ReleaseVersion != intent.SourceRevision {
		return req, agents, portablesetup.ErrIntentConflict
	}
	primary := intent.Primary
	if primary == "" {
		// Intents written before primary was persisted used the old default.
		// Keep that locator identity across an interrupted remove/retry.
		for _, target := range intent.Targets {
			for _, unit := range target.Units {
				if unit == "agent-notify" {
					primary = "primary"
				}
			}
		}
	}
	if req.Primary == "" {
		req.Primary = primary
	} else if primary != "" && req.Primary != primary {
		return req, agents, portablesetup.ErrIntentConflict
	}
	if intent.ExternalUninstalled {
		req.ExternalUninstalled = true
	}
	for _, target := range intent.Targets {
		if target.InstallationID != "" {
			if req.InstallationID == "" {
				req.InstallationID = target.InstallationID
			} else if req.InstallationID != target.InstallationID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.BindingID != "" {
			if req.BindingIDs == nil {
				req.BindingIDs = map[string]string{}
			}
			if existing := req.BindingIDs[target.Client]; existing == "" {
				req.BindingIDs[target.Client] = target.BindingID
			} else if existing != target.BindingID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.DataReceiptID != "" {
			if req.DataReceiptIDs == nil {
				req.DataReceiptIDs = map[string]string{}
			}
			if existing := req.DataReceiptIDs[target.Client]; existing == "" {
				req.DataReceiptIDs[target.Client] = target.DataReceiptID
			} else if existing != target.DataReceiptID {
				return req, agents, portablesetup.ErrIntentConflict
			}
		}
		if target.MCPConfig != "" {
			if req.MCPConfig == nil {
				req.MCPConfig = map[string]string{}
			}
			if existing := req.MCPConfig[target.Client]; existing == "" {
				req.MCPConfig[target.Client] = target.MCPConfig
			} else if existing != target.MCPConfig {
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
	want, specified := intentClientUnits(intent)
	if !specified {
		return req, nil
	}
	if unitFlagsOmitted(req) {
		return applyIntentUnits(req, want), nil
	}
	got := requestClientUnits(req, agents)
	if !sameClientUnits(want, got) {
		return req, portablesetup.ErrIntentConflict
	}
	return req, nil
}

type unitSelection struct {
	hooks, notify bool
}

func intentClientUnits(intent portablesetup.Intent) (map[string]unitSelection, bool) {
	out := map[string]unitSelection{}
	specified := false
	for _, target := range intent.Targets {
		if target.Client == "" {
			continue
		}
		if len(target.Units) > 0 {
			specified = true
		}
		sel := out[target.Client]
		for _, unit := range target.Units {
			switch unit {
			case "hooks":
				sel.hooks = true
			case "direct-mcp", "agent-notify", "mcp", "skills":
				sel.notify = true
			}
		}
		out[target.Client] = sel
	}
	return out, specified
}

func requestClientUnits(req Request, agents []portable.Integration) map[string]unitSelection {
	out := map[string]unitSelection{}
	for _, agent := range agents {
		out[string(agent)] = unitSelection{}
	}
	hooks, notify := selectedUnits(req, agents)
	for _, agent := range hooks {
		sel := out[string(agent)]
		sel.hooks = true
		out[string(agent)] = sel
	}
	for _, agent := range notify {
		sel := out[string(agent)]
		sel.notify = true
		out[string(agent)] = sel
	}
	return out
}

func sameClientUnits(want, got map[string]unitSelection) bool {
	if len(want) != len(got) {
		return false
	}
	for client, sel := range want {
		other, ok := got[client]
		if !ok || other != sel {
			return false
		}
	}
	return true
}

func unitsUniform(want map[string]unitSelection) (unitSelection, bool) {
	var first unitSelection
	seen := false
	for _, sel := range want {
		if !seen {
			first = sel
			seen = true
			continue
		}
		if sel != first {
			return unitSelection{}, false
		}
	}
	return first, seen
}

func applyIntentUnits(req Request, want map[string]unitSelection) Request {
	if uniform, ok := unitsUniform(want); ok {
		req.Hooks = boolPtr(uniform.hooks)
		req.AgentNotify = boolPtr(uniform.notify)
		req.ClaudeHooks, req.CodexHooks = nil, nil
		req.ClaudeAgentNotify, req.CodexAgentNotify = nil, nil
		return req
	}
	req.Hooks, req.AgentNotify = nil, nil
	req.ClaudeHooks, req.CodexHooks = nil, nil
	req.ClaudeAgentNotify, req.CodexAgentNotify = nil, nil
	for client, sel := range want {
		hooks, notify := boolPtr(sel.hooks), boolPtr(sel.notify)
		switch client {
		case "claude":
			req.ClaudeHooks, req.ClaudeAgentNotify = hooks, notify
		case "codex":
			req.CodexHooks, req.CodexAgentNotify = hooks, notify
		}
	}
	return req
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
				profile, _ := recordedLiveProfile(installation.DataReceipts[binding.DataReceiptID].Locator, string(agent))
				digest := installation.Source.TreeDigest
				if binding.PackageRevision != nil && binding.PackageRevision.TreeDigest != "" {
					digest = binding.PackageRevision.TreeDigest
				}
				out.Targets = append(out.Targets, TargetResult{
					Client: string(agent), Unit: "agent-notify", Outcome: "installed",
					Reason: binding.ClientBindingID, Profile: profile,
					TreeDigest: digest,
				})
			}
		}
		if !found {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "absent"})
		}
		out = inspectHooks(req, agent, out)
		mcpPath := discoveryConfigPath(req, agent)
		if mcpPath == "" {
			continue
		}
		command := discovery(req, agent, runtimeRoot, snap).Command
		facts, err := clientsetup.Inspect(ctx, clientsetup.Request{
			ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Command: command, ConfigPath: mcpPath,
			Provider: discoveryProvider(agent), Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
		})
		if err != nil {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "direct-mcp", Outcome: "unknown", Reason: err.Error(), ConfigPath: mcpPath})
			continue
		}
		outcome := "absent"
		if facts.Registered {
			outcome = "installed"
		}
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "direct-mcp", Outcome: outcome, ConfigPath: mcpPath})
	}
	if snap.Recovery {
		out.Outcome, out.Reason = "incomplete", "recovery_required"
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: "kernel-journal"})
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
	out = markInspectedIdentity(view, out)
	return reportPendingWizardIntent(req, snap, out), nil
}

func markInspectedIdentity(view uapinstaller.Inspection, out Result) Result {
	if len(view.Installations) == 1 {
		out.InstallationID = view.Installations[0].InstallationID
	}
	return markInspectedDataRetained(view, out)
}

func markInspectedDataRetained(view uapinstaller.Inspection, out Result) Result {
	for _, installation := range view.Installations {
		if installation.DataRetained {
			out.DataRetained = true
			return out
		}
	}
	return out
}

func markRetainedEmpty(mat portablesetup.Materializer, installationID string, out Result) Result {
	if installationID == "" {
		return out
	}
	retained, err := mat.RetainedEmpty(installationID)
	if err == nil && retained {
		out.DataRetained = true
	}
	return out
}

// reportPendingWizardIntent lists the persisted resume command without
// restoring omitted flags into this inspect request or running Recover.
func reportPendingWizardIntent(req Request, snap installruntime.InstalledSnapshot, out Result) Result {
	pending := snap.Ledger.PendingMutation
	if pending == nil || pending.Owner != "existing-installer" {
		return out
	}
	intent, err := portablesetup.ReadIntent(req.ControlRoot)
	if err != nil || intent.SetupIntentID != pending.ID {
		out.NextActions = append(out.NextActions, NextAction{Kind: "recover", Reason: "pending_intent_unreadable"})
		return out
	}
	clients := intentClients(intent)
	retry := retryRequestFromIntent(req, intent)
	if intent.Action == string(ActionUninstall) && !intent.ExternalUninstalled {
		for _, client := range clients {
			if client == string(portable.Codex) {
				retry.ExternalUninstalled = true
				out.NextActions = append(out.NextActions, NextAction{
					Kind: "external-uninstall", Agents: clients,
					Command: RetryCommand(retry), Reason: "attest_codex_plugin_removed",
				})
				return out
			}
		}
	}
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "resume", Agents: clients, Command: RetryCommand(retry),
		Reason: "pending_" + intent.Action,
	})
	return out
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
		id, err = identity(req, snap, runtimeRoot, mat, req.Action == ActionInstall)
		if err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		req.InstallationID = id.InstallationID
		req.GlobalConfig = id.GlobalConfig
		req.Primary = id.Primary
		out.InstallationID = id.InstallationID
		if req.Action == ActionUpdate || req.Action == ActionRepair {
			if mapped, bindErr := requireLiveNotifyBindings(req, mat, id, notifyAgents, out); bindErr != nil {
				return mapped, bindErr
			}
		}
		if req.Action == ActionRepair {
			bindRepairPackages(ctx, &req, snap, runtimeRoot, notifyAgents)
		}
		if err := recoverWizardJournals(ctx, mat, id, req, notifyAgents); err != nil {
			out.Outcome, out.Reason = "incomplete", "recovery_required"
			return out, err
		}
		if !retainedMetadataUpdate(mat, id, req.Action) {
			if err := reserveClientBindings(&req, mat, notifyAgents); err != nil {
				if mapped, handled := mapAmbiguous(err, out); handled {
					return mapped, err
				}
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
			}
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
				if err := guardLiveProfile(mat, id.InstallationID, string(agent), clientConfig(req, agent)); err != nil {
					if mapped, handled := mapAmbiguous(err, out); handled {
						return mapped, err
					}
					out.Outcome, out.Reason = "incomplete", err.Error()
					return out, err
				}
				materialize := portablesetup.MaterializeRequest{
					Identity: id, Integration: agent, ExpectedGeneration: snap.Ledger.Generation,
					PackageRoot: clientPackageRoot(req, agent), ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
					SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
					TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
					Discovery:   discovery(req, agent, runtimeRoot, snap),
					OperationID: wizardMutationID(req.Action, agent, snap.Ledger.Generation),
					Operation:   wizardPackageOp(req.Action),
				}
				if others, err := mat.OtherLiveClients(id.InstallationID, string(agent)); err != nil {
					out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
					out.Outcome, out.Reason = "incomplete", "portable_inspect_failed"
					return out, err
				} else if len(others) > 0 && req.Action == ActionInstall && !liveNotifyClient(mat, id.InstallationID, string(agent)) {
					if err := mat.GuardSecondClient(ctx, materialize); err != nil {
						if portablesetup.IsUpdateRequired(err) {
							return updateRequired(req, agent, others, nil, nil, out, err)
						}
						out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
						out.Outcome, out.Reason = "incomplete", "portable_install_failed"
						return out, err
					}
				}
			}
		}
	}
	if !retainedMetadataUpdate(mat, id, req.Action) && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair) && len(notifyAgents) > 0 {
		failed, reason, err := bindNotifyPreviews(ctx, &req, req, snap, runtimeRoot, notifyAgents)
		if err != nil {
			if reason == "" {
				return mapPreviewFailure(ctx, req, failed, mat, id, err, out)
			}
			out.Outcome, out.Reason = "incomplete", reason
			return out, err
		}
	}
	if len(notifyAgents) > 0 && (req.Action == ActionInstall || req.Action == ActionUpdate || req.Action == ActionRepair) {
		_, existing, err := portable.InstalledGlobalConfig(snap.Ledger, id.InstallationID, req.ControlRoot)
		if err == nil && existing {
			err = portable.ValidateGlobalConfigParent(id.GlobalConfig)
		} else if err == nil {
			err = config.PrepareGlobalConfigParent(id.GlobalConfig)
			if err == nil {
				err = portable.ValidateGlobalConfigParent(id.GlobalConfig)
			}
		}
		if err != nil {
			out.Outcome, out.Reason = "incomplete", "global_config_parent_invalid"
			return out, err
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
	if retainedMetadataUpdate(mat, id, req.Action) {
		materialize := portablesetup.MaterializeRequest{
			Identity: id, Integration: notifyAgents[0], ExpectedGeneration: snap.Ledger.Generation,
			PackageRoot: req.PackageRoot, ClientConfigRoot: clientConfig(req, notifyAgents[0]), ClientExecutable: clientExecutable(req, notifyAgents[0]),
			OperationID: wizardMutationID(req.Action, notifyAgents[0], snap.Ledger.Generation),
		}
		if switched, err := mat.SwitchRetained(ctx, materialize); err != nil {
			return mapSwitchRetainedFailure(err, out)
		} else {
			out.NextActions = append(out.NextActions, installerNextActions(switched.NextActions)...)
		}
		state, loadErr := mat.Store.Load()
		if loadErr != nil {
			out.Outcome, out.Reason = "incomplete", loadErr.Error()
			return out, loadErr
		}
		recorded := ""
		for _, installation := range state.Installations {
			if installation.InstallationID == id.InstallationID {
				recorded = installation.Source.TreeDigest
				if len(installation.Clients) != 0 {
					out.Outcome, out.Reason = "incomplete", "retained_source_not_durable"
					return out, fmt.Errorf("retained switch materialized a client")
				}
				break
			}
		}
		if recorded == "" {
			out.Outcome, out.Reason = "incomplete", "retained_source_not_durable"
			return out, fmt.Errorf("retained source digest is missing after switch")
		}
		out.InstallationID = id.InstallationID
		out.Outcome, out.Reason = "completed", "retained_source_updated"
		reportProgress(req, "complete")
		return out, nil
	}
	reportProgress(req, "agent-notify")
	generation := snap.Ledger.Generation
	if canGroupNotify(mat, id, req, notifyAgents) {
		var reqs []portablesetup.MaterializeRequest
		for _, agent := range notifyAgents {
			reqs = append(reqs, portablesetup.MaterializeRequest{
				Identity: id, Integration: agent, ExpectedGeneration: generation,
				PackageRoot: clientPackageRoot(req, agent), ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
				SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
				TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
				Discovery:       discovery(req, agent, runtimeRoot, snap),
				OperationID:     wizardMutationID(req.Action, "group", snap.Ledger.Generation),
				KeepReservation: true,
				Operation:       wizardPackageOp(req.Action),
			})
		}
		got, err := mat.ApplyGroup(ctx, reqs)
		if err != nil {
			if portablesetup.IsUpdateRequired(err) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(notifyAgents[0]))
				return updateRequired(req, notifyAgents[0], others, liveUpdateAgents(ctx, req, mat, id, notifyAgents[0]), unboundSelectedAgents(req, mat, id, notifyAgents[0]), out, err)
			}
			if errors.Is(err, portablesetup.ErrSourceIdentityDrift) {
				out.Outcome, out.Reason = "incomplete", "source_identity_drift"
				return out, err
			}
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			return groupNotifyFailed(notifyAgents, req, out, err), err
		}
		if len(got) != len(notifyAgents) {
			out.Outcome, out.Reason = "incomplete", "portable_install_failed"
			return out, fmt.Errorf("group apply returned %d bindings", len(got))
		}
		generation, err = rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		for i, agent := range notifyAgents {
			out.Targets = append(out.Targets, completedNotifyTarget(mat, got[i].InstallationID, string(agent), got[i].BindingID))
			id.InstallationID = got[i].InstallationID
			_ = persistKnownReceipt(ctx, req, runtimeRoot, mat, id.InstallationID, string(agent))
			if err := recordLiveProfile(got[i].DataRoot, string(agent), clientConfig(req, agent)); err != nil {
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
			}
		}
		out.Outcome = "completed"
		reportProgress(req, "complete")
		return out, nil
	}
	for _, agent := range notifyAgents {
		materialize := portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			PackageRoot: clientPackageRoot(req, agent), ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
			SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
			TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
			Discovery:       discovery(req, agent, runtimeRoot, snap),
			OperationID:     wizardMutationID(req.Action, agent, generation),
			KeepReservation: true,
			Operation:       wizardPackageOp(req.Action),
		}
		var got portable.Binding
		var err error
		switch req.Action {
		case ActionUpdate:
			got, err = mat.Update(ctx, materialize)
		case ActionRepair:
			got, err = mat.Repair(ctx, materialize)
		default:
			got, err = mat.Install(ctx, materialize)
		}
		if err != nil {
			if req.Action == ActionRepair && (exactRepairRevision(err) || portablesetup.IsUpdateRequired(err)) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
				if len(others) > 0 {
					live := liveBindingTreeDigest(mat, id.InstallationID, string(agent))
					repairRetry := req
					repairRetry.Agents = []string{string(agent)}
					repairRetry.PackageRoot = ""
					updateRetry := req
					updateRetry.Action = ActionUpdate
					updateRetry.Agents = []string{string(agent)}
					out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "exact_revision_required", TreeDigest: live})
					out.NextActions = append(out.NextActions,
						NextAction{Kind: "repair", Agents: []string{string(agent)}, Reason: "exact_revision_required", Command: RetryCommand(repairRetry)},
						NextAction{Kind: "update", Agents: []string{string(agent)}, Reason: "exact_revision_required", Command: RetryCommand(updateRetry)},
					)
					continue
				}
			}
			if portablesetup.IsUpdateRequired(err) {
				others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
				return updateRequired(req, agent, others, liveUpdateAgents(ctx, req, mat, id, agent), unboundSelectedAgents(req, mat, id, agent), out, err)
			}
			if errors.Is(err, portablesetup.ErrSourceIdentityDrift) {
				out.Outcome, out.Reason = "incomplete", "source_identity_drift"
				return out, err
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
		out.Targets = append(out.Targets, completedNotifyTarget(mat, got.InstallationID, string(agent), got.BindingID))
		id.InstallationID = got.InstallationID
		_ = persistKnownReceipt(ctx, req, runtimeRoot, mat, id.InstallationID, string(agent))
		if err := recordLiveProfile(got.DataRoot, string(agent), clientConfig(req, agent)); err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
	}
	for _, target := range out.Targets {
		if target.Unit == "agent-notify" && target.Reason == "exact_revision_required" {
			out.Outcome, out.Reason = "incomplete", "exact_revision_required"
			reportProgress(req, "complete")
			return out, ErrRefused
		}
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
		req.Primary = id.Primary
		out.InstallationID = id.InstallationID
		// §7.5: Recover is an application step, not Prepare(remove).
		if err := recoverWizardJournals(ctx, mat, id, req, notifyAgents); err != nil {
			out.Outcome, out.Reason = "incomplete", "recovery_required"
			return out, err
		}
		if err := reserveClientBindings(&req, mat, notifyAgents); err != nil {
			if mapped, handled := mapAmbiguous(err, out); handled {
				return mapped, err
			}
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
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
			if err := guardLiveProfile(mat, id.InstallationID, string(agent), clientConfig(req, agent)); err != nil {
				if mapped, handled := mapAmbiguous(err, out); handled {
					return mapped, err
				}
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
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
	_ = persistExternalUninstalled(ctx, req, runtimeRoot)
	out.Generation = snap.Ledger.Generation
	if len(notifyAgents) > 0 {
		// §7.5: persist removal intent, then satisfy Codex attestation before
		// any sibling locator revoke or hooks effect.
		out, err = holdCodexUninstall(ctx, &req, mat, id, snap.Ledger.Generation, notifyAgents, runtimeRoot, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
	}
	removeHooks := func(out Result) (Result, error) {
		if len(hookAgents) == 0 {
			return out, nil
		}
		// Keep the hooks consumer (and its managed helper) alive until UAP has
		// finished removing the portable bindings. Hooks may be the final
		// runtime consumer and their removal can retire that helper.
		reportProgress(req, "hooks")
		current, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out, err = applyHooks(ctx, req, hookAgents, current, true, out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		generation, err := rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		return out, nil
	}
	if len(notifyAgents) == 0 {
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		out.Outcome = "completed"
		reportProgress(req, "complete")
		return out, nil
	}
	if id.InstallationID == "" {
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
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
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		out = markRetainedEmpty(mat, id.InstallationID, out)
		reportProgress(req, "complete")
		return out, nil
	}
	reportProgress(req, "agent-notify")
	generation := snap.Ledger.Generation
	if canGroupRemove(mat, id, req, notifyAgents) {
		dataRoots := make([]string, len(notifyAgents))
		var reqs []portablesetup.MaterializeRequest
		for i, agent := range notifyAgents {
			dataRoots[i] = liveDataRoot(mat, id.InstallationID, string(agent))
			reqs = append(reqs, portablesetup.MaterializeRequest{
				Identity: id, Integration: agent, ExpectedGeneration: generation,
				ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
				OperationID:         wizardMutationID(req.Action, "group", generation),
				ExternalUninstalled: req.ExternalUninstalled,
				KeepReservation:     true,
			})
		}
		got, err := mat.RemoveGroup(ctx, reqs)
		if err != nil {
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			if errors.Is(err, portablesetup.ErrExternalUninstall) {
				return externalUninstallRequired(req, portable.Codex, out), err
			}
			return groupRemoveFailed(notifyAgents, req, out, err), err
		}
		if len(got) != len(notifyAgents) {
			out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
			return out, fmt.Errorf("group remove returned %d results", len(got))
		}
		removed := 0
		for i, agent := range notifyAgents {
			if err := clearLiveProfile(dataRoots[i], string(agent)); err != nil {
				out.Outcome, out.Reason = "incomplete", err.Error()
				return out, err
			}
			if got[i].AlreadyAbsent {
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
				continue
			}
			removed++
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "completed"})
		}
		generation, err = rereadGeneration(req.ControlRoot)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
			return out, err
		}
		out.Generation = generation
		out, err = removeHooks(out)
		if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
			return out, err
		}
		if removed == 0 {
			if out.Reason == "" {
				out.Reason = "already_absent"
			}
			out.Outcome = "unchanged"
			out = markRetainedEmpty(mat, id.InstallationID, out)
			reportProgress(req, "complete")
			return out, nil
		}
		out.Outcome = "completed"
		out = markRetainedEmpty(mat, id.InstallationID, out)
		reportProgress(req, "complete")
		return out, nil
	}
	removed := 0
	expectedCodexSpec := managedCodexPluginSpec(mat, id.InstallationID)
	for _, agent := range notifyAgents {
		if agent == portable.Codex && !req.ExternalUninstalled {
			if attestCodexExternalUninstall(ctx, clientExecutable(req, agent), req.CodexHome, expectedCodexSpec) {
				req.ExternalUninstalled = true
				_ = persistExternalUninstalled(ctx, req, runtimeRoot)
			}
		}
		remove := portablesetup.MaterializeRequest{
			Identity: id, Integration: agent, ExpectedGeneration: generation,
			ClientConfigRoot: clientConfig(req, agent), ClientExecutable: clientExecutable(req, agent),
			// User uninstall does not restore a retired direct MCP.
			OperationID: wizardMutationID(ActionUninstall, agent, generation), ExternalUninstalled: req.ExternalUninstalled,
			HoldOnly:        agent == portable.Codex && !req.ExternalUninstalled,
			KeepReservation: true,
		}
		dataRoot := liveDataRoot(mat, id.InstallationID, string(agent))
		err := mat.Remove(ctx, remove)
		if err != nil {
			if conflict, handled := pendingIntentConflict(req, err, out); handled {
				return conflict, err
			}
			if errors.Is(err, portablesetup.ErrAlreadyAbsent) {
				if clearErr := clearLiveProfile(dataRoot, string(agent)); clearErr != nil {
					out.Outcome, out.Reason = "incomplete", clearErr.Error()
					return out, clearErr
				}
				out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "unchanged", Reason: "already_absent"})
				continue
			}
			if errors.Is(err, portablesetup.ErrExternalUninstall) {
				return externalUninstallRequired(req, agent, out), err
			}
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()})
			out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
			return out, err
		}
		if err := clearLiveProfile(dataRoot, string(agent)); err != nil {
			out.Outcome, out.Reason = "incomplete", err.Error()
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
	out, err = removeHooks(out)
	if err != nil || out.Outcome == "incomplete" || out.Outcome == "invalid" {
		return out, err
	}
	if removed == 0 {
		if out.Reason == "" {
			out.Reason = "already_absent"
		}
		out.Outcome = "unchanged"
		out = markRetainedEmpty(mat, id.InstallationID, out)
		reportProgress(req, "complete")
		return out, nil
	}
	out.Outcome = "completed"
	out = markRetainedEmpty(mat, id.InstallationID, out)
	reportProgress(req, "complete")
	return out, nil
}

func managedCodexPluginSpec(mat portablesetup.Materializer, installationID string) string {
	if installationID == "" {
		return ""
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID || strings.TrimSpace(installation.DeclaredName) == "" {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID != string(portable.Codex) || strings.TrimSpace(binding.PhysicalArtifact) == "" {
				continue
			}
			return installation.DeclaredName + "@" + shared.ManagedMarketplaceName(binding.PhysicalArtifact)
		}
	}
	return ""
}

func portableInstallFailed(agent portable.Integration, req Request, out Result, err error) Result {
	if errors.Is(err, uapinstaller.ErrNotInstalled) {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "not_installed"})
		out.Outcome, out.Reason = "incomplete", "not_installed"
		return out
	}
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		return siblingCompatibilityUnavailable(agent, req, out)
	}
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

func siblingCompatibilityUnavailable(agent portable.Integration, req Request, out Result) Result {
	out.Outcome, out.Reason = "incomplete", "sibling_compatibility_unavailable"
	if agent != "" {
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: out.Reason})
	}
	retry := req
	retry.Action = ActionUpdate
	retry.Yes = true
	retry.Agents = []string{"claude", "codex"}
	out.NextActions = []NextAction{{
		Kind: "update", Agents: retry.Agents, Reason: out.Reason, Command: RetryCommand(retry),
	}}
	return out
}

func groupNotifyFailed(agents []portable.Integration, req Request, out Result, err error) Result {
	var persisted portablesetup.ResultError
	if !errors.As(err, &persisted) || (len(persisted.Result.Targets) == 0 && persisted.Result.Client.ClientID == "") {
		if len(agents) == 0 {
			return portableInstallFailed("", req, out, err)
		}
		return portableInstallFailed(agents[0], req, out, err)
	}
	if errors.Is(err, uapinstaller.ErrNotInstalled) {
		for _, agent := range agents {
			out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "not_installed"})
		}
		out.Outcome, out.Reason = "incomplete", "not_installed"
		return out
	}
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		if len(agents) == 0 {
			return siblingCompatibilityUnavailable("", req, out)
		}
		return siblingCompatibilityUnavailable(agents[0], req, out)
	}
	committed := false
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, string(agent))
		item := groupClientResult(persisted.Result, string(agent))
		target := TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()}
		if item.Materialization != "" && item.Materialization != string(domain.MaterializationAbsent) {
			committed = true
			target.Reason = "activation_incomplete"
		}
		out.Targets = append(out.Targets, target)
	}
	out.Outcome, out.Reason = "incomplete", "portable_install_failed"
	if !committed {
		return out
	}
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
		Kind: "activate", Agents: names, Reason: persisted.Result.Reason,
		Command: RetryCommand(retry),
	})
	return out
}

func groupClientResult(result uapinstaller.Result, clientID string) uapinstaller.ClientResult {
	for _, item := range result.Targets {
		if item.ClientID == clientID {
			return item
		}
	}
	if result.Client.ClientID == clientID {
		return result.Client
	}
	return uapinstaller.ClientResult{}
}

func groupRemoveFailed(agents []portable.Integration, req Request, out Result, err error) Result {
	out.Outcome, out.Reason = "incomplete", "portable_remove_failed"
	names := make([]string, 0, len(agents))
	var persisted portablesetup.ResultError
	hasPersist := errors.As(err, &persisted)
	for _, agent := range agents {
		names = append(names, string(agent))
		target := TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: err.Error()}
		if hasPersist {
			if item := groupClientResult(persisted.Result, string(agent)); item.ClientID != "" && item.Materialization == string(domain.MaterializationAbsent) {
				target.Outcome, target.Reason = "unchanged", "already_absent"
			}
		}
		out.Targets = append(out.Targets, target)
	}
	retry := req
	retry.Action = ActionUninstall
	retry.Yes = true
	if explicitAbs(req.ControlRoot) {
		if intent, readErr := portablesetup.ReadIntent(req.ControlRoot); readErr == nil {
			retry = retryRequestFromIntent(retry, intent)
		}
	}
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "uninstall", Agents: names, Reason: out.Reason,
		Command: RetryCommand(retry),
	})
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
	if intent.TreeDigest != "" {
		retry.TreeDigest = intent.TreeDigest
	}
	if intent.HelperDigest != "" {
		retry.HelperDigest = intent.HelperDigest
	}
	if intent.HelperVersion != "" {
		retry.HelperVersion = intent.HelperVersion
	}
	if intent.SourceRevision != "" {
		retry.ReleaseVersion = intent.SourceRevision
	}
	if intent.ExternalUninstalled {
		retry.ExternalUninstalled = true
	}
	if want, specified := intentClientUnits(intent); specified {
		retry = applyIntentUnits(retry, want)
	}
	for _, target := range intent.Targets {
		if target.InstallationID != "" {
			retry.InstallationID = target.InstallationID
		}
		if target.BindingID != "" {
			if retry.BindingIDs == nil {
				retry.BindingIDs = map[string]string{}
			}
			retry.BindingIDs[target.Client] = target.BindingID
		}
		if target.DataReceiptID != "" {
			if retry.DataReceiptIDs == nil {
				retry.DataReceiptIDs = map[string]string{}
			}
			retry.DataReceiptIDs[target.Client] = target.DataReceiptID
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

func liveUpdateAgents(ctx context.Context, req Request, mat portablesetup.Materializer, id portablesetup.Identity, fallback portable.Integration) []string {
	if !liveNotifyClient(mat, id.InstallationID, string(fallback)) {
		return nil
	}
	root := clientPackageRoot(req, fallback)
	if root == "" {
		root = req.PackageRoot
	}
	desired, err := retainedUpdateTreeDigest(ctx, mat, root)
	if err != nil || desired == "" {
		return []string{string(fallback)}
	}
	selected := req.Agents
	if len(selected) == 0 {
		selected = []string{string(fallback)}
	}
	var agents []string
	for _, name := range selected {
		if !liveNotifyClient(mat, id.InstallationID, name) {
			continue
		}
		got := liveBindingTreeDigest(mat, id.InstallationID, name)
		if got != "" && got != desired {
			agents = append(agents, name)
		}
	}
	if len(agents) == 0 {
		return []string{string(fallback)}
	}
	return agents
}

func unboundSelectedAgents(req Request, mat portablesetup.Materializer, id portablesetup.Identity, fallback portable.Integration) []string {
	selected := req.Agents
	if len(selected) == 0 {
		selected = []string{string(fallback)}
	}
	var unbound []string
	for _, name := range selected {
		if !liveNotifyClient(mat, id.InstallationID, name) {
			unbound = append(unbound, name)
		}
	}
	return unbound
}

func updateRequired(req Request, adding portable.Integration, others []string, liveUpdate, unbound []string, out Result, err error) (Result, error) {
	out.Targets = append(out.Targets, TargetResult{Client: string(adding), Unit: "agent-notify", Outcome: "incomplete", Reason: "update_required"})
	out.Outcome, out.Reason = "incomplete", "update_required"
	updateReq := req
	updateReq.Action = ActionUpdate
	if len(liveUpdate) > 0 {
		updateReq.Agents = append([]string(nil), liveUpdate...)
		reason := "update_existing"
		if len(unbound) > 0 {
			reason = "update_existing_before_add"
		}
		out.NextActions = []NextAction{
			{Kind: "update", Agents: updateReq.Agents, Command: RetryCommand(updateReq), Reason: reason},
		}
		if len(unbound) > 0 {
			addReq := req
			addReq.Agents = append([]string(nil), unbound...)
			out.NextActions = append(out.NextActions, NextAction{
				Kind: "install", Agents: addReq.Agents, Command: RetryCommand(addReq), Reason: "add_after_update",
			})
		}
		return out, err
	}
	updateAgents := others
	if len(updateAgents) == 0 {
		updateAgents = []string{string(adding)}
	}
	updateReq.Agents = updateAgents
	addReq := req
	addReq.Agents = []string{string(adding)}
	out.NextActions = []NextAction{
		{Kind: "update", Agents: updateAgents, Command: RetryCommand(updateReq), Reason: "update_existing_before_add"},
		{Kind: "install", Agents: []string{string(adding)}, Command: RetryCommand(addReq), Reason: "add_after_update"},
	}
	return out, err
}

func annotateRetainedMetadataUpdate(text string, agents []portable.Integration) string {
	text = strings.Replace(text, "required=restart,request-permission,test-notification permission-dialog=explicit delivery=not_verified", "required=none permission-dialog=skipped delivery=not_verified", 1)
	text += " data_retained=true metadata-only data-compatibility-warning"
	var names []string
	for _, agent := range agents {
		names = append(names, string(agent))
	}
	if len(names) > 0 {
		text += " phases=1-update:" + strings.Join(names, ",")
	}
	return text
}

func retainedUpdateTreeDigest(ctx context.Context, mat portablesetup.Materializer, packageRoot string) (string, error) {
	eng, err := installerEngine(mat)
	if err != nil {
		return "", err
	}
	return eng.LocalPackageTreeDigest(ctx, packageRoot)
}

func retainedPackageIdentityConflict(controlRoot, installationID, packageRoot string) bool {
	desired := packageDeclaredName(packageRoot)
	if desired == "" || installationID == "" {
		return false
	}
	state, err := loadUAPState(controlRoot)
	if err != nil {
		return false
	}
	for _, installation := range state.Installations {
		if installation.InstallationID == installationID && installation.DeclaredName != "" && installation.DeclaredName != desired {
			return true
		}
	}
	return false
}

func installerNextActions(actions []uapinstaller.NextAction) []NextAction {
	out := make([]NextAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, NextAction{Kind: action.Kind, Reason: action.Reason})
	}
	return out
}

func mapSwitchRetainedFailure(err error, out Result) (Result, error) {
	var persisted portablesetup.ResultError
	if errors.As(err, &persisted) && persisted.Result.Reason != "" {
		out.Outcome, out.Reason = string(persisted.Result.Outcome), persisted.Result.Reason
		if out.Outcome == "" {
			out.Outcome = "incomplete"
		}
		return out, err
	}
	if errors.Is(err, uapinstaller.ErrAssessmentRejected) {
		out.Outcome, out.Reason = "incomplete", "assessment_rejected"
		return out, err
	}
	out.Outcome, out.Reason = "incomplete", "portable_preflight_failed"
	return out, err
}

func annotateRequiredUpdate(text string, out Result) string {
	var updates, adds []string
	for _, next := range out.NextActions {
		switch next.Kind {
		case "update":
			updates = append(updates, next.Agents...)
		case "install":
			adds = append(adds, next.Agents...)
		}
	}
	if len(updates) == 0 {
		return text
	}
	text += " required-update=" + strings.Join(updates, ",")
	text += " phases=1-update:" + strings.Join(updates, ",")
	if len(adds) > 0 {
		text += ";2-add:" + strings.Join(adds, ",")
	}
	return text
}

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
	return portablesetup.NewMaterializer(portablesetup.UAPRoots{
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
		case string(portable.Codex), string(portable.Claude):
			clientID = req.Agents[0]
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
	if id != "" {
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
	if global == "" {
		selected, err := config.Resolve(config.SnapshotEnv())
		if err != nil {
			return portablesetup.Identity{}, err
		}
		global = selected.Path
	}
	primary, err := primaryName(req, snap.Ledger, id)
	if err != nil {
		return portablesetup.Identity{}, err
	}
	return portablesetup.Identity{
		InstallationID: id, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner,
		ScopeRoot: scope, ControlRoot: req.ControlRoot, GlobalConfig: global,
		RuntimeRoot: runtimeRoot, Primary: primary,
	}, nil
}

func installerEngine(mat portablesetup.Materializer) (*uapinstaller.Engine, error) {
	registry, err := portablesetup.NewRegistry()
	if err != nil {
		return nil, err
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

func copyBindingIDs(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(src))
	for client, id := range src {
		out[client] = id
	}
	return out
}

func copyPackageRoots(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for client, root := range src {
		out[client] = root
	}
	return out
}

func clientPackageRoot(req Request, agent portable.Integration) string {
	if req.PackageRoots != nil {
		if root := req.PackageRoots[string(agent)]; root != "" {
			return root
		}
	}
	return req.PackageRoot
}

func bindRepairPackages(ctx context.Context, req *Request, snap installruntime.InstalledSnapshot, runtimeRoot string, agents []portable.Integration) {
	if req == nil || req.Action != ActionRepair || len(agents) == 0 {
		return
	}
	recorded, _ := desiredPackage(*req)
	seen := map[string]bool{}
	var candidates []string
	for _, root := range []string{req.PackageRoot, recorded} {
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		candidates = append(candidates, root)
	}
	if len(candidates) == 0 {
		return
	}
	if req.PackageRoots == nil {
		req.PackageRoots = map[string]string{}
	}
	for _, agent := range agents {
		if req.PackageRoots[string(agent)] != "" {
			continue
		}
		for _, root := range candidates {
			try := *req
			try.PackageRoot = root
			try.PackageRoots = nil
			if _, err := previewNotifyPlan(ctx, try, snap, runtimeRoot, agent); err == nil {
				req.PackageRoots[string(agent)] = root
				break
			}
		}
	}
}

func reserveClientBindings(req *Request, mat portablesetup.Materializer, agents []portable.Integration) error {
	if req == nil || req.InstallationID == "" || len(agents) == 0 {
		return nil
	}
	eng, err := installerEngine(mat)
	if err != nil {
		return err
	}
	bindings := copyBindingIDs(req.BindingIDs)
	for _, agent := range agents {
		client := string(agent)
		if bindings[client] != "" {
			continue
		}
		reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{
			ClientID:         client,
			InstallationID:   req.InstallationID,
			DeclaredName:     packageDeclaredName(req.PackageRoot),
			ClientConfigRoot: clientConfig(*req, agent),
		})
		if err != nil {
			if errors.Is(err, uapinstaller.ErrAmbiguousInstallations) {
				return fmt.Errorf("%w: %w", ErrAmbiguousInstallation, err)
			}
			return err
		}
		if reserved.BindingID != "" {
			bindings[client] = reserved.BindingID
		}
	}
	req.BindingIDs = bindings
	return nil
}

func bindNotifyPreviews(ctx context.Context, req *Request, previewReq Request, snap installruntime.InstalledSnapshot, runtimeRoot string, notifyAgents []portable.Integration) (portable.Integration, string, error) {
	if req == nil {
		return "", "", nil
	}
	for _, agent := range notifyAgents {
		try := previewReq
		try.PackageRoots = req.PackageRoots
		if root := clientPackageRoot(*req, agent); root != "" {
			try.PackageRoot = root
		}
		preview, err := previewNotifyPlan(ctx, try, snap, runtimeRoot, agent)
		if err != nil {
			if req.Action == ActionRepair && len(notifyAgents) > 1 && (exactRepairRevision(err) || portablesetup.IsUpdateRequired(err)) {
				continue
			}
			return agent, "", err
		}
		if reserved := req.BindingIDs[string(agent)]; reserved != "" && preview.BindingID != "" && preview.BindingID != reserved {
			return agent, "binding_id_drift", ErrRefused
		}
		if err := bindSourceIdentity(req, preview); err != nil {
			return agent, "source_identity_drift", err
		}
		previewReq.TreeDigest = req.TreeDigest
		previewReq.HelperDigest = req.HelperDigest
		previewReq.HelperVersion = req.HelperVersion
	}
	return "", "", nil
}

func bindSourceIdentity(req *Request, preview uapinstaller.Plan) error {
	if req == nil {
		return nil
	}
	if req.Action != ActionRepair {
		if err := bindIdentityField(&req.TreeDigest, preview.TreeDigest); err != nil {
			return err
		}
	}
	if err := bindIdentityField(&req.HelperDigest, preview.HelperDigest); err != nil {
		return err
	}
	return bindIdentityField(&req.HelperVersion, preview.HelperVersion)
}

func bindIdentityField(dst *string, src string) error {
	if dst == nil || src == "" {
		return nil
	}
	if *dst == "" {
		*dst = src
		return nil
	}
	if *dst != src {
		return fmt.Errorf("%w: source_identity_drift", ErrRefused)
	}
	return nil
}

func mapPreviewFailure(ctx context.Context, req Request, agent portable.Integration, mat portablesetup.Materializer, id portablesetup.Identity, err error, out Result) (Result, error) {
	if mapped, handled := mapAmbiguous(err, out); handled {
		return mapped, err
	}
	if errors.Is(err, uapinstaller.ErrNotInstalled) {
		out.Outcome, out.Reason = "incomplete", "not_installed"
		return out, err
	}
	if errors.Is(err, uapinstaller.ErrCompatibilityUnavailable) || errors.Is(err, uapinstaller.ErrTargetFactsUnavailable) {
		return siblingCompatibilityUnavailable(agent, req, out), err
	}
	if portablesetup.IsUpdateRequired(err) {
		others, _ := mat.OtherLiveClients(id.InstallationID, string(agent))
		return updateRequired(req, agent, others, liveUpdateAgents(ctx, req, mat, id, agent), unboundSelectedAgents(req, mat, id, agent), out, err)
	}
	out.Outcome, out.Reason = "incomplete", "portable_preflight_failed"
	return out, err
}

func canGroupNotify(mat portablesetup.Materializer, id portablesetup.Identity, req Request, agents []portable.Integration) bool {
	if len(agents) != 2 {
		return false
	}
	first := liveNotifyClient(mat, id.InstallationID, string(agents[0]))
	second := liveNotifyClient(mat, id.InstallationID, string(agents[1]))
	switch req.Action {
	case ActionInstall:
		if first != second {
			return false
		}
		if !first {
			return true
		}
		return sameLiveRepairRevision(mat, id.InstallationID, agents)
	case ActionUpdate:
		return first && second
	case ActionRepair:
		if !first || !second {
			return false
		}
		if !liveManagedTargetsPresent(mat, id.InstallationID, agents) {
			return false
		}
		if sameLiveRepairRevision(mat, id.InstallationID, agents) {
			return true
		}
		if len(req.PackageRoots) != 2 {
			return false
		}
		for _, agent := range agents {
			if req.PackageRoots[string(agent)] == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func canGroupRemove(mat portablesetup.Materializer, id portablesetup.Identity, req Request, agents []portable.Integration) bool {
	if req.Action != ActionUninstall || len(agents) != 2 {
		return false
	}
	for _, agent := range agents {
		if agent == portable.Codex && !req.ExternalUninstalled && liveNotifyClient(mat, id.InstallationID, string(agent)) {
			return false
		}
	}
	return true
}

func retainedMetadataUpdate(mat portablesetup.Materializer, id portablesetup.Identity, action Action) bool {
	if action != ActionUpdate || id.InstallationID == "" {
		return false
	}
	empty, err := mat.RetainedEmpty(id.InstallationID)
	return err == nil && empty
}

func requireLiveNotifyBindings(req Request, mat portablesetup.Materializer, id portablesetup.Identity, agents []portable.Integration, out Result) (Result, error) {
	if retained, err := mat.RetainedEmpty(id.InstallationID); err == nil && retained && req.Action == ActionUpdate {
		return out, nil
	}
	var missing, live []string
	for _, agent := range agents {
		if liveNotifyClient(mat, id.InstallationID, string(agent)) {
			live = append(live, string(agent))
			continue
		}
		missing = append(missing, string(agent))
		out.Targets = append(out.Targets, TargetResult{Client: string(agent), Unit: "agent-notify", Outcome: "incomplete", Reason: "not_installed"})
	}
	if len(missing) == 0 {
		return out, nil
	}
	out.Outcome, out.Reason = "incomplete", "not_installed"
	if len(live) > 0 {
		installReq := req
		installReq.Action = ActionInstall
		installReq.Agents = append([]string(nil), missing...)
		subset := req
		subset.Agents = append([]string(nil), live...)
		out.NextActions = []NextAction{
			{Kind: "install", Agents: missing, Command: RetryCommand(installReq), Reason: "install_missing"},
			{Kind: string(req.Action), Agents: live, Command: RetryCommand(subset), Reason: "continue_installed_only"},
		}
	}
	return out, uapinstaller.ErrNotInstalled
}

func mapAmbiguous(err error, out Result) (Result, bool) {
	reason := ""
	switch {
	case errors.Is(err, ErrAmbiguousInstallation):
		reason = "ambiguous_installation"
	case errors.Is(err, ErrAmbiguousBinding):
		reason = "ambiguous_binding"
	case errors.Is(err, ErrLiveProfileConflict):
		reason = "live_profile_conflict"
	default:
		return out, false
	}
	out.Outcome, out.Reason = "conflict", reason
	out.NextActions = append(out.NextActions, NextAction{
		Kind: "inspect", Reason: err.Error(),
		Command: []string{"setup-notifications", "wizard", "--action", "inspect", "--json"},
	})
	return out, true
}

func liveClientBindings(mat portablesetup.Materializer, installationID, clientID string) ([]domain.ClientBinding, error) {
	if installationID == "" || clientID == "" {
		return nil, nil
	}
	state, err := mat.Store.Load()
	if err != nil {
		return nil, err
	}
	var out []domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID {
				out = append(out, binding)
			}
		}
	}
	return out, nil
}

func guardLiveProfile(mat portablesetup.Materializer, installationID, clientID, profile string) error {
	bindings, err := liveClientBindings(mat, installationID, clientID)
	if err != nil {
		return err
	}
	if len(bindings) > 1 {
		return fmt.Errorf("%w: client %s has %d bindings", ErrAmbiguousBinding, clientID, len(bindings))
	}
	if len(bindings) == 0 || profile == "" {
		return nil
	}
	live := bindings[0]
	if profileOwnsLive(profile, live) {
		return nil
	}
	if clientConfigAnchored(live) {
		return fmt.Errorf("%w: live target %s is not under %s", ErrLiveProfileConflict, live.TargetLocator, profile)
	}
	// Codex TargetLocator lives under managed/clients. The live CodexHome is
	// recorded in PLUGIN_DATA, not locator JSON, so Registration() bytes stay
	// stable.
	dataRoot, err := bindingDataRoot(mat, installationID, live)
	if err != nil {
		return err
	}
	recorded, err := recordedLiveProfile(dataRoot, clientID)
	if err != nil {
		return err
	}
	if recorded == "" || sameLiveProfile(profile, recorded) {
		return nil
	}
	return fmt.Errorf("%w: live profile %s is not %s", ErrLiveProfileConflict, recorded, profile)
}

func profileOwnsLive(profile string, binding domain.ClientBinding) bool {
	if profileMatchesLive(profile, binding.TargetLocator) {
		return true
	}
	for _, obj := range binding.NativeObjects {
		if profileMatchesLive(profile, obj.Path) {
			return true
		}
	}
	return false
}

func clientConfigAnchored(binding domain.ClientBinding) bool {
	if binding.TargetLocator != "" && !managedClientPath(binding.TargetLocator) {
		return true
	}
	for _, obj := range binding.NativeObjects {
		if obj.Path != "" && !managedClientPath(obj.Path) {
			return true
		}
	}
	return false
}

func managedClientPath(p string) bool {
	return strings.Contains(filepath.ToSlash(p), "/managed/clients/")
}

func profileMatchesLive(profile, target string) bool {
	if profile == "" || target == "" {
		return false
	}
	root := filepath.Clean(profile)
	path := filepath.Clean(target)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
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
				BindingID:      req.BindingIDs[id],
				DataReceiptID:  req.DataReceiptIDs[id],
				Profile:        clientConfig(req, agent),
			}
			byClient[id] = target
			order = append(order, id)
		}
		target.Units = append(target.Units, unit)
		if unit == "agent-notify" {
			target.MCPConfig = discoveryConfigPath(req, agent)
		}
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

func attachKnownReceipts(req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, targets []portablesetup.IntentTarget) []portablesetup.IntentTarget {
	if len(targets) == 0 || req.InstallationID == "" {
		return targets
	}
	mat, err := materializer(req, snap, runtimeRoot)
	if err != nil {
		return targets
	}
	for i, target := range targets {
		if target.DataReceiptID != "" {
			continue
		}
		id := target.InstallationID
		if id == "" {
			id = req.InstallationID
		}
		if receipt := knownReceiptID(mat, id, target.Client); receipt != "" {
			targets[i].DataReceiptID = receipt
		}
	}
	return targets
}

func persistExternalUninstalled(ctx context.Context, req Request, runtimeRoot string) error {
	if !req.ExternalUninstalled {
		return nil
	}
	return (portablesetup.Service{}).PatchIntentExternalUninstalled(ctx, req.ControlRoot, runtimeRoot, "")
}

func persistKnownReceipt(ctx context.Context, req Request, runtimeRoot string, mat portablesetup.Materializer, installationID, client string) error {
	receipt := knownReceiptID(mat, installationID, client)
	if receipt == "" {
		return nil
	}
	return (portablesetup.Service{}).PatchIntentReceipt(ctx, req.ControlRoot, runtimeRoot, "", client, receipt)
}

func knownReceiptID(mat portablesetup.Materializer, installationID, clientID string) string {
	if installationID == "" || clientID == "" {
		return ""
	}
	state, err := mat.Store.Load()
	if err != nil {
		return ""
	}
	for _, installation := range state.Installations {
		if installation.InstallationID != installationID {
			continue
		}
		for _, binding := range installation.Clients {
			if binding.ClientID == clientID && binding.DataReceiptID != "" {
				return binding.DataReceiptID
			}
		}
	}
	return ""
}

func publishWizardIntent(ctx context.Context, req Request, snap installruntime.InstalledSnapshot, runtimeRoot string, hookAgents, notifyAgents []portable.Integration, portablePresent bool) (installruntime.InstalledSnapshot, error) {
	if snap.Ledger.PendingMutation != nil {
		return snap, nil
	}
	if !wizardWillMutate(hookAgents, notifyAgents, portablePresent, req.Action == ActionInstall) {
		return snap, nil
	}
	targets := wizardIntentTargets(req, hookAgents, notifyAgents)
	targets = attachKnownReceipts(req, snap, runtimeRoot, targets)
	if len(targets) == 0 {
		return snap, nil
	}
	if _, _, err := (portablesetup.Service{}).PublishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{
		ControlRoot: req.ControlRoot, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner,
		ExpectedGeneration: snap.Ledger.Generation, Action: string(req.Action), Stage: "confirmed",
		SourceRevision: req.ReleaseVersion, SourceDigest: req.PackageSHA256,
		TreeDigest: req.TreeDigest, HelperDigest: req.HelperDigest, HelperVersion: req.HelperVersion,
		Primary:             req.Primary,
		ExternalUninstalled: req.ExternalUninstalled,
		Targets:             targets,
	}); err != nil {
		return snap, err
	}
	return installruntime.ReadInstalledSnapshot(req.ControlRoot)
}

func shouldFinishWizardIntent(out Result) bool {
	switch out.Outcome {
	case "completed", "unchanged":
		return true
	case "incomplete":
		// §7.4.1: UAP binding, locator, and runtime consumer are consistent.
		// Manual/failed activation is a next action, not an unfinished handoff.
		// Mixed-revision Repair advertises a different package/agent retry
		// after the matching sibling succeeded, so leftover intent would
		// conflict with that next action.
		return out.Reason == "activation_incomplete" || out.Reason == "exact_revision_required"
	default:
		return false
	}
}

func finishWizardIntent(ctx context.Context, req Request, runtimeRoot string, out Result, err error) (Result, error) {
	if !shouldFinishWizardIntent(out) {
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

// repairRequestsMissingHooks reports a Codex hooks unit that repair would add.
// Claude hooks are never managed by this wizard. A missing notify binding is
// not_installed from requireLiveNotifyBindings, not this check.
func repairRequestsMissingHooks(req Request, hookAgents []portable.Integration) (portable.Integration, bool) {
	if req.Action != ActionRepair {
		return "", false
	}
	for _, agent := range hookAgents {
		if agent == portable.Claude {
			continue
		}
		if !hooksManaged(req, agent) {
			return agent, true
		}
	}
	return "", false
}

// preserveLiveUnits keeps omitted update/repair unit flags on the currently
// managed units. Omission must not add a unit the user left off.
func preserveLiveUnits(ctx context.Context, req Request, agents []portable.Integration) Request {
	if !unitFlagsOmitted(req) {
		return req
	}
	notifyLive := map[string]bool{}
	if view, err := inspectUAPState(ctx, req); err == nil {
		for _, installation := range view.Installations {
			for _, binding := range installation.Bindings {
				if binding.ClientID != "" {
					notifyLive[binding.ClientID] = true
				}
			}
		}
	}
	for _, agent := range agents {
		hooks, notify := boolPtr(hooksManaged(req, agent)), boolPtr(notifyLive[string(agent)])
		switch agent {
		case portable.Claude:
			req.ClaudeHooks, req.ClaudeAgentNotify = hooks, notify
		case portable.Codex:
			req.CodexHooks, req.CodexAgentNotify = hooks, notify
		}
	}
	return req
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
			Hooks:      "not_checked",
			MCP:        "not_checked",
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
				fact.Hooks = targetReadiness(req.Action, target)
			case "agent-notify":
				fact.MCP = targetReadiness(req.Action, target)
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

func targetReadiness(action Action, target TargetResult) string {
	if action == ActionUninstall && (target.Outcome == "completed" || target.Outcome == "unchanged" && target.Reason == "already_absent") {
		return "absent"
	}
	if target.Outcome == "completed" {
		return "installed"
	}
	return target.Outcome
}

func offerPostSetupActions(req Request, agents []portable.Integration, out Result, restartPending bool) Result {
	if req.Action == ActionUninstall {
		return out
	}
	notifyReady := false
	for _, target := range out.Targets {
		if target.Unit == "agent-notify" && (target.Outcome == "completed" || target.Outcome == "installed") {
			notifyReady = true
			break
		}
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
	if has("recover") || has("external-uninstall") || has("resume") || has("activate") {
		return out
	}
	if restartPending && !has("restart-client") {
		out.NextActions = append(out.NextActions, NextAction{
			Kind: "restart-client", Agents: names, Reason: "pending_client_restart",
		})
	}
	if !notifyReady {
		return out
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

func applyHostSnapshots(req *Request) {
	if req == nil {
		return
	}
	if req.CodexHome == "" {
		req.CodexHome = req.EnvCodexHome
	}
	if req.ClaudeConfig == "" {
		req.ClaudeConfig = req.EnvClaudeConfig
	}
	if req.ReleaseVersion == "" {
		req.ReleaseVersion = req.DefaultReleaseVersion
	}
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}
