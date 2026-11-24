// Package setupwizard owns installation planning, execution and recovery.
// Run and InstallOrUpdate do not prompt. FillInteractive fills the same Request
// through a terminal adapter before the caller confirms application effects.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func Run(ctx context.Context, req Request) (Result, error) {
	req.LocalConfig = copyLocalConfig(req.LocalConfig)
	out, err := run(ctx, &req)
	if errors.Is(err, uapinstaller.ErrMigrationRequired) {
		out.Outcome, out.Reason = "incomplete", "state_migration_required"
	}
	// Hooks can return the kernel conflict directly; portable boundaries wrap
	// it as ErrConcurrentChange. Keep completed targets, but require new consent.
	if errors.Is(err, installruntime.ErrPolicyConflict) && !errors.Is(err, portablesetup.ErrConcurrentChange) {
		err = fmt.Errorf("%w: %w", portablesetup.ErrConcurrentChange, err)
	}
	if errors.Is(err, portablesetup.ErrConcurrentChange) || out.Reason == "concurrent_change" {
		out.Outcome, out.Reason = "conflict", "concurrent_change"
		out.Command, out.NextActions = nil, nil
		return out, err
	}
	if strings.HasPrefix(out.Reason, "state_migration_") {
		out.Command, out.NextActions = nil, nil
		return out, err
	}
	return attachWizardCommand(req, out), err
}

func run(ctx context.Context, req *Request) (Result, error) {
	// Evaluation fills omitted unit flags and may restore pending-intent maps.
	// A recovered kernel journal changes the live set, so keep the caller's
	// request intact for the second evaluation.
	original := *req
	original.Agents = append([]string(nil), req.Agents...)
	original.PackageRoots = maps.Clone(req.PackageRoots)
	original.ClientExecutables = maps.Clone(req.ClientExecutables)
	original.BindingIDs = maps.Clone(req.BindingIDs)
	original.MigrationBindings = maps.Clone(req.MigrationBindings)
	original.RemovalBindings = maps.Clone(req.RemovalBindings)
	original.DataReceiptIDs = maps.Clone(req.DataReceiptIDs)
	original.MCPConfig = maps.Clone(req.MCPConfig)
	ev := evaluate(ctx, req, true)
	if ev.stop {
		return ev.out, ev.err
	}
	if req.Action == ActionInspect {
		got, err := inspect(ctx, *req, ev.agents, ev.snap, ev.runtimeRoot, ev.out)
		return attachReadiness(*req, ev.agents, got, false), err
	}
	if len(ev.notifyAgents) > 0 {
		migrated, migrationErr := applyRequestedStateMigration(ctx, req)
		if migrationErr != nil {
			out := StateMigrationFailure(*req, migrationErr)
			out.StateMigration = migrated
			return out, migrationErr
		}
		if migrated != nil {
			// Registry migration invalidates all pre-migration UAP plans. Rebuild
			// them before publishing an intent or touching any client registration.
			plan, planErr := Plan(ctx, *req)
			if plan.Request.StateMigration != nil && plan.Request.StateMigration.Required {
				out := StateMigrationFailure(*req, uapinstaller.ErrPlanChanged)
				out.StateMigration = migrated
				return out, uapinstaller.ErrPlanChanged
			}
			if planErr != nil || !plan.Ready {
				plan.Result.StateMigration = migrated
				return plan.Result, planErr
			}
			*req = plan.Request
			out, runErr := run(ctx, req)
			out.StateMigration = migrated
			return out, runErr
		}
	}
	if ev.snap.Recovery {
		// A journal replay advances the ledger generation. Recover before any
		// target mutation, then evaluate the recovered state again so intent
		// publication fences the generation that actually owns those targets.
		// Automatic bootstrap selected clients from the old state and must
		// reselect them in a new invocation instead of adopting the replay.
		if req.BootstrapExpectedGeneration != nil {
			ev.out.Outcome, ev.out.Reason = "conflict", "concurrent_change"
			return ev.out, ErrRefused
		}
		if err := verifyWizardKernelRecovery(ctx, *req, ev.snap, ev.runtimeRoot, ev.notifyAgents); err != nil {
			ev.out.Outcome, ev.out.Reason = "incomplete", "recovery_required"
			return ev.out, err
		}
		release, err := installruntime.AcquireCoordinatorLease(ctx, req.ControlRoot)
		if err != nil {
			ev.out.Outcome, ev.out.Reason = "incomplete", "recovery_required"
			return ev.out, err
		}
		current, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
		if err == nil && (current.Ledger.Generation != ev.snap.Ledger.Generation || !current.Recovery) {
			err = portablesetup.ErrConcurrentChange
		}
		var recovered installruntime.Ledger
		if err == nil {
			recovered, err = installruntime.Recover(ctx, req.ControlRoot)
		}
		release()
		if errors.Is(err, portablesetup.ErrConcurrentChange) {
			ev.out.Outcome, ev.out.Reason = "conflict", "concurrent_change"
			return ev.out, err
		}
		if err != nil {
			ev.out.Outcome, ev.out.Reason = "incomplete", "recovery_required"
			return ev.out, err
		}
		*req = original
		ev = evaluate(ctx, req, true)
		if ev.stop {
			return ev.out, ev.err
		}
		if ev.snap.Recovery || ev.snap.Ledger.Generation != recovered.Generation {
			ev.out.Outcome, ev.out.Reason = "conflict", "concurrent_change"
			return ev.out, portablesetup.ErrConcurrentChange
		}
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
	// Both pending-intent restoration and discovery fill omitted MCP paths.
	// Plan and Run own those additions, including when evaluation later refuses.
	req.MCPConfig = maps.Clone(req.MCPConfig)
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
	// Use the same OS-owned root aliases as committed portable bindings and
	// UAP managed paths before resolving either ledger. Arbitrary user symlinks
	// are intentionally not followed by PhysicalPath.
	for _, path := range []*string{&req.ControlRoot, &req.RuntimeRoot, &req.ScopeRoot} {
		if *path != "" && explicitAbs(*path) {
			physical, pathErr := installruntime.PhysicalPath(*path)
			if pathErr != nil {
				out.Outcome, out.Reason = "invalid", "root_path_invalid"
				return evaluated{out: out, err: pathErr, stop: true}
			}
			*path = physical
		}
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
	if req.Action != ActionInspect && req.BootstrapExpectedGeneration != nil &&
		(!haveSnap || snap.Ledger.Generation != *req.BootstrapExpectedGeneration) {
		out.Outcome, out.Reason = "conflict", "concurrent_change"
		return evaluated{out: out, err: ErrRefused, stop: true}
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
	if req.Action != ActionInspect && req.BootstrapMCP != nil {
		observation := *req
		observation.Agents = nil
		for _, id := range []string{"claude", "codex", "cursor", "copilot-vscode", "pi"} {
			if _, ok := req.BootstrapMCP.Projection.Profiles[id]; ok {
				observation.Agents = append(observation.Agents, id)
			}
		}
		current, generation, policy, observeErr := ObserveBootstrapMCPWithPolicy(ctx, observation)
		if observeErr == nil && generation != snap.Ledger.Generation {
			observeErr = ErrRefused
		}
		if observeErr == nil {
			observeErr = CheckBootstrapMCP(*req.BootstrapMCP, current)
		}
		if observeErr != nil {
			out.Outcome, out.Reason = "conflict", "concurrent_change"
			return evaluated{out: out, err: observeErr, stop: true}
		}
		if req.BootstrapExpectedPolicy != nil && *req.BootstrapExpectedPolicy != policy {
			out.Outcome, out.Reason = "conflict", "concurrent_change"
			return evaluated{out: out, err: ErrRefused, stop: true}
		}
		if req.BootstrapExpectedPolicy == nil {
			req.BootstrapExpectedPolicy = &policy
		}
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
		preserved, err := preserveLiveUnits(ctx, *req, agents)
		if err != nil {
			out.Outcome, out.Reason = "incomplete", "existing_units_unavailable"
			return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, err: err, stop: true}
		}
		*req = preserved
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
			Kind: "install", Agents: []string{string(agent)}, Command: localRetryCommand(installReq), Reason: "repair_does_not_add_units",
		}}
		return evaluated{agents: agents, snap: snap, runtimeRoot: runtimeRoot, out: out, err: ErrRefused, stop: true}
	}
	return evaluated{
		agents: agents, snap: snap, runtimeRoot: runtimeRoot,
		hookAgents: hookAgents, notifyAgents: notifyAgents, out: out,
	}
}

func applyHostSnapshots(req *Request) {
	if req == nil {
		return
	}
	if req.PiAgentDir == "" {
		for _, agent := range req.Agents {
			if agent == "pi" {
				req.PiAgentDir = req.EnvPiAgentDir
				break
			}
		}
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
