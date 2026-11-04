//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Exercise returned public Plan/Run retry argv through the real CLI parser.
// Supplied names are selection authority; a retry must not substitute defaults.
func TestLocalReturnedRetryPreservesExactNames(t *testing.T) {
	f, _, _ := configureFixture(t)
	ledger, _, err := installruntime.ReadOwnership(f.control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime,
		Owner: ledger.Owner, ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &ledger.Generation,
		Files: []installruntime.File{{Path: filepath.Join(f.runtime, filepath.FromSlash(portable.PlatformPrimary())),
			Data: []byte(setupCommandRead(t, f.command)), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(f.root, "TEST-explicit-local")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(profile, "settings.json")
	setupCommandWrite(t, settings, `{"TEST_foreign":true}`, 0600)
	for _, server := range []string{"agent-notifications", "agent-notify"} {
		t.Run(server, func(t *testing.T) {
			req, _, err := parseSetupWizard([]string{"--action", "install", "--agents", "copilot-vscode", "--scope-root", profile,
				"--client-executable", f.command, "--local-settings", settings, "--local-mcp", "true", "--local-skills", "true",
				"--control-root", f.control, "--package", filepath.Join(f.root, "TEST-missing-package"), "--yes"})
			if err != nil {
				t.Fatal(err)
			}
			req.LocalConfig.MCPServers = []string{server}
			req.LocalConfig.Skills = []string{"chosen skill with spaces", "extra-skill"}
			for _, route := range []string{"Plan", "Run"} {
				t.Run(route, func(t *testing.T) {
					var result setupwizard.Result
					if route == "Plan" {
						plan, _ := setupwizard.Plan(setupCommandContext(t), req)
						result = plan.Result
					} else {
						result, _ = setupwizard.Run(setupCommandContext(t), req)
					}
					if len(result.Command) < 3 || result.Command[0] != "setup-notifications" || result.Command[1] != "wizard" {
						t.Fatalf("public result lacks retry: %+v", result)
					}
					replay, _, err := parseSetupWizard(result.Command[2:])
					if err != nil || replay.LocalConfig == nil {
						t.Fatalf("returned retry is not parseable: %q %v", result.Command, err)
					}
					if !reflect.DeepEqual(req.LocalConfig.MCPServers, replay.LocalConfig.MCPServers) ||
						!reflect.DeepEqual(req.LocalConfig.Skills, replay.LocalConfig.Skills) || replay.LocalConfig.ProfileSettingsPath != settings {
						t.Fatalf("returned retry changed exact selection: before=%+v after=%+v argv=%q", req.LocalConfig, replay.LocalConfig, result.Command)
					}
				})
			}
		})
	}
}

func TestLocalExactNameFlagsKeepLegacyBooleans(t *testing.T) {
	root := setupCommandRoot(t)
	settings, executable := filepath.Join(root, "settings.json"), filepath.Join(root, "TEST-executable")
	base := []string{"--agents", "copilot-vscode", "--scope-root", root, "--client-executable", executable, "--local-settings", settings}
	for _, enabled := range []string{"true", "false"} {
		req, _, err := parseSetupWizard(append(append([]string{}, base...), "--local-mcp", enabled, "--local-skills", enabled))
		if err != nil {
			t.Fatal(err)
		}
		if enabled == "true" {
			if !reflect.DeepEqual(req.LocalConfig.MCPServers, []string{"agent-notify"}) || !reflect.DeepEqual(req.LocalConfig.Skills, []string{"agent-notifications"}) {
				t.Fatalf("legacy boolean defaults changed: %+v", req.LocalConfig)
			}
		} else if len(req.LocalConfig.MCPServers) != 0 || len(req.LocalConfig.Skills) != 0 {
			t.Fatalf("false selected components: %+v", req.LocalConfig)
		}
	}
	for _, flags := range [][]string{
		{"--local-mcp-names", `["agent-notifications"]`},
		{"--local-mcp", "false", "--local-mcp-names", `["agent-notifications"]`},
		{"--local-mcp", "true", "--local-mcp-names", `null`},
		{"--local-mcp", "true", "--local-mcp-names", `[]`},
		{"--local-mcp", "true", "--local-mcp-names", `["x","x"]`},
		{"--local-mcp", "true", "--local-mcp-names", `[""]`},
		{"--local-skills", "true", "--local-skill-names", `{"name":"skill"}`},
		{"--local-skills", "true", "--local-skill-names", `[1]`},
	} {
		if _, _, err := parseSetupWizard(append(append([]string{}, base...), flags...)); err == nil {
			t.Fatalf("invalid exact selection accepted: %q", flags)
		}
	}
	if _, _, err := parseSetupWizard([]string{"--agents", "codex", "--local-mcp", "true", "--local-mcp-names", `["agent-notifications"]`}); err == nil {
		t.Fatal("exact Local names accepted for another client")
	}
}
