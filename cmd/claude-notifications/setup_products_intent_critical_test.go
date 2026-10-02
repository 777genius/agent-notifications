//go:build linux || darwin

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Old code predicts disabled as soon as an observer is selected. Existing hooks
// let portable policy-only configuration run first and seed enabled=true, which
// old admission rejects. Exercise the normalized request and real policy writer.
func TestBootstrapMixedHooksOnlyAbsentPolicySeed(t *testing.T) {
	for _, tc := range []struct {
		observer string
		disabled bool
	}{{"opencode", false}, {"gemini", false}, {"opencode", true}} {
		observer := tc.observer
		name := observer + "/absent"
		if tc.disabled {
			name = observer + "/preserved-disabled"
		}
		t.Run(name, func(t *testing.T) {
			f, _, deps := configureFixture(t)
			path := filepath.Join(f.root, "TEST-PATH")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"claude", observer} {
				if err := os.WriteFile(filepath.Join(path, id), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", path)
			a, err := parseSetupProducts([]string{"confirm", "--products", "claude," + observer, "--desktop", "--agent-notify", "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false", "--control-root", f.control})
			if err != nil {
				t.Fatal(err)
			}
			e := productEnvironment{Home: f.root, PATH: path, DefaultControlRoot: f.control, Values: map[string]string{}, Config: config.SnapshotEnv()}
			ctx := setupCommandContext(t)
			if tc.disabled {
				off := false
				gen := f.generation(t)
				if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &off}); err != nil {
					t.Fatal(err)
				}
			}
			initial, err := installruntime.ReadPolicySnapshot(ctx, f.control)
			if err != nil || initial.Preimage.Exists != tc.disabled {
				t.Fatalf("wrong initial policy: %+v %v", initial, err)
			}
			provenance := selectorProvenance{Version: "TEST", SourceCommit: strings.Repeat("1", 40), SHA256: strings.Repeat("2", 64), Stage: []byte(f.root)}
			intent, _, err := buildConfirmedBootstrapIntent(ctx, a, e, provenance)
			if err != nil {
				t.Fatal(err)
			}
			result, err := configureNotifications(ctx, a.Configure, deps)
			if err != nil {
				t.Fatalf("portable configure: %+v %v", result, err)
			}
			seeded, err := installruntime.ReadPolicySnapshot(ctx, f.control)
			if err != nil || seeded.Policy.Enabled == tc.disabled {
				t.Fatalf("real first phase did not seed enabled: %+v %v", seeded, err)
			}
			var route map[string]json.RawMessage
			if err := json.Unmarshal(seeded.Fields["route"], &route); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"localRouting", "allowUnknownCaller", "allowCallerAsserted"} {
				if string(route[key]) != "false" {
					t.Fatalf("normalized false/false decision changed: %s=%s", key, route[key])
				}
			}
			current, _, err := setupwizard.ObserveBootstrapMCP(ctx, intentWizardRequest(intent))
			if err != nil {
				t.Fatal(err)
			}
			if err := setupwizard.CheckBootstrapMCP(intent.MCP, current); err != nil {
				t.Fatalf("legitimate hooks-only mixed seed rejected: %v", err)
			}
			if intent.MCP.SeedEnabled == tc.disabled {
				t.Fatal("confirmation showed wrong seed")
			}
		})
	}
}
