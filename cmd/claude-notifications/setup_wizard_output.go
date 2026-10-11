package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

// Installation and observed delivery are separate facts. A completed mutation
// must never imply that the client restarted, trusted hooks, or displayed an alert.
func writeSetupWizardSummary(out io.Writer, result setupwizard.Result) {
	if result.Action != "inspect" && result.Outcome != "completed" && result.Outcome != "unchanged" && result.Outcome != "cancelled" {
		writeSetupWizardIncomplete(out, result)
		return
	}
	title := "Setup " + readableSetupState(result.Outcome)
	if result.Action == "inspect" {
		title = "Notification setup status"
	} else if result.Outcome == "completed" {
		switch result.Action {
		case "install":
			title = "Installation complete"
		case "update":
			title = "Update complete"
		case "repair":
			title = "Repair complete"
		case "uninstall":
			title = "Removal complete"
		}
	}
	_, _ = fmt.Fprintln(out, title)
	if result.Reason != "" {
		_, _ = fmt.Fprintln(out, "Details: "+safeSetupText(result.Reason))
	}
	if result.DataRetained {
		_, _ = fmt.Fprintln(out, "Settings and notification data were retained.")
	}
	for _, target := range result.Targets {
		unit := target.Unit
		switch unit {
		case "agent-notify":
			unit = "notification tool and skill"
		case "hooks":
			unit = "automatic notification hooks"
		case "direct-mcp":
			unit = "MCP registration"
		}
		state := readableSetupState(target.Outcome)
		if result.Action == "uninstall" && target.Outcome == "completed" {
			state = "removed"
		}
		_, _ = fmt.Fprintf(out, "  %s - %s: %s", setupClientName(target.Client), unit, state)
		if target.Reason != "" && target.Outcome != "completed" && target.Outcome != "installed" {
			_, _ = fmt.Fprintf(out, " (%s)", target.Reason)
		}
		_, _ = fmt.Fprintln(out)
		if target.ConfigPath != "" {
			_, _ = fmt.Fprintln(out, "    MCP config: "+safeSetupText(target.ConfigPath))
		}
		for _, warning := range target.Warnings {
			_, _ = fmt.Fprintln(out, "    Warning: "+safeSetupText(warning))
		}
	}
	for _, fact := range result.Readiness {
		_, _ = fmt.Fprintf(out, "  %s - runtime: %s; hooks: %s; notification tool: %s\n", setupClientName(fact.Client), readableSetupState(fact.Runtime), readableSetupState(fact.Hooks), readableSetupState(fact.MCP))
		_, _ = fmt.Fprintf(out, "    Permission: %s. Restart: %s. Delivery: %s.\n", readableSetupState(fact.Permission), readableSetupState(fact.Restart), readableSetupState(fact.Delivery))
	}
	if len(result.NextActions) > 0 || len(result.Command) > 0 {
		_, _ = fmt.Fprintln(out, "\nNext steps:")
		if runtime.GOOS == "windows" {
			_, _ = fmt.Fprintln(out, "Commands below use PowerShell syntax.")
		}
	}
	for _, next := range result.NextActions {
		clients := setupClientNames(next.Agents)
		switch next.Kind {
		case "restart-client":
			_, _ = fmt.Fprintln(out, "  Restart "+clients+" (exit and reopen).")
		case "request-permission":
			granted := len(result.Readiness) > 0
			for _, fact := range result.Readiness {
				if fact.Permission != "allowed" && fact.Permission != "granted" {
					granted = false
				}
			}
			if granted {
				continue
			}
			_, _ = fmt.Fprintln(out, "  Check OS notification permission; grant it if needed:")
			if len(next.Command) > 0 {
				_, _ = fmt.Fprintln(out, "    "+setupPrintableCommand(next.Command))
			}
		case "test-notification":
			_, _ = fmt.Fprintln(out, "  Ask "+clients+" to send a test notification using the notify tool.")
		default:
			// Keep recovery kind/reason and the exact command useful to operators.
			_, _ = fmt.Fprintf(out, "  next %s:", next.Kind)
			if next.Reason != "" {
				_, _ = fmt.Fprint(out, " "+next.Reason)
			}
			_, _ = fmt.Fprintln(out)
			if len(next.Command) > 0 {
				_, _ = fmt.Fprintln(out, "    "+setupPrintableCommand(next.Command))
			}
		}
	}
	if len(result.Command) > 0 {
		_, _ = fmt.Fprintln(out, "  retry: "+setupPrintableCommand(result.Command))
	}
	// Trust applies to installed Codex hooks, never to an MCP-only installation.
	if result.Action != "inspect" && result.Action != "uninstall" {
		for _, target := range result.Targets {
			if target.Client == "codex" && target.Unit == "hooks" && (target.Outcome == "completed" || target.Outcome == "installed") {
				_, _ = fmt.Fprintln(out, "  In Codex, run /hooks, review the entries and trust them.")
				break
			}
		}
	}
}

// Failure defaults contain only finite presentation categories. Raw CLI errors,
// paths and full recovery argv remain in the machine result and private log.
func writeSetupWizardIncomplete(out io.Writer, result setupwizard.Result) {
	_, _ = fmt.Fprintln(out, "Setup incomplete: "+setupFailureDescription(result))
	for _, target := range result.Targets {
		if target.Client != "claude" && target.Client != "codex" && target.Client != "opencode" && target.Client != "gemini" && target.Client != "cursor" {
			continue
		}
		state := "needs inspection"
		switch target.Outcome {
		case "completed", "installed":
			state = "activation verified"
		case "activation_timeout":
			state = "activation timed out"
		case "activation_not_attempted":
			state = "activation not started"
		case "activation_canceled":
			state = "activation canceled"
		case "activation_deadline":
			state = "activation deadline reached"
		case "activation_failed":
			state = "activation failed"
		}
		if target.Unit == "hooks" {
			if target.Outcome == "completed" || target.Outcome == "installed" {
				state = "hooks installed"
			}
		} else if target.Materialization == "materialized" || target.Materialization == "degraded" {
			state = "registration saved; " + state
		}
		_, _ = fmt.Fprintf(out, "  %s  %s\n", setupClientName(target.Client), state)
	}
	_, _ = fmt.Fprintln(out, "  Delivery  not verified")
	for _, next := range result.NextActions {
		if usableInstallerRecovery(next) {
			_, _ = fmt.Fprintln(out, "Next: Resume saved activation; use --json to view its command.")
			return
		}
	}
	_, _ = fmt.Fprintln(out, "Next: Inspect setup status before retrying.")
}

func setupFailureDescription(result setupwizard.Result) string {
	for _, target := range result.Targets {
		switch target.Outcome {
		case "activation_timeout":
			return setupClientName(target.Client) + " plugin verification timed out."
		case "activation_canceled":
			return "activation was canceled."
		case "activation_deadline":
			return "the setup deadline was reached."
		case "activation_failed":
			return setupClientName(target.Client) + " activation failed."
		}
	}
	switch result.Reason {
	case "concurrent_change", "pending_intent_conflict":
		return "saved setup state changed; review the selection."
	case "managed_runtime_required":
		return "a managed runtime is required."
	case "package_acquisition_failed", "recorded_package_unavailable", "package_required":
		return "the installation package is unavailable."
	case "invalid_arguments", "invalid_action":
		return "setup arguments are invalid."
	}
	return "installation needs inspection."
}

func safeSetupText(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 32 || r >= 127 && r <= 159 {
			return ' '
		}
		return r
	}, value)), " ")
}

func setupPrintableCommand(command []string) string {
	return strings.Join(quoteWizardArgs(wizardPrintableCommand(command)), " ")
}

func setupClientName(client string) string {
	switch client {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	case "gemini":
		return "Gemini CLI"
	}
	return "Selected client"
}

func setupClientNames(clients []string) string {
	if len(clients) == 0 {
		return "your selected agent"
	}
	names := make([]string, len(clients))
	for i, client := range clients {
		names[i] = setupClientName(client)
	}
	return strings.Join(names, " and ")
}

func readableSetupState(state string) string {
	switch state {
	case "not_checked", "":
		return "not checked"
	case "not_verified":
		return "not verified"
	case "pending":
		return "required"
	case "not_required":
		return "not required"
	case "completed":
		return "installed"
	case "absent":
		return "not installed"
	case "unchanged":
		return "unchanged"
	}
	return strings.ReplaceAll(state, "_", " ")
}
