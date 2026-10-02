package main

import (
	"path/filepath"
	"testing"
)

// Red regression: supported bootstrap caller bools become renderer defaults,
// unknown/repeated flags reach discovery, or observer consent grants MCP rights.
func TestSetupProductsNormalizedArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		valid bool
	}{
		{"ui-repeat", []string{"select", "--plain", "--ui=plain"}, true},
		{"ui-conflict", []string{"select", "--plain", "--ui=rich"}, false},
		{"unknown-mode", []string{"select", "--ui=fast"}, false},
		{"missing-ui", []string{"select", "--ui"}, false},
		{"explicit-false", []string{"confirm", "--products", "claude", "--agent-notify", "--navigation", "none", "--allow-unknown-caller=false", "--allow-caller-asserted=false"}, true},
		{"auto-route", []string{"confirm", "--products", "codex"}, true},
		{"partial-route", []string{"confirm", "--products", "claude", "--navigation", "none"}, false},
		{"duplicate-route", []string{"confirm", "--products", "claude", "--preserve-policy", "--preserve-policy"}, false},
		{"skip-route", []string{"confirm", "--products", "claude", "--skip-agent-notify", "--request-permission"}, false},
		{"observers-mcp", []string{"confirm", "--products", "gemini", "--webhook", "--agent-notify"}, false},
		{"portable-channels", []string{"confirm", "--products", "codex", "--desktop"}, false},
		{"observer-channels-required", []string{"confirm", "--products", "opencode"}, false},
		{"channels", []string{"channels", "--products", "claude,gemini", "--plain"}, true},
		{"channels-pure-portable", []string{"channels", "--products", "claude"}, false},
		{"json", []string{"select", "--json"}, false},
		{"ambiguous-executable", []string{"select", "--client-executable", filepath.Join(t.TempDir(), "agent")}, false},
		{"relative-authority", []string{"select", "--codex-home", "relative"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseSetupProducts(tc.args)
			if (err == nil) != tc.valid {
				t.Fatalf("request=%+v err=%v", r, err)
			}
			if tc.name == "explicit-false" && (r.Configure.Route.AllowUnknownCaller || r.Configure.Route.AllowCallerAsserted) {
				t.Fatal("false caller flags became true")
			}
			if tc.name == "auto-route" && (!r.Configure.PreservePolicy || !r.Configure.PreserveEnabled || !r.Configure.PolicyOnly || !r.Configure.Route.AllowUnknownCaller) {
				t.Fatalf("auto route/preservation lost: %+v", r.Configure)
			}
		})
	}
}
