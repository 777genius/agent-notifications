package setupwizard

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
)

// InstallOrUpdate applies the confirmed selection through bounded update/add
// phases. It preserves opt-outs and the original policy fences across every
// phase; the caller owns argument parsing, confirmation and presentation.
func InstallOrUpdate(ctx context.Context, req Request, preserveExistingUnits bool) (result Result, err error) {
	var migration *StateMigrationOutcome
	defer func() {
		if migration != nil {
			result.StateMigration = migration
		}
		if result.Reason == "concurrent_change" {
			// The original auto selection is stale. A direct mutation retry
			// would turn an opt-out into an implicit Add.
			result.Command = nil
			result.NextActions = nil
		}
	}()
	// Auto mode inspects before it chooses Update/Add phases. Validate the UAP
	// registry first so inspection cannot leak parser errors or recommend native
	// recovery for an old schema that needs its previous installer.
	if req.AgentNotify == nil || *req.AgentNotify {
		if req.StateMigration != nil {
			if err := CheckStateMigration(ctx, req.ControlRoot, *req.StateMigration); err != nil {
				return StateMigrationFailure(req, err), err
			}
		} else {
			prepared, err := PrepareStateMigration(ctx, req.ControlRoot)
			if err != nil {
				return StateMigrationFailure(req, err), err
			}
			req.StateMigration = &prepared
		}
	}
	inspect := req
	inspect.Action = ActionInspect
	before, inspectErr := Run(ctx, inspect)
	if inspectErr != nil {
		before.Action = string(req.Action)
		return before, inspectErr
	}
	for _, next := range before.NextActions {
		if next.Kind == "recover" || next.Kind == "resume" {
			return Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required", NextActions: before.NextActions}, ErrRefused
		}
	}
	var preserved []string
	if req.BootstrapMCP != nil {
		current, generation, policy, e := ObserveBootstrapMCPWithPolicy(ctx, req)
		if e == nil && (req.BootstrapExpectedGeneration != nil && generation != *req.BootstrapExpectedGeneration || req.BootstrapExpectedPolicy != nil && policy != *req.BootstrapExpectedPolicy) {
			e = portablesetup.ErrConcurrentChange
		}
		if e == nil {
			e = CheckBootstrapMCP(*req.BootstrapMCP, current)
		}
		if e != nil {
			return Result{Action: string(req.Action), Outcome: "conflict", Reason: "concurrent_change"}, e
		}
		// Observe/check the full frozen projection above, including clients whose
		// primary install failed. Only the ensuing effects may be narrowed.
		req.Agents = readyBootstrapAgents(req.BootstrapMCP.Selected, req.BootstrapReadyClients)
		preserved = append([]string(nil), req.BootstrapMCP.Skipped...)
		if len(req.Agents) == 0 {
			return Result{Action: string(req.Action), Outcome: "unchanged", Reason: "existing_opt_out", Targets: PreservedUnitTargets(preserved)}, nil
		}
		if req.BootstrapExpectedGeneration == nil {
			req.BootstrapExpectedGeneration = &generation
		}
		if req.BootstrapExpectedPolicy == nil {
			req.BootstrapExpectedPolicy = &policy
		}
	} else if preserveExistingUnits {
		selected, skipped, err := BootstrapAutoTargets(ctx, req, before)
		if err != nil {
			return Result{Action: string(req.Action), Outcome: "incomplete", Reason: "existing_units_unavailable"}, err
		}
		req.Agents, preserved = selected, skipped
		if len(selected) == 0 {
			return Result{Action: string(req.Action), Outcome: "unchanged", Reason: "existing_opt_out", Targets: PreservedUnitTargets(preserved)}, nil
		}
		// Selection is a policy decision about currently live bindings. The
		// package fetch can take time, so every later Plan and Run must still
		// see this ledger generation (or one produced by our own phase).
		expected := before.Generation
		req.BootstrapExpectedGeneration = &expected
	}
	if req.PackageRoot == "" {
		pinned, err := PinCurrentReleasePackage(ctx, req)
		if err != nil {
			return Result{Action: string(req.Action), Outcome: "incomplete", Reason: "package_acquisition_failed"}, err
		}
		req = pinned
	}
	// A group update can expose another behind client only after the first
	// binding moves. Replan the original selection until every selected client
	// is ready; a completed phase alone is not proof that the group is current.
	for attempt := 0; attempt <= len(req.Agents); attempt++ {
		plan, err := Plan(ctx, req)
		if plan.Ready && (plan.Request.Action != req.Action || strings.Join(plan.Request.Agents, ",") != strings.Join(req.Agents, ",")) {
			return Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required"}, ErrRefused
		}
		if plan.Ready {
			result, runErr := Run(ctx, plan.Request)
			if migration == nil {
				migration = result.StateMigration
			}
			if runErr != nil || result.ExitCode() != 0 {
				return result, runErr
			}
			verified, verifyErr := verifyInstallOrUpdate(ctx, req, result)
			verified.Targets = append(verified.Targets, PreservedUnitTargets(preserved)...)
			return verified, verifyErr
		}
		if plan.Result.Reason != "update_required" || !validInstallOrUpdateActions(req.Agents, plan.Result.NextActions) {
			return plan.Result, err
		}
		for _, next := range plan.Result.NextActions {
			phase := req
			phase.Action = Action(next.Kind)
			phase.Agents = append([]string(nil), next.Agents...)
			phasePlan, planErr := Plan(ctx, phase)
			if !phasePlan.Ready {
				return phasePlan.Result, planErr
			}
			result, runErr := Run(ctx, phasePlan.Request)
			if migration == nil {
				migration = result.StateMigration
			}
			if runErr != nil || result.ExitCode() != 0 {
				return result, runErr
			}
			if req.BootstrapExpectedGeneration != nil {
				*req.BootstrapExpectedGeneration = result.Generation
			}
			// Our completed phase consumed its reviewed registry snapshot. Bind
			// the next phase to the resulting current schema; never approve a
			// second old-schema migration that appeared during this invocation.
			current, prepareErr := PrepareStateMigration(ctx, req.ControlRoot)
			if prepareErr != nil {
				return StateMigrationFailure(req, prepareErr), prepareErr
			}
			if current.Required {
				return StateMigrationFailure(req, uapinstaller.ErrPlanChanged), uapinstaller.ErrPlanChanged
			}
			req.StateMigration = &current
		}
	}
	return Result{Action: string(req.Action), Outcome: "incomplete", Reason: "update_required_after_retries"}, ErrRefused
}

// PreservedUnitTargets reports existing per-client opt-outs without implying
// that an absent unit was selected for installation.
func PreservedUnitTargets(clients []string) []TargetResult {
	var targets []TargetResult
	for _, client := range clients {
		targets = append(targets, TargetResult{Client: client, Unit: "agent-notify", Outcome: "absent", Reason: "preserved_existing_opt_out"})
	}
	return targets
}

func validInstallOrUpdateActions(selected []string, next []NextAction) bool {
	if len(next) < 1 || len(next) > 2 || next[0].Kind != "update" ||
		(len(next) == 2 && next[1].Kind != "install") {
		return false
	}
	allowed := map[string]bool{}
	for _, agent := range selected {
		allowed[agent] = true
	}
	seen := map[string]bool{}
	for i, action := range next {
		if len(action.Agents) == 0 {
			return false
		}
		phaseSeen := map[string]bool{}
		for _, agent := range action.Agents {
			if !allowed[agent] || phaseSeen[agent] {
				return false
			}
			phaseSeen[agent] = true
			// A retained-empty installation needs a metadata-only update,
			// then an install of the same selected client to restore delivery.
			if seen[agent] && (i != 1 || next[0].Reason != "update_existing_before_add" || action.Reason != "add_after_update") {
				return false
			}
			seen[agent] = true
		}
	}
	return true
}

func verifyInstallOrUpdate(ctx context.Context, req Request, result Result) (Result, error) {
	inspect := req
	inspect.Action = ActionInspect
	view, err := Run(ctx, inspect)
	// Inspect can report recovery-required state without an error while still
	// listing installed clients. Neither that state nor an inspection error
	// proves a complete setup; keep the mutation action's failure exit semantics.
	if err != nil || view.Outcome != "completed" {
		view.Action, view.Outcome = string(req.Action), "incomplete"
		if view.Reason == "" {
			view.Reason = "post_install_incomplete"
		}
		return view, errors.Join(ErrRefused, err)
	}
	installed := map[string]bool{}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			installed[target.Client] = true
		}
	}
	for _, agent := range req.Agents {
		if !installed[agent] {
			return Result{Action: string(req.Action), Outcome: "incomplete", Reason: "post_install_incomplete", Targets: view.Targets}, ErrRefused
		}
	}
	return result, nil
}

func readyBootstrapAgents(selected, ready []string) []string {
	var result []string
	for _, id := range selected {
		if len(ready) == 0 || slices.Contains(ready, id) {
			result = append(result, id)
		}
	}
	return result
}
