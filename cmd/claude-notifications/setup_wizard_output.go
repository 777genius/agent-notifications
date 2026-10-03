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
		_, _ = fmt.Fprintln(out, "Details: "+result.Reason)
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
			_, _ = fmt.Fprintln(out, "    MCP config: "+target.ConfigPath)
		}
		for _, warning := range target.Warnings {
			_, _ = fmt.Fprintln(out, "    Warning: "+warning)
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

func setupPrintableCommand(command []string) string {
	return strings.Join(quoteWizardArgs(wizardPrintableCommand(command)), " ")
}

func setupClientName(client string) string {
	switch client {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	}
	return client
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
