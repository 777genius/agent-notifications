package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/cli/installerui"
)

const setupWizardHelp = `Usage: claude-notifications setup-notifications wizard [OPTIONS]
Master for hooks, portable MCP/skill and the native Pi extension.
TTY stdin prompts for agents, an existing-install action, and units when those flags are omitted, then shows a preflight plan and asks for confirmation.
When Claude and Codex already have different units, the TTY shows both and does not collapse omission to one bool.
--json never prompts. Progress phases go to stderr. No TTY and no --agents/--yes is invalid for mutation, not a hang. Omitted --agents with --yes is still invalid unless a matching pending intent restores them.
Inspect exit 0 means the report was read; readiness stays in result fields.
Without --action, a new machine defaults to install; an existing portable
installation is offered inspect, add/reinstall, uninstall, update, or repair.
  --action install|uninstall|inspect|update|repair
  --install-or-update       Bootstrap-only: install selected clients or update owned bindings first
  --preserve-existing-units Bootstrap auto mode: leave absent MCP units off when another binding already exists
  --agents claude,codex|pi|cursor|copilot-vscode   Cursor requires explicit --scope-root and --client-executable; select separately
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
  --pi-agent-dir PATH         Pi profile; else PI_CODING_AGENT_DIR or ~/.pi/agent once
  --pi-executable PATH        Native Pi executable; Windows requires standalone .exe
  --pi-desktop true|false     Pi desktop consent; omission preserves existing choice
  --pi-webhook true|false     Pi webhook consent; omission preserves existing choice
  --client-executable PATH    Selected client executable; never launched from PATH
  --claude-executable PATH    Claude executable when installing both clients
  --codex-executable PATH     Codex executable when installing both clients
  --helper PATH               Managed stdio helper (default: runtime primary)
  --scope-root PATH
  --local-settings PATH       Explicit Local settings.json in the selected profile
  --local-native-stop true|false  Independent Local Stop selection
  --local-mcp true|false       Independent Local MCP selection
  --local-skills true|false    Independent Local skill selection
  --local-mcp-names JSON      Exact MCP name array; requires --local-mcp true
  --local-skill-names JSON    Exact skill name array; requires --local-skills true
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
	args, bootstrapReadyClients, readyClientsErr := stripBootstrapReadyClients(args)
	args, bootstrapProgressFile, progressFileErr := stripBootstrapProgressFile(args)
	if readyClientsErr == nil {
		readyClientsErr = progressFileErr
	}
	if intentFlagErr == nil {
		intentFlagErr = readyClientsErr
	}
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
	if (len(bootstrapReadyClients) > 0 || bootstrapProgressFile != "") && bootstrapIntentFile == "" {
		return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, nil)
	}
	if bootstrapIntentFile != "" {
		if !installOrUpdate || !req.Yes || jsonOut && needsWizardInteraction(req) {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, nil)
		}
		req, confirmed, err = admitBootstrapWizardIntent(bootstrapIntentFile, req)
		if err != nil {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "conflict", Reason: "concurrent_change"}, err)
		}
		if err := admitBootstrapReadyClients(&req, bootstrapReadyClients); err != nil {
			return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, err)
		}
		if bootstrapProgressFile != "" {
			progress, closeProgress, progressErr := openBootstrapProgress(bootstrapIntentFile, bootstrapProgressFile)
			if progressErr != nil {
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "invalid", Reason: "invalid_arguments"}, progressErr)
			}
			defer closeProgress()
			req.ClientProgress = progress
		}
	}
	if errOut != nil {
		req.StateMigrationProgress = func(m setupwizard.StateMigrationOutcome) {
			if m.BackupPath != "" {
				_, _ = fmt.Fprintln(errOut, "UAP registry backup: "+setupDisplayPath(m.BackupPath))
			}
			_, _ = fmt.Fprintf(errOut, "UAP registry migration %s: schema %d -> %d.\n", m.Outcome, m.SourceSchema, m.TargetSchema)
		}
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
				return writeSetupWizardResult(out, jsonOut, setupwizard.Result{Action: string(req.Action), Outcome: "unchanged", Reason: "existing_opt_out", Targets: setupwizard.PreservedUnitTargets(confirmed.MCP.Skipped)}, nil)
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
	if confirmed != nil && (containsProduct(req.Agents, "cursor") || containsProduct(req.Agents, "copilot-vscode")) {
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
		result, err = setupwizard.InstallOrUpdate(ctx, req, preserveExistingUnits)
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
