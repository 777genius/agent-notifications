package main

import "github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"

func escapeProductNotice(raw string) ([]string, error) {
	return setupwizard.EscapeConfirmationRows([]string{raw})
}
