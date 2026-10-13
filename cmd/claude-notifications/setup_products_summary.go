package main

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

func escapeProductNotice(raw string) ([]string, error) {
	return escapeProductRows([]string{raw})
}

const setupProductsConfirmationTitle = "Apply this plan?"

func setupProductsConfirmationRows(rows []string, rich bool) ([]string, error) {
	help := "Press Enter or type y to install; type n to cancel."
	if rich {
		help = "Yes is selected initially. Press Enter to install; Right selects No to cancel."
	}
	result := append([]string{help, ""}, rows...)
	total := len(setupProductsConfirmationTitle)
	for _, row := range result {
		total += len(row)
	}
	if len(result) > 128 || total > 64<<10 {
		return nil, errors.New("confirmation summary budget exceeded")
	}
	return result, nil
}

// Preserve the shared encoder's reversible escaping and limits. Its enclosing
// quotes are presentation punctuation, unnecessary for an installation summary.
func escapeProductRows(raw []string) ([]string, error) {
	rows, err := setupwizard.EscapeConfirmationRows(raw)
	if err != nil {
		return nil, err
	}
	for n, row := range rows {
		rows[n] = strings.TrimSuffix(strings.TrimPrefix(row, `"`), `"`)
	}
	return rows, nil
}

func bootstrapOnOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// Delimit unusual boundary whitespace so a different filesystem authority
// cannot look like the ordinary path. The shared encoder still escapes bytes.
func bootstrapAuthority(value string) string {
	if strings.TrimSpace(value) != value {
		return strconv.QuoteToASCII(value)
	}
	return value
}

func bootstrapScopeLabel(key string) string {
	labels := map[string]string{
		"home": "Home directory", "claude-config": "Claude Code settings", "claude-mcp-config": "Claude Code MCP settings",
		"codex-home": "Codex home", "codex-mcp-config": "Codex MCP settings", "opencode-config-dir": "OpenCode settings",
		"gemini-config-root": "Gemini settings", "control-root": "Shared installation state", "runtime-root": "Notification runtime",
		"global-config": "Shared notification settings", "claude-executable": "Claude Code executable", "codex-executable": "Codex executable",
		"opencode-executable": "OpenCode executable", "gemini-executable": "Gemini executable",
		"scope-root": "Cursor workspace", "client-executable": "Cursor agent executable", "local-settings": "VS Code Local settings",
	}
	if label, ok := labels[key]; ok {
		return label
	}
	return key
}

func bootstrapPreservedPolicyRows(policy map[string]json.RawMessage) ([]string, error) {
	validated, err := setupwizard.BootstrapPolicyRows(policy)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"preserved enabled": "Notification service", "preserved route localRouting": "Local click navigation",
		"preserved route allowUnknownCaller": "Allow unrecognized callers", "preserved route allowCallerAsserted": "Allow caller-asserted identity",
		"preserved route applicationPath": "Desktop application", "preserved route teamID": "Application signing team",
	}
	rows := []string{}
	for _, row := range validated {
		key, value, _ := strings.Cut(row, "=")
		if key == "preserved route applicationPath" || key == "preserved route teamID" {
			var decoded string
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return nil, err
			}
			value = bootstrapAuthority(decoded)
		} else if value == "true" || value == "false" {
			value = bootstrapOnOff(value == "true")
		}
		rows = append(rows, "  "+labels[key]+": "+value+" (kept)")
	}
	return rows, nil
}

func localChoice(specified, value bool) *bool {
	if !specified {
		return nil
	}
	return &value
}
func bootstrapLocalChoice(choice *bool) string {
	if choice == nil {
		return "keep this binding's choice"
	}
	return bootstrapOnOff(*choice)
}
