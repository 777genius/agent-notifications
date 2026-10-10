package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/cli/installerui"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/clientdetect"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/cursor"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

const setupWizardHelp = `Usage: claude-notifications setup-notifications wizard [OPTIONS]
Master for hooks plus portable MCP/skill.
TTY stdin prompts for agents, an existing-install action, and units when those flags are omitted, then shows a preflight plan and asks for confirmation.
When Claude and Codex already have different units, the TTY shows both and does not collapse omission to one bool.
--json never prompts. Progress phases go to stderr. No TTY and no --agents/--yes is invalid for mutation, not a hang. Omitted --agents with --yes is still invalid unless a matching pending intent restores them.
Inspect exit 0 means the report was read; readiness stays in result fields.
Without --action, a new machine defaults to install; an existing portable
installation is offered inspect, add/reinstall, uninstall, update, or repair.
  --action install|uninstall|inspect|update|repair
  --install-or-update       Bootstrap-only: install selected clients or update owned bindings first
  --preserve-existing-units Bootstrap auto mode: leave absent MCP units off when another binding already exists
  --agents claude,codex|cursor|copilot-vscode   Cursor requires explicit --scope-root and --client-executable; select separately
  Omit on inspect to report both clients
  --hooks true|false          Omit on install of new targets to include hooks; omit on update/repair to keep live units; omit on uninstall to select all units
  --agent-notify true|false   Omit on install of new targets to include portable MCP+skill; omit on update/repair to keep live units
  --claude-hooks true|false   Per-client override; mixed Claude/Codex opt-outs are not collapsed
  --codex-hooks true|false
  --claude-agent-notify true|false
  --codex-agent-notify true|false
  --yes                       Required for mutation without a TTY unless a matching pending intent exists
  --external-uninstalled      Codex native plugin already removed or never activated; --yes does not set this
  --json
  --package PATH              Local standard package root or same-release zip
  --plugin-root PATH          Existing plugin bundle for Codex hooks-only setup
  --control-root PATH         Existing managed control directory
  --runtime-root PATH         Existing managed runtime (default: ledger runtime)
  --global-config PATH
  --codex-home PATH           Explicit Codex UAP client config root; else CODEX_HOME once
  --claude-config PATH        Explicit Claude UAP client config root; else CLAUDE_CONFIG_DIR once
  --client-executable PATH    Selected client executable; never launched from PATH
  --claude-executable PATH    Claude executable when installing both clients
  --codex-executable PATH     Codex executable when installing both clients
  --helper PATH               Managed stdio helper (default: runtime primary)
  --scope-root PATH
  --local-settings PATH       Explicit Local settings.json in the selected profile
  --local-native-stop true|false  Independent Local Stop selection
  --local-mcp true|false       Independent Local MCP selection
  --local-skills true|false    Independent Local skill selection
  --installation-id ID
  --mcp-config PATH           Owned Codex MCP config to hand off; omit to use an existing config.toml in the selected Codex profile
  --claude-mcp-config PATH    Owned Claude MCP config to hand off; omit to use an existing .claude.json in the selected Claude profile
Cursor native desktop/webhook consent requires confirmed setup-products channel choices; --yes and MCP selection do not grant it.
Cursor --agent-notify false and uninstall revoke the recorded native pair before physical cleanup.
Update and repair require an existing owned binding; missing files remain repairable. Two Claude+Codex notify installs when both are unbound, and update/repair of two live same-revision bindings, use one group apply. Install onto mixed live revisions requires Update of the behind sibling, not group Add. Install of both when one client is already live on an older revision is Update of that client then Add of the missing one. Mixed Codex uninstall still needs --external-uninstalled before Claude is removed with it.
`

func executeSetupWizard(ctx context.Context, args []string, out io.Writer) int {
	return executeSetupWizardWith(ctx, args, out, os.Stderr, os.Stdin, stdinIsCharDevice())
}

func executeSetupWizardWith(ctx context.Context, args []string, out, errOut io.Writer, in io.Reader, tty bool) int {
	if len(args) == 1 && args[0] == "--help" {
		_, err := io.WriteString(out, setupWizardHelp)
		if err != nil {
			return 1
		}
		return 0
	}
	args, uiMode, uiErr := stripWizardUIMode(args)
	args, bootstrapIntentFile, intentFlagErr := stripBootstrapIntentFile(args)
	if intentFlagErr == nil {
		intentFlagErr = uiErr
	}
	args, installOrUpdate, preserveExistingUnits, flagErr := stripInstallOrUpdate(args)
	if flagErr == nil {
		flagErr = intentFlagErr
	}
	req, jsonOut, err := parseSetupWizard(args)
	if flagErr != nil && err == nil {
		err = flagErr
	}
	if err != nil {
		if jsonOut {
			_ = json.NewEncoder(out).Encode(setupwizard.Result{Outcome: "invalid", Reason: "invalid_arguments"})
		} else {
			_, _ = fmt.Fprintln(out, "invalid_arguments")
		}
		return 2
	}
	if req.ControlRoot == "" {
		root, e := installruntime.ControlRoot()
		if e != nil {
			return 1
		}
		req.ControlRoot = root
	}
	if req.Action == "" && (!tty || jsonOut) {
		if jsonOut {
			_ = json.NewEncoder(out).Encode(setupwizard.Result{Outcome: "invalid", Reason: "invalid_arguments"})
		} else {
			_, _ = fmt.Fprintln(out, "invalid_arguments")
		}
		return 2
	}
	if req.ReleaseVersion == "" {
		req.DefaultReleaseVersion = config.ConsumerVersion
	}
	if req.ReleaseDownloadRoot == "" {
		req.ReleaseDownloadRoot = portableasset.DefaultReleaseDownloadRoot
	}
	if (preserveExistingUnits && !installOrUpdate) || (installOrUpdate && !validInstallOrUpdateRequest(req)) {
		return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, nil)
	}
	var confirmed *confirmedBootstrapIntent
	if bootstrapIntentFile != "" {
		if !installOrUpdate || !req.Yes || jsonOut && needsWizardInteraction(req) {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, nil)
		}
		req, confirmed, err = admitBootstrapWizardIntent(bootstrapIntentFile, req)
		if err != nil {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "conflict", Reason: "concurrent_change"}, err)
		}
	}
	if errOut != nil {
		req.Progress = func(phase string) {
			if jsonOut {
				_, _ = fmt.Fprintln(errOut, "phase", phase)
			} else if phase != "complete" {
				_, _ = fmt.Fprintln(errOut, "Setting up notifications: "+strings.ReplaceAll(phase, "-", " ")+"...")
			}
		}
	}
	needsPrompt := tty && !jsonOut && (req.Action == "" || (req.Action != setupwizard.ActionInspect && (len(req.Agents) == 0 || !req.Yes)))
	var prompt setupwizard.Prompter
	if needsPrompt {
		if input, ok := in.(*os.File); ok {
			visible, _ := out.(*os.File)
			prompt, err = setupwizard.NewTerminalPublicPrompt(input, visible, installerui.TerminalMode(uiMode), os.Getenv("NO_COLOR") != "")
			if errors.Is(err, setupwizard.ErrPromptUnavailable) {
				visible, _ = errOut.(*os.File)
				prompt, err = setupwizard.NewTerminalPublicPrompt(input, visible, installerui.TerminalMode(uiMode), os.Getenv("NO_COLOR") != "")
			}
		} else {
			prompt, err = setupwizard.NewPublicPrompt(in, out)
		}
		if err != nil {
			return writeSetupWizardPromptError(out, jsonOut, req, err)
		}
	}
	if needsPrompt {
		req.DiscoverAgents = func() []setupwizard.AgentCapability {
			return setupwizard.DiscoverAgents(req)
		}
		filled, e := setupwizard.FillInteractive(ctx, req, prompt, func(agents []string) []string {
			return setupwizard.LiveSetupClients(req, agents)
		})
		if e != nil {
			return writeSetupWizardPromptError(out, jsonOut, req, e)
		}
		req = filled
	}
	// Pending original-token recovery remains owned by composition. No early
	// revocation or affirmative consent can enter that reservation.
	ownership, recovering, ownershipErr := installruntime.ReadOwnership(req.ControlRoot)
	pendingCursor := ownershipErr != nil || recovering || ownership.PendingMutation != nil
	if containsProduct(req.Agents, "cursor") && pendingCursor && (confirmed != nil || req.AgentNotify != nil && !*req.AgentNotify) {
		return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required"}, setupwizard.ErrRefused)
	}
	if containsProduct(req.Agents, "cursor") && pendingCursor && req.Action == setupwizard.ActionUninstall {
		// A matching removal retry can continue after its earlier revocation;
		// it cannot perform another false-only commit inside the reservation.
		s, e := installruntime.ReadPolicySnapshot(ctx, req.ControlRoot)
		var route struct {
			Cursor struct{ Desktop, Webhook *bool } `json:"cursorNotifications"`
		}
		if e != nil || json.Unmarshal(s.Fields["route"], &route) != nil || route.Cursor.Desktop == nil || route.Cursor.Webhook == nil || *route.Cursor.Desktop || *route.Cursor.Webhook {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required"}, setupwizard.ErrRefused)
		}
		generation, policy := s.Installation.Ledger.Generation, s.Preimage
		req.BootstrapExpectedGeneration, req.BootstrapExpectedPolicy = &generation, &policy
	}
	if containsProduct(req.Agents, "cursor") && !pendingCursor && req.Action != setupwizard.ActionInspect {
		if confirmed != nil {
			if err = fenceCursorIntent(ctx, req, *confirmed); err != nil {
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "conflict", Reason: "concurrent_change"}, err)
			}
			if !containsProduct(confirmed.MCP.Selected, "cursor") {
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "unchanged", Reason: "existing_opt_out", Targets: preservedUnitTargets(confirmed.MCP.Skipped)}, nil)
			}
		}
		recorded, found, e := recordedCursorRequest(req)
		if e != nil {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "conflict", Reason: "cursor_identity_unavailable"}, e)
		}
		disable := req.AgentNotify != nil && !*req.AgentNotify || req.CursorAgentNotify != nil && !*req.CursorAgentNotify
		revoke := req.Action == setupwizard.ActionUninstall || disable
		if found && !revoke {
			// A superseded or unverified registration cannot lend consent to repair.
			snapshot, e := installruntime.ReadInstalledSnapshot(req.ControlRoot)
			if e == nil {
				cfg, fixed, inputErr := cursorInstalledInputs(ctx, recorded, snapshot, filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state"))
				if inputErr == nil {
					gate, gateErr := copilotvscodeinstall.NewCursorGate(recorded, cfg, fixed)
					if gateErr == nil {
						_, gateErr = gate.ConsumerBinding(ctx)
					}
					inputErr = gateErr
				}
				e = inputErr
			}
			revoke = e != nil || req.Helper != "" && req.Helper != filepath.Join(recorded.RuntimeRoot, filepath.FromSlash(recorded.Primary)) || req.GlobalConfig != "" && req.GlobalConfig != recorded.GlobalConfig || req.RuntimeRoot != "" && req.RuntimeRoot != recorded.RuntimeRoot
		}
		if revoke && found {
			if !req.Yes && tty && !jsonOut {
				ok, e := prompt.Confirm(ctx, "Cursor action="+string(req.Action)+" installation="+recorded.InstallationID+" binding="+recorded.BindingID+" profile="+recorded.ScopeRoot+" global-config="+recorded.GlobalConfig+" helper="+filepath.Join(recorded.RuntimeRoot, filepath.FromSlash(recorded.Primary))+": revoke desktop and webhook, then attempt the selected owned lifecycle action; cleanup may fail while revocation remains. Apply this plan?")
				if e != nil {
					return writeSetupWizardPromptError(out, jsonOut, req, e)
				}
				if !ok {
					return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "cancelled", Reason: "prompt_canceled"}, nil)
				}
				req.Yes = true
			}
			if !req.Yes {
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, setupwizard.ErrRefused)
			}
			ledger, after, e := revokeRecordedCursor(ctx, recorded, req.BootstrapExpectedGeneration, req.BootstrapExpectedPolicy)
			if e != nil {
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "cursor_revocation_failed"}, e)
			}
			if disable {
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "completed", InstallationID: recorded.InstallationID, Generation: ledger.Generation}, nil)
			}
			generation := ledger.Generation
			req.BootstrapExpectedGeneration, req.BootstrapExpectedPolicy = &generation, &after
		}
		if disable && !found {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "cursor_identity_unavailable"}, setupwizard.ErrRefused)
		}
	}
	if containsProduct(req.Agents, "copilot-vscode") {
		var early *setupwizard.Result
		req, early, err = prepareLocalWizard(ctx, req, confirmed)
		if early != nil {
			return writeSetupWizardResult(out, jsonOut, *early, err)
		}
		if err != nil {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "local_composition_required"}, err)
		}
	}
	req, err = composeCursorWizard(ctx, req)
	if err != nil {
		return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "cursor_composition_required"}, err)
	}
	if confirmed != nil && (containsProduct(confirmed.Request.Products, "cursor") || containsProduct(confirmed.Request.Products, "copilot-vscode")) {
		// The command already fenced and consumed the one immutable selection.
		// Legacy BootstrapMCP evaluation captures a new policy observation; retain
		// our original CAS instead of allowing that path to adopt an opt-out.
		req.BootstrapMCP = nil
	}
	if req.Action != setupwizard.ActionInspect && !req.Yes && tty && !jsonOut {
		plan, e := setupwizard.Plan(ctx, req)
		req = plan.Request
		if e != nil || !plan.Ready {
			if plan.Text != "" {
				_, _ = fmt.Fprintln(out, plan.Text)
			}
			return writeSetupWizardResult(out, jsonOut, plan.Result, e)
		}
		var ok bool
		if confirmer, hasPlan := prompt.(interface {
			ConfirmPlan(context.Context, setupwizard.SetupPlan) (bool, error)
		}); hasPlan {
			ok, e = confirmer.ConfirmPlan(ctx, plan)
		} else {
			ok, e = prompt.Confirm(ctx, plan.Text)
		}
		if e != nil {
			return writeSetupWizardPromptError(out, jsonOut, req, e)
		}
		if !ok {
			result := setupwizard.Result{Action: string(req.Action), Outcome: "cancelled", Reason: "prompt_canceled"}
			return writeSetupWizardResult(out, jsonOut, result, nil)
		}
		req.Yes = true
	}
	var result setupwizard.Result
	if installOrUpdate {
		result, err = runInstallOrUpdate(ctx, req, preserveExistingUnits)
	} else {
		result, err = setupwizard.Run(ctx, req)
	}
	if err == nil && result.ExitCode() == 0 && confirmed != nil && (containsProduct(confirmed.MCP.Selected, "cursor") || containsProduct(confirmed.MCP.Selected, "copilot-vscode")) {
		if containsProduct(req.Agents, "copilot-vscode") {
			result, err = commitConfirmedLocalConsent(ctx, req, result, *confirmed)
		} else {
			result, err = commitConfirmedCursorConsent(ctx, req, result, *confirmed)
		}
	}
	return writeSetupWizardResult(out, jsonOut, result, err)
}

// The bootstrap flag is intentionally outside the public wizard Request. It
// changes orchestration, not the meaning of any explicit wizard action.
func stripInstallOrUpdate(args []string) ([]string, bool, bool, error) {
	filtered := make([]string, 0, len(args))
	found, preserve := false, false
	for _, arg := range args {
		if arg != "--install-or-update" && arg != "--preserve-existing-units" {
			filtered = append(filtered, arg)
			continue
		}
		if arg == "--preserve-existing-units" {
			if preserve {
				return filtered, false, false, errors.New("invalid_arguments")
			}
			preserve = true
			continue
		}
		if found {
			return filtered, false, false, errors.New("invalid_arguments")
		}
		found = true
	}
	return filtered, found, preserve, nil
}

func validInstallOrUpdateRequest(req setupwizard.Request) bool {
	if req.Action != setupwizard.ActionInstall || !req.Yes || req.Hooks == nil || *req.Hooks ||
		req.AgentNotify == nil || !*req.AgentNotify || req.ExternalUninstalled ||
		req.ClaudeHooks != nil || req.CodexHooks != nil ||
		req.ClaudeAgentNotify != nil || req.CodexAgentNotify != nil ||
		len(req.Agents) == 0 || len(req.Agents) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, agent := range req.Agents {
		if (agent != "claude" && agent != "codex" && ((agent != "cursor" && agent != "copilot-vscode") || len(req.Agents) != 1)) || seen[agent] {
			return false
		}
		seen[agent] = true
	}
	return true
}

func runInstallOrUpdate(ctx context.Context, req setupwizard.Request, preserveExistingUnits bool) (result setupwizard.Result, err error) {
	defer func() {
		if result.Reason == "concurrent_change" {
			// The original auto selection is stale. A direct mutation retry
			// would turn an opt-out into an implicit Add.
			result.Command = nil
			result.NextActions = nil
		}
	}()
	inspect := req
	inspect.Action = setupwizard.ActionInspect
	before, inspectErr := setupwizard.Run(ctx, inspect)
	if inspectErr != nil {
		return before, inspectErr
	}
	for _, next := range before.NextActions {
		if next.Kind == "recover" || next.Kind == "resume" {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required", NextActions: before.NextActions}, setupwizard.ErrRefused
		}
	}
	var preserved []string
	if req.BootstrapMCP != nil {
		current, generation, policy, e := setupwizard.ObserveBootstrapMCPWithPolicy(ctx, req)
		if e == nil && (req.BootstrapExpectedGeneration != nil && generation != *req.BootstrapExpectedGeneration || req.BootstrapExpectedPolicy != nil && policy != *req.BootstrapExpectedPolicy) {
			e = portablesetup.ErrConcurrentChange
		}
		if e == nil {
			e = setupwizard.CheckBootstrapMCP(*req.BootstrapMCP, current)
		}
		if e != nil {
			return setupwizard.Result{Action: string(req.Action), Outcome: "conflict", Reason: "concurrent_change"}, e
		}
		req.Agents = append([]string(nil), req.BootstrapMCP.Selected...)
		preserved = append([]string(nil), req.BootstrapMCP.Skipped...)
		if len(req.Agents) == 0 {
			return setupwizard.Result{Action: string(req.Action), Outcome: "unchanged", Reason: "existing_opt_out", Targets: preservedUnitTargets(preserved)}, nil
		}
		req.BootstrapExpectedGeneration = &generation
		req.BootstrapExpectedPolicy = &policy
	} else if preserveExistingUnits {
		selected, skipped, err := setupwizard.BootstrapAutoTargets(ctx, req, before)
		if err != nil {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "existing_units_unavailable"}, err
		}
		req.Agents, preserved = selected, skipped
		if len(selected) == 0 {
			return setupwizard.Result{Action: string(req.Action), Outcome: "unchanged", Reason: "existing_opt_out", Targets: preservedUnitTargets(preserved)}, nil
		}
		// Selection is a policy decision about currently live bindings. The
		// package fetch can take time, so every later Plan and Run must still
		// see this ledger generation (or one produced by our own phase).
		expected := before.Generation
		req.BootstrapExpectedGeneration = &expected
	}
	if req.PackageRoot == "" {
		pinned, err := setupwizard.PinCurrentReleasePackage(ctx, req)
		if err != nil {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "package_acquisition_failed"}, err
		}
		req = pinned
	}
	// A group update can expose another behind client only after the first
	// binding moves. Replan the original selection until every selected client
	// is ready; a completed phase alone is not proof that the group is current.
	for attempt := 0; attempt <= len(req.Agents); attempt++ {
		plan, err := setupwizard.Plan(ctx, req)
		if plan.Ready && (plan.Request.Action != req.Action || strings.Join(plan.Request.Agents, ",") != strings.Join(req.Agents, ",")) {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "pending_setup_required"}, setupwizard.ErrRefused
		}
		if plan.Ready {
			result, runErr := setupwizard.Run(ctx, plan.Request)
			if runErr != nil || result.ExitCode() != 0 {
				return result, runErr
			}
			verified, verifyErr := verifyInstallOrUpdate(ctx, req, result)
			verified.Targets = append(verified.Targets, preservedUnitTargets(preserved)...)
			return verified, verifyErr
		}
		if plan.Result.Reason != "update_required" || !validInstallOrUpdateActions(req.Agents, plan.Result.NextActions) {
			return plan.Result, err
		}
		for _, next := range plan.Result.NextActions {
			phase := req
			phase.Action = setupwizard.Action(next.Kind)
			phase.Agents = append([]string(nil), next.Agents...)
			phasePlan, planErr := setupwizard.Plan(ctx, phase)
			if !phasePlan.Ready {
				return phasePlan.Result, planErr
			}
			result, runErr := setupwizard.Run(ctx, phasePlan.Request)
			if runErr != nil || result.ExitCode() != 0 {
				return result, runErr
			}
			if req.BootstrapExpectedGeneration != nil {
				*req.BootstrapExpectedGeneration = result.Generation
			}
		}
	}
	return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "update_required_after_retries"}, setupwizard.ErrRefused
}

func preservedUnitTargets(clients []string) []setupwizard.TargetResult {
	var targets []setupwizard.TargetResult
	for _, client := range clients {
		targets = append(targets, setupwizard.TargetResult{Client: client, Unit: "agent-notify", Outcome: "absent", Reason: "preserved_existing_opt_out"})
	}
	return targets
}

func validInstallOrUpdateActions(selected []string, next []setupwizard.NextAction) bool {
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

func verifyInstallOrUpdate(ctx context.Context, req setupwizard.Request, result setupwizard.Result) (setupwizard.Result, error) {
	inspect := req
	inspect.Action = setupwizard.ActionInspect
	view, err := setupwizard.Run(ctx, inspect)
	if err != nil {
		return view, err
	}
	installed := map[string]bool{}
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			installed[target.Client] = true
		}
	}
	for _, agent := range req.Agents {
		if !installed[agent] {
			return setupwizard.Result{Action: string(req.Action), Outcome: "incomplete", Reason: "post_install_incomplete", Targets: view.Targets}, setupwizard.ErrRefused
		}
	}
	return result, nil
}

func writeSetupWizardPromptError(out io.Writer, jsonOut bool, req setupwizard.Request, e error) int {
	reason, outcome := "prompt_canceled", "cancelled"
	if errors.Is(e, setupwizard.ErrPromptInputClosed) {
		reason = "prompt_closed"
	} else if errors.Is(e, setupwizard.ErrPromptUnavailable) {
		reason, outcome = "prompt_unavailable", "invalid"
	} else if !errors.Is(e, setupwizard.ErrPromptCanceled) {
		reason, outcome = "prompt_failed", "invalid"
	} else if strings.Contains(e.Error(), "invalid_choice") {
		reason, outcome = "invalid_choice", "invalid"
	}
	result := setupwizard.Result{Action: string(req.Action), Outcome: outcome, Reason: reason}
	return writeSetupWizardResult(out, jsonOut, result, e)
}

func writeSetupWizardResult(out io.Writer, jsonOut bool, result setupwizard.Result, err error) int {
	if jsonOut {
		if e := json.NewEncoder(out).Encode(result); e != nil {
			return 1
		}
	} else {
		writeSetupWizardSummary(out, result)
	}

	if result.ExitCode() != 0 {
		return result.ExitCode()
	}
	if result.Action == string(setupwizard.ActionInspect) {
		return 0
	}
	if err != nil && result.Outcome != "completed" && result.Outcome != "unchanged" && result.Outcome != "cancelled" && result.Outcome != "ready" {
		return 1
	}
	return 0
}

func wizardPrintableCommand(command []string) []string {
	if len(command) == 0 || command[0] != "setup-notifications" {
		return append([]string(nil), command...)
	}
	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		executable = "claude-notifications"
	}
	out := make([]string, 0, len(command)+1)
	out = append(out, executable)
	return append(out, command...)
}

func parseSetupWizard(args []string) (setupwizard.Request, bool, error) {
	var req setupwizard.Request
	if len(args) > 64 {
		return req, false, errors.New("invalid_arguments")
	}
	total := 0
	for _, arg := range args {
		total += len(arg)
		if len(arg) > 4096 || !utf8.ValidString(arg) || strings.IndexFunc(arg, unicode.IsControl) >= 0 {
			return req, false, errors.New("invalid_arguments")
		}
	}
	if total > 16384 {
		return req, false, errors.New("invalid_arguments")
	}
	values := map[string]string{}
	jsonOut := false
	allowed := map[string]bool{
		"action": true, "agents": true, "hooks": true, "agent-notify": true,
		"local-settings": true, "local-native-stop": true, "local-mcp": true, "local-skills": true,
		"claude-hooks": true, "codex-hooks": true, "claude-agent-notify": true, "codex-agent-notify": true,
		"package": true, "plugin-root": true, "control-root": true, "runtime-root": true, "global-config": true,
		"codex-home": true, "claude-config": true, "client-executable": true, "helper": true,
		"scope-root": true, "installation-id": true, "mcp-config": true, "claude-mcp-config": true,
		"claude-executable": true, "codex-executable": true,
	}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--yes" {
			if req.Yes {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			req.Yes = true
			continue
		}
		if token == "--external-uninstalled" {
			if req.ExternalUninstalled {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			req.ExternalUninstalled = true
			continue
		}
		if token == "--json" {
			if jsonOut {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			jsonOut = true
			continue
		}
		if !strings.HasPrefix(token, "--") {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		key, value, inline := strings.Cut(token[2:], "=")
		if !allowed[key] {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		if _, ok := values[key]; ok {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		if !inline {
			i++
			if i >= len(args) {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			value = args[i]
		}
		if value == "" || strings.HasPrefix(value, "--") {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		values[key] = value
	}
	for _, key := range []string{"package", "plugin-root", "control-root", "runtime-root", "global-config", "codex-home", "claude-config", "client-executable", "helper", "scope-root", "mcp-config", "claude-mcp-config", "claude-executable", "codex-executable", "local-settings"} {
		if values[key] == "" {
			continue
		}
		normalized, err := normalizeSetupPhysicalPath(values[key])
		if err != nil {
			return req, jsonOut, err
		}
		values[key] = normalized
	}
	switch values["action"] {
	case "install":
		req.Action = setupwizard.ActionInstall
	case "uninstall":
		req.Action = setupwizard.ActionUninstall
	case "inspect":
		req.Action = setupwizard.ActionInspect
	case "update":
		req.Action = setupwizard.ActionUpdate
	case "repair":
		req.Action = setupwizard.ActionRepair
	case "":
	default:
		return req, jsonOut, errors.New("invalid_arguments")
	}
	if agents := values["agents"]; agents != "" {
		req.Agents = strings.Split(agents, ",")
	}
	if v, ok := values["hooks"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.Hooks = &on
	}
	if v, ok := values["agent-notify"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.AgentNotify = &on
	}
	if v, ok := values["claude-hooks"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.ClaudeHooks = &on
	}
	if v, ok := values["codex-hooks"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.CodexHooks = &on
	}
	if v, ok := values["claude-agent-notify"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.ClaudeAgentNotify = &on
	}
	if v, ok := values["codex-agent-notify"]; ok {
		on, err := parseBoolFlag(v)
		if err != nil {
			return req, jsonOut, err
		}
		req.CodexAgentNotify = &on
	}
	req.PackageRoot = values["package"]
	req.PluginRoot = values["plugin-root"]
	req.ControlRoot = values["control-root"]
	req.RuntimeRoot = values["runtime-root"]
	req.GlobalConfig = values["global-config"]
	req.CodexHome = values["codex-home"]
	req.ClaudeConfig = values["claude-config"]
	env := setupwizard.ApplyEnvDefaults(setupwizard.Request{})
	req.EnvCodexHome = env.CodexHome
	req.EnvClaudeConfig = env.ClaudeConfig
	for _, path := range []*string{&req.EnvCodexHome, &req.EnvClaudeConfig} {
		if runtime.GOOS == "windows" {
			*path = strings.ReplaceAll(*path, "/", `\`)
		}
		if !filepath.IsAbs(*path) {
			*path = ""
			continue
		}
		*path = filepath.Clean(*path)
	}
	req.ClientExecutable = values["client-executable"]
	if values["claude-executable"] != "" || values["codex-executable"] != "" {
		req.ClientExecutables = map[string]string{}
		if values["claude-executable"] != "" {
			req.ClientExecutables["claude"] = values["claude-executable"]
		}
		if values["codex-executable"] != "" {
			req.ClientExecutables["codex"] = values["codex-executable"]
		}
	}
	req.Helper = values["helper"]
	req.ScopeRoot = values["scope-root"]
	if containsProduct(req.Agents, "cursor") {
		if len(req.Agents) != 1 || req.ScopeRoot == "" || req.ClientExecutable == "" {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		req.CursorConfig = req.ScopeRoot
		req.ClientExecutables = map[string]string{"cursor": req.ClientExecutable}
	}
	req.InstallationID = values["installation-id"]
	if containsProduct(req.Agents, "copilot-vscode") {
		if len(req.Agents) != 1 || req.ScopeRoot == "" || req.ClientExecutable == "" {
			return req, jsonOut, errors.New("invalid_arguments")
		}
		req.ClientExecutables = map[string]string{"copilot-vscode": req.ClientExecutable}
		if values["local-settings"] != "" || values["local-native-stop"] != "" || values["local-mcp"] != "" || values["local-skills"] != "" {
			if values["local-settings"] != filepath.Join(req.ScopeRoot, "settings.json") {
				return req, jsonOut, errors.New("invalid_arguments")
			}
			req.LocalConfig = localWizardConfig(values["local-settings"])
			for key, dest := range map[string]*bool{"local-native-stop": &req.LocalConfig.NativeStop} {
				if value := values[key]; value != "" {
					on, e := parseBoolFlag(value)
					if e != nil {
						return req, jsonOut, e
					}
					*dest = on
				}
			}
			for key, dest := range map[string]*[]string{"local-mcp": &req.LocalConfig.MCPServers, "local-skills": &req.LocalConfig.Skills} {
				if value := values[key]; value != "" {
					on, e := parseBoolFlag(value)
					if e != nil {
						return req, jsonOut, e
					}
					if on {
						name := "agent-notify"
						if key == "local-skills" {
							name = "agent-notifications"
						}
						*dest = []string{name}
					}
				}
			}
		}
	} else if values["local-settings"] != "" || values["local-native-stop"] != "" || values["local-mcp"] != "" || values["local-skills"] != "" {
		return req, jsonOut, errors.New("invalid_arguments")
	}
	if values["mcp-config"] != "" || values["claude-mcp-config"] != "" {
		req.MCPConfig = map[string]string{}
		if values["mcp-config"] != "" {
			req.MCPConfig["codex"] = values["mcp-config"]
		}
		if values["claude-mcp-config"] != "" {
			req.MCPConfig["claude"] = values["claude-mcp-config"]
		}
	}
	return req, jsonOut, nil
}

// MSYS hands native Windows processes absolute paths using forward slashes.
// Canonicalize only that separator form; Clean must still reject traversal and
// nonphysical paths before the values reach the installer or its ACL checks.
func normalizeSetupPhysicalPath(path string) (string, error) {
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", `\`)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("invalid_arguments")
	}
	return path, nil
}

func parseBoolFlag(v string) (bool, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("invalid_arguments")
	}
}

func stdinIsCharDevice() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func quoteWizardArgs(args []string) []string {
	if runtime.GOOS == "windows" {
		return quotePowerShellArgs(args)
	}
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return out
}

func quotePowerShellArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		// Single-quoted PowerShell strings preserve $, backticks, and all other
		// metacharacters; apostrophes are represented by two apostrophes.
		out[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
	}
	if len(out) > 0 {
		out[0] = "& " + out[0]
	}
	return out
}

// Private admission accepts only the bootstrap command. Native product/config
// writers have no new flag. An installed same-release copy can read the original
// private stage while retaining exact helper version/source/digest provenance.
func stripBootstrapIntentFile(args []string) ([]string, string, error) {
	out := []string{}
	path := ""
	for n := 0; n < len(args); n++ {
		key, value, inline := strings.Cut(args[n], "=")
		if key != "--bootstrap-intent-file" {
			out = append(out, args[n])
			continue
		}
		if path != "" {
			return out, path, errors.New("invalid_arguments")
		}
		if !inline {
			n++
			if n >= len(args) {
				return out, path, errors.New("invalid_arguments")
			}
			value = args[n]
		}
		if !validProductPath(value) {
			return out, path, errors.New("invalid_arguments")
		}
		path = value
	}
	return out, path, nil
}
func needsWizardInteraction(r setupwizard.Request) bool { return !r.Yes || len(r.Agents) == 0 }
func admitBootstrapWizardRequest(path string, r setupwizard.Request) (setupwizard.Request, error) {
	r, _, err := admitBootstrapWizardIntent(path, r)
	return r, err
}
func admitBootstrapWizardIntent(path string, r setupwizard.Request) (setupwizard.Request, *confirmedBootstrapIntent, error) {
	provenance, err := currentSelectorProvenance()
	if err != nil {
		return r, nil, err
	}
	provenance.Stage = []byte(filepath.Dir(path))
	intent, err := loadBootstrapIntent(path, provenance)
	if err != nil {
		return r, nil, err
	}
	expected := intentWizardRequest(intent)
	if !sameWizardBootstrapScope(r, expected) || intent.Request.SkipAgentNotify {
		return r, nil, errors.New("bootstrap request differs from confirmed intent")
	}
	r.BootstrapMCP = &intent.MCP
	if containsProduct(intent.Request.Products, "cursor") || containsProduct(intent.Request.Products, "copilot-vscode") {
		generation, policy := intent.Initial.Generation, intent.Initial.Policy
		r.BootstrapExpectedGeneration, r.BootstrapExpectedPolicy = &generation, &policy
	}
	r.EnvClaudeConfig, r.EnvCodexHome = "", ""
	return r, &intent, nil
}

// Record selection uses immutable registration bytes, even when physical assets
// are replaced. It neither allocates a binding nor chooses a sorted consumer.
func recordedCursorRequest(r setupwizard.Request) (portable.Binding, bool, error) {
	ledger, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery || ledger.PendingMutation != nil {
		return portable.Binding{}, false, setupwizard.ErrRefused
	}
	var selected portable.Binding
	found := false
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var b portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &b) != nil {
			return selected, false, portable.ErrInvalid
		}
		if b.Integration != portable.Cursor || b.ScopeRoot != r.ScopeRoot || r.InstallationID != "" && b.InstallationID != r.InstallationID {
			continue
		}
		if found || b.ControlRoot != r.ControlRoot || b.ComponentID != ledger.ID || b.Owner != ledger.Owner || b.RuntimeRoot != ledger.RuntimeRoot || !portable.ExactCommittedBinding(ledger, b) || r.BindingIDs["cursor"] != "" && b.BindingID != r.BindingIDs["cursor"] {
			return selected, false, portable.ErrInvalid
		}
		selected, found = b, true
	}
	return selected, found, nil
}

func fenceCursorIntent(ctx context.Context, r setupwizard.Request, i confirmedBootstrapIntent) error {
	policy, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return err
	}
	l := policy.Installation.Ledger
	if policy.Installation.Recovery || l.PendingMutation != nil || l.ID != i.Initial.LedgerID || l.Owner != i.Initial.Owner || l.Generation != i.Initial.Generation || policy.Preimage != i.Initial.Policy {
		return portablesetup.ErrConcurrentChange
	}
	observation := r
	off := false
	observation.CursorAgentNotify = &off
	current, generation, err := setupwizard.ObserveBootstrapMCP(ctx, observation)
	if err != nil {
		return err
	}
	if generation != i.Initial.Generation {
		return portablesetup.ErrConcurrentChange
	}
	return setupwizard.CheckBootstrapMCP(i.MCP, current)
}

// Use policy-only observation before physical cleanup, retaining any admitted
// original CAS. The predicted output is meaningful only after successful Commit.
func revokeRecordedCursor(ctx context.Context, b portable.Binding, generation *uint64, policy *installruntime.Identity) (installruntime.Ledger, installruntime.Identity, error) {
	s, err := installruntime.ReadRevocationSnapshot(ctx, b.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	if generation != nil && *generation != s.Generation || policy != nil && *policy != s.Preimage {
		return installruntime.Ledger{}, installruntime.Identity{}, portablesetup.ErrConcurrentChange
	}
	off := false
	patch, err := copilotvscodeinstall.CursorPolicyPatch(b, copilotvscodeinstall.CursorChoices{Desktop: &off, Webhook: &off}, nil)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	after, err := installruntime.PredictPolicyIdentity(b.ControlRoot, s.Preimage, patch)
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return installruntime.Ledger{}, installruntime.Identity{}, err
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, RevokeCursor: true, ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: patch})
	if err != nil {
		return ledger, installruntime.Identity{}, err
	}
	return ledger, after, nil
}

func commitConfirmedCursorConsent(ctx context.Context, r setupwizard.Request, result setupwizard.Result, i confirmedBootstrapIntent) (setupwizard.Result, error) {
	deny := func(err error) (setupwizard.Result, error) {
		result.Outcome, result.Reason = "incomplete", "cursor_consent_unavailable"
		return result, err
	}
	if result.Outcome == "cancelled" || result.InstallationID == "" {
		return deny(setupwizard.ErrRefused)
	}
	for _, next := range result.NextActions {
		switch next.Kind {
		case "restart-client", "request-permission", "test-notification":
		default:
			return deny(setupwizard.ErrRefused)
		}
	}
	r.InstallationID = result.InstallationID
	b, found, err := recordedCursorRequest(r)
	if err != nil || !found {
		return deny(setupwizard.ErrRefused)
	}
	acknowledged := false
	for _, target := range result.Targets {
		if target.Client == "cursor" && target.Unit == "agent-notify" && target.Outcome == "completed" && target.Reason == b.BindingID {
			acknowledged = true
		}
	}
	if !acknowledged {
		return deny(setupwizard.ErrRefused)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return deny(err)
	}
	policy := i.Initial.Policy
	if r.BootstrapExpectedPolicy != nil {
		policy = *r.BootstrapExpectedPolicy
	}
	if s.Installation.Recovery || s.Installation.Ledger.PendingMutation != nil || s.Installation.Ledger.ID != i.Initial.LedgerID || s.Installation.Ledger.Owner != i.Initial.Owner || s.Installation.Ledger.Generation != result.Generation || s.Preimage != policy {
		return deny(portablesetup.ErrConcurrentChange)
	}
	cfg, fixed, err := cursorInstalledInputs(ctx, b, s.Installation, filepath.Join(filepath.Dir(b.ControlRoot), "uap", "state"))
	if err != nil {
		return deny(err)
	}
	gate, err := copilotvscodeinstall.NewCursorGate(b, cfg, fixed)
	if err != nil {
		return deny(err)
	}
	binding, err := gate.ConsumerBinding(ctx)
	if err != nil || binding.Generation != result.Generation {
		return deny(setupwizard.ErrRefused)
	}
	previous, err := copilotvscodeinstall.ReadCursorConsent(s, b)
	if err != nil {
		return deny(err)
	}
	patch, err := copilotvscodeinstall.CursorPolicyPatch(b, copilotvscodeinstall.CursorChoices{Desktop: &i.Request.Desktop, Webhook: &i.Request.Webhook}, &previous)
	if err != nil {
		return deny(err)
	}
	key, consumer, _, err := b.Registration()
	if err != nil {
		return deny(err)
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &result.Generation, ExpectedPolicy: &policy, PolicyFields: patch})
	if err != nil {
		return deny(err)
	}
	result.Generation = ledger.Generation
	for n := range result.NextActions {
		if result.NextActions[n].Kind == "request-permission" {
			result.NextActions[n].Command = []string{"setup-notifications", "request-permission", "--control-root", r.ControlRoot, "--expected-generation", fmt.Sprintf("%d", ledger.Generation)}
		}
	}
	return result, nil
}
func sameWizardBootstrapScope(actual, expected setupwizard.Request) bool {
	if containsProduct(expected.Agents, "copilot-vscode") && !sameLocalWizardSelection(actual, expected) {
		return false
	}
	if strings.Join(actual.Agents, ",") != strings.Join(expected.Agents, ",") || actual.ControlRoot != expected.ControlRoot || (!containsProduct(expected.Agents, "cursor") && !containsProduct(expected.Agents, "copilot-vscode") && (actual.ClaudeConfig != expected.ClaudeConfig || actual.CodexHome != expected.CodexHome)) || containsProduct(expected.Agents, "cursor") && actual.CursorConfig != expected.CursorConfig || (containsProduct(expected.Agents, "cursor") && actual.ScopeRoot != expected.ScopeRoot) || actual.GlobalConfig != expected.GlobalConfig {
		return false
	}
	// A fresh legacy phase chooses its existing managed runtime during hooks
	// preparation. A known runtime authority must remain exact; its own creation
	// is outside the relevant portable binding projection.
	if expected.RuntimeRoot != "" && actual.RuntimeRoot != "" && actual.RuntimeRoot != expected.RuntimeRoot {
		return false
	}
	for _, id := range expected.Agents {
		path := actual.ClientExecutables[id]
		if path == "" {
			path = actual.ClientExecutable
		}
		if path != expected.ClientExecutables[id] || actual.MCPConfig[id] != expected.MCPConfig[id] {
			return false
		}
	}
	return true
}

// The same finite UI grammar applies to direct wizard questions. Complete
// --yes/inspect/JSON paths validate it without constructing a terminal.
func stripWizardUIMode(args []string) ([]string, string, error) {
	remaining, uiArgs := []string{}, []string{"select"}
	for n := 0; n < len(args); n++ {
		key, _, inline := strings.Cut(args[n], "=")
		if key != "--ui" && key != "--plain" {
			remaining = append(remaining, args[n])
			continue
		}
		uiArgs = append(uiArgs, args[n])
		if key == "--ui" && !inline {
			n++
			if n >= len(args) {
				return remaining, "", errors.New("invalid_arguments")
			}
			uiArgs = append(uiArgs, args[n])
		}
	}
	parsed, err := parseSetupProducts(uiArgs)
	return remaining, parsed.Mode, err
}

// Immutable accepted source proof: public selected projection/ack/data, eight
// refusals and sibling/recovery on Ubuntu24.04, 6.17.0-1022-azure, ext4. This
// qualifies no other host/profile and supplies no installed native E or consent.
const cursorP1Qualification = "uap406:run37381034498:job112002773007:public098:commit0983418ab0c2724b5cf648512e0a26baafaad546:sha256:7d305d6cabe775e12083c5c4f49fe9246eb1cb8244d596d03c50aa1e7de670b5"
const selectedCursorVersion = "2026.09.28-64d2043"

func composeCursorWizard(ctx context.Context, req setupwizard.Request) (_ setupwizard.Request, err error) {
	if !containsProduct(req.Agents, "cursor") {
		return req, nil
	}
	if err := ctx.Err(); err != nil {
		return req, err
	}
	if len(req.Agents) != 1 || req.CursorConfig != req.ScopeRoot || !validProductPath(req.ScopeRoot) {
		return req, portable.ErrInvalid
	}
	profile, err := cursor.New().ResolveProfileRoot(req.CursorConfig)
	if err != nil {
		return req, err
	}
	req.ScopeRoot, req.CursorConfig = profile, profile
	if req.BootstrapMCP != nil && !containsProduct(req.BootstrapMCP.Selected, "cursor") {
		off := false
		req.CursorAgentNotify = &off
	}
	if req.CursorAgentNotify != nil && !*req.CursorAgentNotify || req.AgentNotify != nil && !*req.AgentNotify || req.Action == setupwizard.ActionInspect && req.AgentNotify == nil {
		return req, nil
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return req, err
	}
	if snapshot.Recovery || snapshot.Ledger.ID == "" || snapshot.Ledger.Owner != "existing-installer" {
		return req, setupwizard.ErrRefused
	}
	if req.AgentNotify == nil && (req.Action == setupwizard.ActionUpdate || req.Action == setupwizard.ActionRepair) {
		state, e := (statev2.Store{Path: filepath.Join(filepath.Dir(req.ControlRoot), "uap", "state", "state-v2.json")}).Load()
		if e != nil {
			return req, e
		}
		live := false
		for _, installation := range state.Installations {
			for _, binding := range installation.Clients {
				live = live || binding.ClientID == "cursor"
			}
		}
		if !live {
			off := false
			req.CursorAgentNotify = &off
			return req, nil
		}
	}
	if req.Action != setupwizard.ActionUninstall {
		// Fresh Capture belongs to public Prepare. Its frozen original token is
		// retained/revalidated by Apply and lifecycle, never recaptured here.
		agent := req.ClientExecutables["cursor"]
		if agent == "" {
			agent = req.ClientExecutable
		}
		agent, err = normalizeProductExecutable(agent)
		if err != nil {
			return req, err
		}
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		version, e := clientdetect.NewOS(filepath.Dir(req.CursorConfig)).ProbeVersionWithEnvironment(probe, agent, []string{"HOME=" + filepath.Dir(req.CursorConfig), "PATH=" + os.Getenv("PATH")})
		cancel()
		if e != nil || strings.TrimSpace(version) != selectedCursorVersion {
			return req, fmt.Errorf("cursor agent version is outside the fixed tuple: %w", e)
		}
		req.ClientExecutables = map[string]string{"cursor": agent}
		if filepath.Base(req.CursorConfig) != ".cursor" {
			return req, setupwizard.ErrRefused
		}
		if err := cursorQualifiedNamespace(req.CursorConfig); err != nil {
			return req, err
		}
	}
	acquiring := req.PackageRoot == "" && snapshot.Ledger.PendingMutation == nil
	req, cfg, previous, cleanup, err := reserveCursorCaller(ctx, req, snapshot)
	if err != nil {
		return req, err
	}
	defer func() {
		if err != nil {
			cleanup() // Composition is still read-only; Plan/Run has not begun.
		}
	}()
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, req.CursorAuthority)
	if err != nil {
		return req, err
	}
	base, err := portablesetup.NewRegistry()
	if err != nil {
		return req, err
	}
	cfg.Registry, err = clients.NewRegistry(append(base.All(), adapter)...)
	if err != nil {
		return req, err
	}
	eng, err := uapinstaller.New(cfg)
	if err != nil {
		return req, err
	}
	// The selected registry, constructed from acknowledged facts, verifies the
	// original token. Historical Cursor is used only for inert reservation.
	if previous && req.Action != setupwizard.ActionUninstall {
		if err := eng.VerifyProfileAuthority(ctx, req.InstallationID, req.BindingIDs["cursor"]); err != nil {
			return req, err
		}
	}
	// Retained identity and Authority above are inert. Package admission starts
	// only after the selected registry verifies the original physical token.
	if previous && req.Action != setupwizard.ActionUninstall {
		if snapshot.Ledger.PendingMutation != nil {
			intent, e := portablesetup.ReadIntent(req.ControlRoot)
			if e != nil {
				return req, e
			}
			req, err = setupwizard.ResolvePendingPackage(ctx, req, intent)
		} else if req.Action == setupwizard.ActionUpdate && req.PackageRoot == "" {
			req, cleanup, err = setupwizard.StageCurrentReleasePackage(ctx, req)
		}
		if err != nil {
			return req, err
		}
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: "cursor", InstallationID: req.InstallationID, ClientConfigRoot: req.CursorConfig, DeclaredName: cursorCallerDeclaredName(req.PackageRoot)})
	if err != nil || reserved.InstallationID != req.InstallationID || reserved.BindingID != req.BindingIDs["cursor"] {
		return req, portable.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return req, err
	}
	if acquiring {
		req, err = setupwizard.RetainCurrentReleasePackage(ctx, req)
	}
	return req, err
}

// Inert reservation is independent of physical identity and channel consent.
// It binds the real managed observer, never a future helper or the vendor agent.
func reserveCursorCaller(ctx context.Context, req setupwizard.Request, snapshot installruntime.InstalledSnapshot) (_ setupwizard.Request, _ uapinstaller.Config, _ bool, cleanup func(), err error) {
	cleanup = func() {}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	uapRoot := filepath.Join(filepath.Dir(req.ControlRoot), "uap")
	cfg := uapinstaller.Config{StateRoot: filepath.Join(uapRoot, "state"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed")}
	deny := func(err error) (setupwizard.Request, uapinstaller.Config, bool, func(), error) {
		return req, cfg, false, cleanup, err
	}
	if snapshot.Recovery || snapshot.Ledger.ID == "" || snapshot.Ledger.Owner != "existing-installer" || req.RuntimeRoot != "" && req.RuntimeRoot != snapshot.Ledger.RuntimeRoot {
		return deny(setupwizard.ErrRefused)
	}
	// Pending identity is selected before reservation; the wizard remains the
	// owner of retry unit/digest validation and uncertain recovery.
	var pendingIntent *portablesetup.Intent
	if pending := snapshot.Ledger.PendingMutation; pending != nil {
		intent, e := portablesetup.ReadIntent(req.ControlRoot)
		if e != nil || pending.Owner != "existing-installer" || pending.IntentRef != portablesetup.IntentPath(req.ControlRoot) || intent.SetupIntentID != pending.ID || intent.Action != string(req.Action) || len(intent.Targets) != 1 {
			return deny(setupwizard.ErrRefused)
		}
		target := intent.Targets[0]
		if target.Client != "cursor" || target.InstallationID == "" || target.BindingID == "" || target.Profile != req.CursorConfig || req.InstallationID != "" && req.InstallationID != target.InstallationID || req.BindingIDs["cursor"] != "" && req.BindingIDs["cursor"] != target.BindingID {
			return deny(portablesetup.ErrIntentConflict)
		}
		req.InstallationID = target.InstallationID
		req.BindingIDs = map[string]string{"cursor": target.BindingID}
		pendingIntent = &intent
	}
	req.RuntimeRoot = snapshot.Ledger.RuntimeRoot
	generation := snapshot.Ledger.Generation
	if req.BootstrapExpectedGeneration != nil && *req.BootstrapExpectedGeneration != generation {
		return deny(portablesetup.ErrConcurrentChange)
	}
	req.BootstrapExpectedGeneration = &generation
	base, err := portablesetup.NewRegistry()
	if err != nil {
		return deny(err)
	}
	cfg.Registry, err = clients.NewRegistry(append(base.All(), cursor.New())...)
	if err != nil {
		return deny(err)
	}
	eng, err := uapinstaller.New(cfg)
	if err != nil {
		return deny(err)
	}
	reserved, err := eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: "cursor", InstallationID: req.InstallationID, Allocate: req.Action == setupwizard.ActionInstall && snapshot.Ledger.PendingMutation == nil, ClientConfigRoot: req.CursorConfig})
	if err != nil {
		return deny(err)
	}
	req.InstallationID = reserved.InstallationID
	primary, found, err := portable.InstalledPrimary(snapshot.Ledger, req.InstallationID, req.ControlRoot)
	if err != nil {
		return deny(err)
	}
	if !found {
		primary = portable.PlatformPrimary()
	}
	if req.Primary != "" && req.Primary != primary {
		return deny(portable.ErrInvalid)
	}
	req.Primary = primary
	observer, err := portable.ResolvePrimaryExecutable(snapshot.Ledger, primary)
	if err != nil || req.Helper != "" && req.Helper != observer {
		return deny(portable.ErrInvalid)
	}
	req.Helper = observer
	global, found, err := portable.InstalledGlobalConfig(snapshot.Ledger, req.InstallationID, req.ControlRoot)
	if err != nil {
		return deny(err)
	}
	if found && req.GlobalConfig != "" && req.GlobalConfig != global {
		return deny(portable.ErrInvalid)
	}
	if found {
		req.GlobalConfig = global
	}
	if req.GlobalConfig == "" {
		selection, e := config.Resolve(config.SnapshotEnv())
		if e != nil {
			return deny(e)
		}
		req.GlobalConfig = selection.Path
	}
	if pendingIntent != nil {
		if pendingIntent.Primary != "" && pendingIntent.Primary != req.Primary || pendingIntent.GlobalConfig != "" && pendingIntent.GlobalConfig != req.GlobalConfig {
			return deny(portablesetup.ErrIntentConflict)
		}
		view, e := eng.Inspect(ctx)
		if e != nil || view.Recovery.Required {
			return deny(setupwizard.ErrRefused)
		}
	}
	if reserved.BindingID == "" {
		if snapshot.Ledger.PendingMutation != nil || req.Action != setupwizard.ActionInstall {
			return deny(uapinstaller.ErrNotInstalled)
		}
		if req.PackageRoot == "" {
			req, cleanup, err = setupwizard.StageCurrentReleasePackage(ctx, req)
			if err != nil {
				return deny(err)
			}
		}
		name := cursorCallerDeclaredName(req.PackageRoot)
		if name == "" {
			return deny(portable.ErrInvalid)
		}
		reserved, err = eng.ReserveIdentity(uapinstaller.IdentityRequest{ClientID: "cursor", InstallationID: reserved.InstallationID, DeclaredName: name, ClientConfigRoot: req.CursorConfig})
		if err != nil || reserved.BindingID == "" || reserved.TargetPath == "" {
			return deny(portable.ErrInvalid)
		}
		req.CursorAuthority = &cursorinstall.Authority{ObjectID: "cursor-user-stop-v1", CursorVersion: selectedCursorVersion, QualificationID: cursorP1Qualification}
		req.CursorAuthority.Selector = filepath.Join(cfg.PluginDataBase, domain.ComputePhysicalArtifactID(name, reserved.InstallationID)) // completed below
	}
	if reserved.InstallationID == "" || reserved.Scope != string(domain.ScopeUser) || reserved.BindingID != domain.ComputeClientBindingID(reserved.InstallationID, "cursor", reserved.Scope, reserved.TargetPath) {
		return deny(portable.ErrInvalid)
	}
	req.InstallationID = reserved.InstallationID
	if req.BindingIDs != nil && req.BindingIDs["cursor"] != "" && req.BindingIDs["cursor"] != reserved.BindingID {
		return deny(portable.ErrInvalid)
	}
	req.BindingIDs = map[string]string{"cursor": reserved.BindingID}
	state, err := (statev2.Store{Path: filepath.Join(cfg.StateRoot, "state-v2.json")}).Load()
	if err != nil {
		return deny(err)
	}
	dataRoot := ""
	previous := false
	var installed domain.Installation
	var client domain.ClientBinding
	for _, installation := range state.Installations {
		if installation.InstallationID != req.InstallationID {
			continue
		}
		if c, ok := installation.Clients[reserved.BindingID]; ok {
			data, ok := installation.DataReceipts[c.DataReceiptID]
			if !ok || c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" {
				return deny(portable.ErrInvalid)
			}
			dataRoot, previous = data.Locator, true
			installed, client = installation, c
		}
	}
	if !previous {
		if req.CursorAuthority == nil {
			return deny(portable.ErrInvalid)
		}
		dataRoot = req.CursorAuthority.Selector
	}
	id := portablesetup.Identity{InstallationID: req.InstallationID, ComponentID: snapshot.Ledger.ID, Owner: snapshot.Ledger.Owner, ScopeRoot: req.CursorConfig, ControlRoot: req.ControlRoot, GlobalConfig: req.GlobalConfig, RuntimeRoot: req.RuntimeRoot, Primary: primary}
	binding, err := portablesetup.Complete(id, portable.Cursor, "cursor", reserved.Scope, reserved.TargetPath, dataRoot)
	if err != nil || binding.BindingID != reserved.BindingID {
		return deny(portable.ErrInvalid)
	}
	if previous {
		committed, found, e := portable.ResolveCommittedBinding(snapshot.Ledger, binding)
		if e != nil {
			return deny(portable.ErrInvalid)
		}
		var fixed cursorinstall.Authority
		if found {
			_, fixed, e = cursorInstalledInputs(ctx, committed, snapshot, cfg.StateRoot)
		} else {
			fixed, e = cursorPendingHandoff(binding, snapshot, cfg, installed, client, pendingIntent)
		}
		if e != nil || fixed.QualificationID != cursorP1Qualification {
			return deny(portable.ErrInvalid)
		}
		req.CursorAuthority = &fixed
		if pendingIntent != nil && req.Action != setupwizard.ActionUninstall {
			// The committed source is usable only if it is this exact candidate;
			// an acknowledged A cannot stand in for a frozen update B.
			if req.PackageRoot == "" && pendingIntent.TreeDigest != "" && installed.Source.TreeDigest == pendingIntent.TreeDigest {
				req.PackageRoot = installed.Source.CanonicalSource
			}
		}
	} else {
		name, e := binding.Filename()
		if e != nil {
			return deny(e)
		}
		req.CursorAuthority.ProfileRoot, req.CursorAuthority.Executable = req.CursorConfig, observer
		req.CursorAuthority.ExecutableDigest = "sha256:" + snapshot.Ledger.Files[observer].SHA256
		req.CursorAuthority.Selector = filepath.Join(binding.DataRoot, name)
	}
	return req, cfg, previous, cleanup, nil
}

// This admits only the public pre-native first handoff. It does not grant
// installed delivery eligibility or synthesize a runtime consumer.
func cursorPendingHandoff(b portable.Binding, snap installruntime.InstalledSnapshot, cfg uapinstaller.Config, installation domain.Installation, c domain.ClientBinding, intent *portablesetup.Intent) (cursorinstall.Authority, error) {
	deny := func() (cursorinstall.Authority, error) { return cursorinstall.Authority{}, portable.ErrInvalid }
	if intent == nil || intent.Action != "install" || intent.Stage != "confirmed" || intent.ExpectedGeneration+1 != snap.Ledger.Generation || len(intent.Targets) != 1 || b.CheckPrimaryFile(snap) != nil || installation.NeedsRebind ||
		c.ClientBindingID != b.BindingID || c.ClientBindingID != domain.ComputeClientBindingID(b.InstallationID, c.ClientID, c.Scope, c.TargetLocator) || c.ClientID != "cursor" || c.Scope != b.ScopeID || c.NativeProfileRoot != b.ScopeRoot || c.ProfileNamespace != cfg.StateRoot || c.ProfileAuthority == nil || c.ProfileAuthority.IsZero() || c.ProfileAuthority.Facts().CanonicalRoot != b.ScopeRoot ||
		c.PendingNativeIntent != nil || c.NativeActivationAttempt != "" || c.Activation != domain.ActivationPrepared || c.Verification != domain.VerificationPackageValid || c.Materialization != domain.MaterializationMaterialized || c.SelectedDelivery.ValidateClient(domain.ClientCursor) != nil {
		return deny()
	}
	target, data := intent.Targets[0], installation.DataReceipts[c.DataReceiptID]
	if target.OldBinding != nil || target.NewBinding != nil || target.OldConsumerKey != "" || target.NewConsumerKey != "" || target.DataReceiptID != "" && target.DataReceiptID != c.DataReceiptID || data.DataReceiptID != c.DataReceiptID || data.PhysicalBackend != c.PhysicalArtifact || data.State != domain.DataReceiptOwned || data.Locator != b.DataRoot || intent.Primary != b.Primary || intent.GlobalConfig != b.GlobalConfig || intent.TreeDigest != installation.Source.TreeDigest {
		return deny()
	}
	for _, object := range c.NativeObjects {
		if object.Kind != "managed_package_directory" || object.CursorReceipt != (domain.CursorHookReceipt{}) {
			return deny()
		}
	}
	facts, ok := c.SelectedDelivery.CursorFacts()
	name, err := b.Filename()
	observer, e := portable.ResolvePrimaryExecutable(snap.Ledger, b.Primary)
	if !ok || err != nil || e != nil || c.SelectedDelivery.Validate() != nil || facts.CanonicalDigest != intent.TreeDigest || facts.ProfileRoot != b.ScopeRoot || facts.Selector != filepath.Join(b.DataRoot, name) || facts.Executable != observer {
		return deny()
	}
	return cursorinstall.Authority{ProfileRoot: facts.ProfileRoot, CursorVersion: facts.CursorVersion, QualificationID: facts.QualificationID, Executable: observer, ExecutableDigest: "sha256:" + snap.Ledger.Files[observer].SHA256, Selector: facts.Selector, ObjectID: facts.ObjectID}, nil
}

// A narrow filter for the supplied Ubuntu24.04.5/6.17.0-1022-azure/ext4 P1
// observation. Mount metadata only limits evidence scope; UUID/inode authority
// comes exclusively from public complete-ancestry Capture/Revalidate.
func cursorQualifiedNamespace(profile string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return setupwizard.ErrRefused
	}
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil || strings.TrimSpace(string(kernel)) != "6.17.0-1022-azure" {
		return setupwizard.ErrRefused
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains("\n"+string(osRelease), "\nID=ubuntu\n") || !strings.Contains(string(osRelease), `VERSION="24.04.5 LTS`) {
		return setupwizard.ErrRefused
	}
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil || len(mounts) > 1<<20 {
		return setupwizard.ErrRefused
	}
	return cursorQualifiedMounts(profile, string(mounts))
}

func cursorQualifiedMounts(profile, mounts string) error {
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for path := profile; ; path = filepath.Dir(path) {
		longest, fs := -1, ""
		ambiguous := false
		for _, row := range strings.Split(mounts, "\n") {
			left, right, ok := strings.Cut(row, " - ")
			fields, kind := strings.Fields(left), strings.Fields(right)
			if !ok || len(fields) < 6 || len(kind) < 3 {
				continue
			}
			mount := decode.Replace(fields[4])
			if mount != "/" && path != mount && !strings.HasPrefix(path, mount+"/") {
				continue
			}
			if len(mount) > longest {
				longest, fs = len(mount), kind[0]
				ambiguous = false
			} else if len(mount) == longest {
				// Row order does not establish which stacked mount is visible.
				ambiguous = true
			}
		}
		if ambiguous || fs != "ext4" {
			return setupwizard.ErrRefused
		}
		if path == "/" {
			return nil
		}
	}
}

func cursorCallerDeclaredName(root string) string {
	var body []byte
	var err error
	if strings.EqualFold(filepath.Ext(root), ".zip") {
		archive, e := zip.OpenReader(root)
		if e != nil {
			return ""
		}
		defer func() { _ = archive.Close() }()
		file, e := archive.Open("plugin.json")
		if e != nil {
			return ""
		}
		defer func() { _ = file.Close() }()
		body, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
	} else {
		body, err = os.ReadFile(filepath.Join(root, "plugin.json"))
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err != nil || len(body) > 1<<20 || json.Unmarshal(body, &manifest) != nil {
		return ""
	}
	return manifest.Name
}
