package main

import (
	"errors"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

// This private bootstrap flag cannot choose new clients, profiles or policy.
// The caller still supplies the complete original --agents scope, and admission
// plus every later Plan/Run retain the full confirmed MCP projection.
func stripBootstrapReadyClients(args []string) ([]string, []string, error) {
	var out, clients []string
	for n := 0; n < len(args); n++ {
		key, value, inline := strings.Cut(args[n], "=")
		if key != "--bootstrap-ready-clients" {
			out = append(out, args[n])
			continue
		}
		if clients != nil {
			return out, nil, errors.New("duplicate bootstrap ready clients")
		}
		if !inline {
			n++
			if n >= len(args) {
				return out, nil, errors.New("missing bootstrap ready clients")
			}
			value = args[n]
		}
		switch value {
		case "claude", "codex", "claude,codex":
			clients = strings.Split(value, ",")
		default:
			return out, nil, errors.New("invalid bootstrap ready clients")
		}
	}
	return out, clients, nil
}

func admitBootstrapReadyClients(req *setupwizard.Request, ready []string) error {
	if len(ready) == 0 {
		return nil
	}
	if req.BootstrapMCP == nil {
		return errors.New("confirmed bootstrap required")
	}
	for _, id := range req.Agents {
		if id != "claude" && id != "codex" {
			return errors.New("bootstrap ready clients requires legacy client scope")
		}
	}
	for _, id := range ready {
		if !containsProduct(req.Agents, id) {
			return errors.New("ready client was not confirmed")
		}
	}
	req.BootstrapReadyClients = append([]string(nil), ready...)
	return nil
}

func readyBootstrapAgents(selected, ready []string) []string {
	var result []string
	for _, id := range selected {
		if len(ready) == 0 || containsProduct(ready, id) {
			result = append(result, id)
		}
	}
	return result
}
