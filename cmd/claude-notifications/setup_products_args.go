package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var productOrder = []string{"claude", "codex", "opencode", "gemini"}
var productLabels = map[string]string{"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode", "gemini": "Gemini CLI"}
var scopeKeys = []string{"claude-config", "codex-home", "opencode-config-dir", "gemini-home", "gemini-config-root", "claude-executable", "codex-executable", "opencode-executable", "gemini-executable", "control-root"}

type setupProductsArgs struct {
	Operation, Mode, IntentFile                    string
	Products                                       []string
	Scopes                                         map[string]string
	AgentNotify, SkipAgentNotify, Desktop, Webhook bool
	Configure                                      notificationConfigureRequest
	ConfigureArgs                                  []string
}

func parseSetupProducts(args []string) (r setupProductsArgs, err error) {
	r.Scopes = map[string]string{}
	r.Mode = "auto"
	invalid := func() (setupProductsArgs, error) { return r, errors.New("invalid setup-products arguments") }
	if len(args) == 0 || len(args) > 80 {
		return invalid()
	}
	r.Operation = args[0]
	switch r.Operation {
	case "capabilities", "features":
		if len(args) != 1 {
			return invalid()
		}
		return r, nil
	case "select", "channels", "confirm", "intent-args", "preflight":
	default:
		return invalid()
	}
	seen := map[string]bool{}
	modeSeen := false
	routeArgs := []string{}
	routePresent := false
	for i := 1; i < len(args); i++ {
		raw := args[i]
		if len(raw) > 4096 || strings.IndexByte(raw, 0) >= 0 {
			return invalid()
		}
		key, value, inline := strings.Cut(strings.TrimPrefix(raw, "--"), "=")
		if !strings.HasPrefix(raw, "--") {
			return invalid()
		}
		boolean := key == "plain" || key == "desktop" || key == "webhook" || key == "agent-notify" || key == "skip-agent-notify" || key == "preserve-policy" || key == "request-permission"
		if boolean {
			if inline {
				return invalid()
			}
		} else if !inline {
			i++
			if i >= len(args) {
				return invalid()
			}
			value = args[i]
		}
		if key == "ui" || key == "plain" {
			if key == "plain" {
				value = "plain"
			}
			if value != "auto" && value != "plain" && value != "rich" {
				return invalid()
			}
			if modeSeen && r.Mode != value {
				return invalid()
			}
			r.Mode = value
			modeSeen = true
			continue
		}
		if seen[key] {
			return invalid()
		}
		seen[key] = true
		switch key {
		case "products":
			r.Products, err = parseProductCSV(value)
			if err != nil {
				return r, err
			}
		case "intent-file":
			if !validProductPath(value) {
				return invalid()
			}
			r.IntentFile = value
		case "agent-notify":
			r.AgentNotify = true
		case "skip-agent-notify":
			r.SkipAgentNotify = true
		case "desktop":
			r.Desktop = true
		case "webhook":
			r.Webhook = true
		case "navigation", "app", "team-id", "allow-unknown-caller", "allow-caller-asserted", "preserve-policy", "request-permission":
			routePresent = true
			routeArgs = append(routeArgs, "--"+key)
			if !boolean {
				routeArgs = append(routeArgs, value)
			}
		default:
			if !containsProduct(scopeKeys, key) || !validProductPath(value) {
				return invalid()
			}
			r.Scopes[key] = value
		}
	}
	if r.AgentNotify && r.SkipAgentNotify {
		return invalid()
	}
	if r.Operation == "select" {
		if len(r.Products) > 0 || r.IntentFile != "" || r.AgentNotify || r.SkipAgentNotify || r.Desktop || r.Webhook || routePresent {
			return invalid()
		}
		return r, nil
	}
	if r.Operation == "intent-args" {
		if r.IntentFile == "" || len(seen) != 1 || modeSeen {
			return invalid()
		}
		return r, nil
	}
	if len(r.Products) == 0 {
		return invalid()
	}
	portable, observer := false, false
	for _, id := range r.Products {
		if id == "claude" || id == "codex" {
			portable = true
		} else {
			observer = true
		}
	}
	if r.Operation == "channels" {
		if !observer || len(seen) != 1 || r.IntentFile != "" {
			return invalid()
		}
		return r, nil
	}
	if r.Operation == "preflight" {
		if r.IntentFile == "" || len(r.Scopes) > 0 || modeSeen {
			return invalid()
		}
	}
	if (!portable && (routePresent || r.AgentNotify || r.SkipAgentNotify)) || (!observer && (r.Desktop || r.Webhook)) || (observer && !r.Desktop && !r.Webhook) || (r.SkipAgentNotify && routePresent) {
		return invalid()
	}
	if portable && !r.SkipAgentNotify {
		provider := "both"
		if !containsProduct(r.Products, "claude") {
			provider = "codex"
		} else if !containsProduct(r.Products, "codex") {
			provider = "claude"
		}
		if !seen["navigation"] && !seen["app"] && !seen["team-id"] && !seen["allow-unknown-caller"] && !seen["allow-caller-asserted"] {
			routeArgs = append([]string{"--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"}, routeArgs...)
			if !seen["preserve-policy"] {
				routeArgs = append(routeArgs, "--preserve-policy")
			}
		} else if !seen["allow-unknown-caller"] || !seen["allow-caller-asserted"] {
			return invalid()
		}
		r.ConfigureArgs = append([]string{"--provider", provider}, routeArgs...)
		r.Configure, _, err = parseNotificationConfigure(r.ConfigureArgs)
		if err != nil {
			return r, err
		}
		// The portable path preserves enablement and configures policy only. These
		// derived internal flags are recorded; they are not public helper options.
		r.Configure.PolicyOnly = true
		r.Configure.PreserveEnabled = true
	}
	return r, nil
}

func validProductPath(p string) bool {
	return len(p) > 0 && len(p) <= 4096 && strings.IndexByte(p, 0) < 0 && filepath.IsAbs(p) && filepath.Clean(p) == p && p != string(filepath.Separator)
}
func containsProduct(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func parseProductCSV(raw string) ([]string, error) {
	if len(raw) == 0 || len(raw) > 64 {
		return nil, errors.New("invalid products")
	}
	seen := map[string]bool{}
	for _, id := range strings.Split(raw, ",") {
		if !containsProduct(productOrder, id) || seen[id] {
			return nil, fmt.Errorf("invalid product %q", id)
		}
		seen[id] = true
	}
	out := []string{}
	for _, id := range productOrder {
		if seen[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
