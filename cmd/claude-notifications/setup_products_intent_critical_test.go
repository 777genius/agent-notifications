//go:build linux || darwin

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
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

// Regression: actual confirmation freezes Cursor but Plan/Run observes only
// Claude/Codex, rejecting even an unchanged preserved opt-out as a conflict.
func TestCursorConfirmationReachesWizardAdmissionWithFrozenOptOut(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Log("NOT_RUN Cursor product tuple")
		return
	}
	f, _, _ := configureFixture(t)
	ctx := setupCommandContext(t)
	agent := filepath.Join(f.root, "TEST-agent")
	if err := os.WriteFile(agent, []byte("TEST-not-executed"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(f.root, ".cursor")
	// A retained installation with no live Cursor preserves its absent unit;
	// fresh absence would select Cursor automatically, not represent an opt-out.
	store := statev2.Store{Path: filepath.Join(filepath.Dir(f.control), "uap", "state", "state-v2.json")}
	if err := store.Save(domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion, Installations: []domain.Installation{{InstallationID: "TEST-kept-installation", DeclaredName: "agent-notify", Source: domain.SourceBinding{SourceBindingID: "TEST-kept-source"}, Package: domain.PackageBinding{DeclaredName: "agent-notify"}, Clients: map[string]domain.ClientBinding{}}}}); err != nil {
		t.Fatal(err)
	}
	a, err := parseSetupProducts([]string{"confirm", "--products", "cursor", "--scope-root", profile, "--client-executable", agent, "--control-root", f.control})
	if err != nil {
		t.Fatal(err)
	}
	old := selectorSourceCommit
	selectorSourceCommit = "51ceb8f1ef4af56823bb12f21818a74e7d5abf47"
	t.Cleanup(func() { selectorSourceCommit = old })
	provenance, err := currentSelectorProvenance()
	if err != nil {
		t.Fatal(err)
	}
	provenance.Stage = []byte(f.root)
	i, _, err := buildConfirmedBootstrapIntent(ctx, a, productEnvironment{Home: f.root, PATH: "", DefaultControlRoot: f.control, Values: map[string]string{}, Config: config.SnapshotEnv()}, provenance)
	if err != nil {
		t.Fatal(err)
	}
	if len(i.MCP.Selected) != 0 || !containsProduct(i.MCP.Skipped, "cursor") || string(i.MCP.Projection.Profiles["cursor"]) != profile {
		t.Fatalf("confirmation lost opt-out/profile: %+v", i.MCP)
	}
	path := filepath.Join(f.root, "TEST-confirmed-intent.json")
	if err := writeBootstrapIntent(path, i); err != nil {
		t.Fatal(err)
	}
	r, err := admitBootstrapWizardRequest(path, intentWizardRequest(i))
	if err != nil {
		t.Fatal(err)
	}
	r, err = composeCursorWizard(ctx, r)
	if err != nil || r.CursorAgentNotify == nil || *r.CursorAgentNotify || r.CursorAuthority != nil {
		t.Fatalf("caller broadened opt-out: %+v %v", r, err)
	}
	before, err := os.ReadFile(filepath.Join(f.control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := setupwizard.Plan(ctx, r)
	if err != nil || plan.Result.Reason != "empty_units" || plan.Request.BootstrapExpectedPolicy == nil {
		t.Fatalf("unchanged Plan admission: %+v %v", plan.Result, err)
	}
	result, err := setupwizard.Run(ctx, r)
	if err != nil || result.Reason != "empty_units" {
		t.Fatalf("unchanged Run admission: %+v %v", result, err)
	}
	bad := r
	bad.CursorConfig, bad.ScopeRoot = filepath.Join(f.root, "TEST-substitution"), filepath.Join(f.root, "TEST-substitution")
	if _, err := admitBootstrapWizardRequest(path, bad); err == nil {
		t.Fatal("caller admitted profile substitution")
	}
	for _, bad := range []setupwizard.Request{bad, func() setupwizard.Request { b := r; g := uint64(0); b.BootstrapExpectedGeneration = &g; return b }()} {
		plan, err := setupwizard.Plan(ctx, bad)
		if err == nil || plan.Result.Reason != "concurrent_change" {
			t.Fatalf("changed Plan admitted: %+v %v", plan.Result, err)
		}
		result, err := setupwizard.Run(ctx, bad)
		if err == nil || result.Reason != "concurrent_change" {
			t.Fatalf("changed Run admitted: %+v %v", result, err)
		}
	}
	after, err := os.ReadFile(filepath.Join(f.control, "ownership.json"))
	if err != nil || string(before) != string(after) {
		t.Fatalf("preflight changed ownership: %v", err)
	}
}
