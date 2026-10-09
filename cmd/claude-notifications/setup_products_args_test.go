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
		{"observer-default-channels", []string{"confirm", "--products", "opencode"}, true},
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

// Regression: explicit Cursor selection is rejected, or its observer consent is
// mistaken for Claude/Codex routing and silently expands a group apply.
func TestSetupProductsCursorSelection(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		products string
		extra    []string
		valid    bool
	}{
		{"cursor", nil, true}, {"cursor", []string{"--agent-notify"}, true},
		{"cursor", []string{"--skip-agent-notify"}, true},
		{"cursor", []string{"--desktop"}, true},
		{"cursor", []string{"--webhook"}, true},
		{"cursor", []string{"--desktop", "--webhook"}, true},
		{"cursor", []string{"--navigation", "none"}, false},
		{"claude,cursor", nil, false}, {"claude,codex", nil, true},
	} {
		args := append([]string{"confirm", "--products", tc.products}, tc.extra...)
		if tc.products == "cursor" {
			args = append(args, "--scope-root", root, "--client-executable", filepath.Join(root, "TEST-agent"))
		}
		r, err := parseSetupProducts(args)
		if (err == nil) != tc.valid {
			t.Fatalf("%v: %+v %v", args, r, err)
		}
		if tc.valid && tc.products == "cursor" && r.Configure.Route != nil {
			t.Fatal("Cursor selection granted portable routing")
		}
	}
}

func TestCursorChannelQuestionRequiresOnlySingleSelection(t *testing.T) {
	for _, extra := range [][]string{nil, {"--desktop"}, {"--agent-notify"}, {"--scope-root", t.TempDir(), "--client-executable", filepath.Join(t.TempDir(), "TEST-agent")}} {
		args := append([]string{"channels", "--products", "cursor"}, extra...)
		_, err := parseSetupProducts(args)
		if (err == nil) != (extra == nil) {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if _, err := parseSetupProducts([]string{"confirm", "--products", "cursor", "--desktop"}); err == nil {
		t.Fatal("native choice bypassed explicit profile/executable")
	}
}
