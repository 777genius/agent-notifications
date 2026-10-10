//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
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
	a, err := parseSetupProducts([]string{"confirm", "--products", "cursor", "--desktop", "--scope-root", profile, "--client-executable", agent, "--control-root", f.control})
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
	if !i.Request.Desktop || i.Request.Webhook || !i.Units[0].Desktop || i.Units[0].Webhook || !i.Units[0].PreservedOff {
		t.Fatal("kept-off desktop-only confirmation lost frozen atoms")
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
	if err := fenceCursorIntent(ctx, intentWizardRequest(i), i); err != nil {
		t.Fatalf("inert Cursor fence: %v", err)
	}
	admitted, retained, err := admitBootstrapWizardIntent(path, intentWizardRequest(i))
	if err != nil {
		t.Fatal(err)
	}
	*admitted.BootstrapExpectedGeneration++
	if retained.Initial.Generation != i.Initial.Generation {
		t.Fatal("caller mutated immutable confirmation generation")
	}
	commandArgs := []string{"--action", "install", "--install-or-update", "--agents", "cursor", "--hooks", "false", "--agent-notify", "true", "--scope-root", profile, "--client-executable", agent, "--control-root", f.control, "--runtime-root", string(i.Scopes["runtime-root"]), "--global-config", string(i.Scopes["global-config"]), "--bootstrap-intent-file", path, "--yes", "--json"}
	var keptOutput bytes.Buffer
	keptCode := executeSetupWizardWith(ctx, commandArgs, &keptOutput, io.Discard, strings.NewReader(""), false)
	var kept setupwizard.Result
	if err := json.Unmarshal(keptOutput.Bytes(), &kept); err != nil || keptCode != 0 || kept.Reason != "existing_opt_out" || len(kept.Targets) != 1 || kept.Targets[0].Reason != "preserved_existing_opt_out" {
		t.Fatalf("kept-off command: %d %s %v", keptCode, keptOutput.String(), err)
	}
	if setupCommandRead(t, filepath.Join(f.control, "ownership.json")) != string(before) {
		t.Fatal("kept-off command mutated ownership")
	}
	// Drive real command admission after a policy-only opt-out with unchanged
	// projection. It must stop before physical composition/package mutation.
	initialPolicy, err := installruntime.ReadPolicySnapshot(ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, Owner: "existing-installer", RuntimeRoot: f.runtime, ConsumerID: "hooks", PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &initialPolicy.Installation.Ledger.Generation, ExpectedPolicy: &initialPolicy.Preimage, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"cursorNotifications":{"desktop":false,"webhook":false}}`)}}); err != nil {
		t.Fatal(err)
	}
	ownershipBefore := setupCommandRead(t, filepath.Join(f.control, "ownership.json"))
	var output bytes.Buffer
	code := executeSetupWizardWith(ctx, commandArgs, &output, io.Discard, strings.NewReader(""), false)
	var refused setupwizard.Result
	if err := json.Unmarshal(output.Bytes(), &refused); err != nil || code == 0 || refused.Reason != "concurrent_change" {
		t.Fatalf("frozen opt-out: %d %s %v", code, output.String(), err)
	}
	if setupCommandRead(t, filepath.Join(f.control, "ownership.json")) != ownershipBefore {
		t.Fatal("drifted command mutated ownership")
	}
}

// These are real kernel/command boundaries in disposable roots, not public
// native acknowledgment fixtures. They deliberately cannot grant delivery.
func cursorConsentCommandFixture(t *testing.T) (setupCommandFixture, portable.Binding) {
	t.Helper()
	f, _, _ := configureFixture(t)
	ledger, _, err := installruntime.ReadOwnership(f.control)
	if err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{Version: 1, Integration: portable.Cursor, InstallationID: "TEST-cursor-install", BindingID: "TEST-cursor-binding", ScopeID: "user", ComponentID: ledger.ID, Owner: ledger.Owner, ScopeRoot: filepath.Join(f.root, ".cursor"), DataRoot: filepath.Join(f.root, "TEST-data"), ControlRoot: f.control, RuntimeRoot: f.runtime, GlobalConfig: f.global, Primary: "bin/claude-notifications"}
	for _, root := range []string{b.ScopeRoot, b.DataRoot} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (portablesetup.Service{}).CommitBinding(setupCommandContext(t), portablesetup.Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	return f, b
}

func setCursorTestConsent(t *testing.T, b portable.Binding, desktop, webhook bool) installruntime.PolicySnapshot {
	t.Helper()
	ctx := setupCommandContext(t)
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := copilotvscodeinstall.CursorPolicyPatch(b, copilotvscodeinstall.CursorChoices{Desktop: &desktop, Webhook: &webhook}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, consumer, _, _ := b.Registration()
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: patch}); err != nil {
		t.Fatal(err)
	}
	s, err = installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertCursorNativePair(t *testing.T, root string, desktop, webhook bool) installruntime.PolicySnapshot {
	t.Helper()
	s, err := installruntime.ReadPolicySnapshot(setupCommandContext(t), root)
	if err != nil {
		t.Fatal(err)
	}
	var route struct {
		Cursor struct{ Desktop, Webhook bool } `json:"cursorNotifications"`
	}
	if err := json.Unmarshal(s.Fields["route"], &route); err != nil || route.Cursor.Desktop != desktop || route.Cursor.Webhook != webhook {
		t.Fatalf("actual route=%s err=%v", s.Fields["route"], err)
	}
	return s
}

func TestCursorRegistrationInitializesFalseAndExactBindingPreserves(t *testing.T) {
	f, b := cursorConsentCommandFixture(t)
	ctx := setupCommandContext(t)
	assertCursorNativePair(t, f.control, false, false)
	s := setCursorTestConsent(t, b, true, false)
	before := setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json"))
	if _, err := (portablesetup.Service{ExpectedPolicy: &s.Preimage}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: s.Installation.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	if after := setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json")); after != before {
		t.Fatal("exact registration rewrote consent")
	}
	// A changed registered global config creates a different consumer, even
	// when the old consumer was removed. Global true leaves are not inherited.
	key, _, _, _ := b.Registration()
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage}); err != nil {
		t.Fatal(err)
	}
	b.GlobalConfig = filepath.Join(f.root, "TEST-rebound-global.json")
	s, err := installruntime.ReadPolicySnapshot(ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (portablesetup.Service{ExpectedPolicy: &s.Preimage}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: s.Installation.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	after := assertCursorNativePair(t, f.control, false, false)
	if s.Preimage != after.Preimage {
		t.Fatal("successful false registration did not advance request-local CAS")
	}
	if _, err := (portablesetup.Service{ExpectedPolicy: &s.Preimage}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: after.Installation.Ledger.Generation}); err != nil {
		t.Fatalf("next coordinated registration used stale CAS: %v", err)
	}
	if !portable.ExactCommittedBinding(after.Installation.Ledger, b) {
		t.Fatal("false pair was not committed with exact registration")
	}
	if !reflect.DeepEqual(s.Installation.Ledger.Consumers["hooks"], after.Installation.Ledger.Consumers["hooks"]) || s.Policy.Enabled != after.Policy.Enabled {
		t.Fatal("registration altered sibling or shared intent")
	}
	if ok, err := portable.ExactLocator(b); err != nil || !ok {
		t.Fatalf("registration locator: %t %v", ok, err)
	}
}

func TestCursorWizardEarlyRevokesBeforeDamagedPhysicalCleanup(t *testing.T) {
	for _, action := range []string{"disable", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			f, b := cursorConsentCommandFixture(t)
			s := setCursorTestConsent(t, b, true, true)
			global := setupCommandRead(t, f.global)
			if err := os.Rename(b.ScopeRoot, b.ScopeRoot+"-replaced"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(b.ScopeRoot, []byte("TEST-replaced-profile"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.command, []byte("TEST-damaged-helper"), 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"--action", "install", "--agents", "cursor", "--hooks", "false", "--agent-notify", "false", "--scope-root", b.ScopeRoot, "--installation-id", b.InstallationID, "--client-executable", f.command, "--control-root", f.control, "--yes", "--json"}
			if action == "uninstall" {
				args[1], args[7] = "uninstall", "true"
			}
			var out bytes.Buffer
			code := executeSetupWizardWith(setupCommandContext(t), args, &out, io.Discard, strings.NewReader(""), false)
			var result setupwizard.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("%s: %v", out.String(), err)
			}
			if action == "disable" && (code != 0 || result.Generation != s.Installation.Ledger.Generation+1) {
				t.Fatalf("disable: %d %+v", code, result)
			}
			if action == "uninstall" && code == 0 {
				t.Fatal("replaced profile cleanup falsely succeeded")
			}
			// Full installed reads rightly fail for the damaged helper; revocation
			// evidence uses the actual raw policy and structural owner instead.
			var raw struct {
				Route struct {
					Cursor struct{ Desktop, Webhook bool } `json:"cursorNotifications"`
				}
			}
			if err := json.Unmarshal([]byte(setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json"))), &raw); err != nil || raw.Route.Cursor.Desktop || raw.Route.Cursor.Webhook {
				t.Fatalf("pair remains on: %+v %v", raw, err)
			}
			ledger, recovery, err := installruntime.ReadOwnership(f.control)
			if err != nil || recovery || ledger.Generation != s.Installation.Ledger.Generation+1 || !reflect.DeepEqual(ledger.Consumers, s.Installation.Ledger.Consumers) || !reflect.DeepEqual(ledger.Files, s.Installation.Ledger.Files) || ledger.PendingMutation != nil {
				t.Fatalf("revocation removed units or allocated binding: %+v %v", ledger, err)
			}
			if setupCommandRead(t, f.global) != global {
				t.Fatal("global opt-out changed")
			}
		})
	}
}

func TestCursorWizardAmbiguousDisableFailsWithoutMutation(t *testing.T) {
	f, b := cursorConsentCommandFixture(t)
	setCursorTestConsent(t, b, true, true)
	s, err := installruntime.ReadPolicySnapshot(setupCommandContext(t), f.control)
	if err != nil {
		t.Fatal(err)
	}
	other := b
	other.BindingID = "TEST-other-binding"
	key, consumer, _, _ := other.Registration()
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, ExpectedGeneration: &s.Installation.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	before := setupCommandRead(t, filepath.Join(f.control, "ownership.json"))
	var out bytes.Buffer
	code := executeSetupWizardWith(setupCommandContext(t), []string{"--action", "install", "--agents", "cursor", "--hooks", "false", "--agent-notify", "false", "--scope-root", b.ScopeRoot, "--client-executable", f.command, "--control-root", f.control, "--yes", "--json"}, &out, io.Discard, strings.NewReader(""), false)
	if code == 0 || setupCommandRead(t, filepath.Join(f.control, "ownership.json")) != before {
		t.Fatalf("ambiguous disable mutated: %d %s", code, out.String())
	}
	assertCursorNativePair(t, f.control, true, true)
}

func TestCursorConfirmedConsentRefusesPreparedOnlyAndPolicyDrift(t *testing.T) {
	f, b := cursorConsentCommandFixture(t)
	ctx := setupCommandContext(t)
	s := assertCursorNativePair(t, f.control, false, false)
	i := confirmedBootstrapIntent{Request: setupProductsArgs{Desktop: true}, Initial: bootstrapInitialObservation{LedgerID: s.Installation.Ledger.ID, Owner: s.Installation.Ledger.Owner, Generation: s.Installation.Ledger.Generation, Policy: s.Preimage}}
	r := setupwizard.Request{ControlRoot: f.control, ScopeRoot: b.ScopeRoot, InstallationID: b.InstallationID}
	result := setupwizard.Result{Action: "install", Outcome: "completed", InstallationID: b.InstallationID, Generation: s.Installation.Ledger.Generation, Targets: []setupwizard.TargetResult{{Client: "cursor", Unit: "agent-notify", Outcome: "completed", Reason: b.BindingID}}}
	before := setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json"))
	if _, err := commitConfirmedCursorConsent(ctx, r, result, i); err == nil {
		t.Fatal("registration without public installed acknowledgment enabled native consent")
	}
	if setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json")) != before {
		t.Fatal("prepared-only changed policy")
	}
	// An intervening opt-out is not a new baseline, including unchanged ledger
	// generation and an otherwise unchanged recorded consumer.
	setupCommandWrite(t, filepath.Join(f.control, "agent-notifications.json"), before+"\n", 0600)
	if _, err := commitConfirmedCursorConsent(ctx, r, result, i); err == nil {
		t.Fatal("policy byte drift enabled consent")
	}
	assertCursorNativePair(t, f.control, false, false)
	t.Log("NOT_RUN CursorConfirmedDesktopOnlyPublicInstalledAck: immutable UAP143c materializer denial; needs qualified public composed pin")
	t.Log("NOT_RUN CursorConfirmedNativeStopDelivery: needs genuine installed original-token acknowledgment and retained channel/CAS/deadline proof")
}

// A conflicting real locator fails after the false registration Commit. Cleanup
// must use its known output CAS, preserving false and foreign locator bytes.
func TestCursorFalseRegistrationLocatorFailureUsesKnownCAS(t *testing.T) {
	f, b := cursorConsentCommandFixture(t)
	s := setCursorTestConsent(t, b, true, true)
	b.GlobalConfig = filepath.Join(f.root, "TEST-new-global.json")
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.DataRoot, name)
	foreign := []byte("TEST-foreign-locator")
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	expected := s.Preimage
	if _, err := (portablesetup.Service{ExpectedPolicy: &expected}).CommitBinding(setupCommandContext(t), portablesetup.Request{Binding: b, ExpectedGeneration: s.Installation.Ledger.Generation}); err == nil {
		t.Fatal("conflicting locator published")
	}
	after := assertCursorNativePair(t, f.control, false, false)
	if _, exists := after.Installation.Ledger.Consumers[key]; exists {
		t.Fatal("stale policy CAS prevented registration rollback")
	}
	if expected != after.Preimage || after.Installation.Ledger.Generation != s.Installation.Ledger.Generation+2 || !reflect.DeepEqual(after.Installation.Ledger.Consumers, s.Installation.Ledger.Consumers) {
		t.Fatal("cleanup lost known CAS or sibling registration")
	}
	if setupCommandRead(t, path) != string(foreign) {
		t.Fatal("foreign locator overwritten")
	}
}

func localConsentCommandFixture(t *testing.T) (setupCommandFixture, portable.Binding) {
	t.Helper()
	f, _, _ := configureFixture(t)
	ledger, _, err := installruntime.ReadOwnership(f.control)
	if err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{Version: 1, Integration: portable.CopilotVSCode, InstallationID: "TEST-local-install", BindingID: "TEST-local-binding", ScopeID: "user", ComponentID: ledger.ID, Owner: ledger.Owner, ScopeRoot: filepath.Join(f.root, "TEST-local-profile"), DataRoot: filepath.Join(f.root, "TEST-local-data"), ControlRoot: f.control, RuntimeRoot: f.runtime, GlobalConfig: f.global, Primary: "bin/claude-notifications"}
	for _, root := range []string{b.ScopeRoot, b.DataRoot} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (portablesetup.Service{}).CommitBinding(setupCommandContext(t), portablesetup.Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	return f, b
}

func setLocalTestConsent(t *testing.T, b portable.Binding, desktop, webhook, manual bool) installruntime.PolicySnapshot {
	t.Helper()
	ctx := setupCommandContext(t)
	s, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := copilotvscodeinstall.PolicyPatch(b, copilotvscodeinstall.Choices{Desktop: &desktop, Webhook: &webhook, Manual: &manual}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, consumer, _, _ := b.Registration()
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: fields}); err != nil {
		t.Fatal(err)
	}
	return assertLocalConsent(t, b.ControlRoot, desktop, webhook, manual)
}

func assertLocalConsent(t *testing.T, root string, desktop, webhook, manual bool) installruntime.PolicySnapshot {
	t.Helper()
	s, err := installruntime.ReadPolicySnapshot(setupCommandContext(t), root)
	if err != nil {
		t.Fatal(err)
	}
	var route struct {
		Local struct {
			Desktop, Webhook *bool
			Manual           struct{ Enabled *bool }
		} `json:"copilotVSCodeNotifications"`
	}
	if err := json.Unmarshal(s.Fields["route"], &route); err != nil || route.Local.Desktop == nil || route.Local.Webhook == nil || route.Local.Manual.Enabled == nil || *route.Local.Desktop != desktop || *route.Local.Webhook != webhook || *route.Local.Manual.Enabled != manual {
		t.Fatalf("three actual Local leaves: %s %v", s.Fields["route"], err)
	}
	return s
}

func TestLocalRegistrationFalseExactBindingAndLocatorCAS(t *testing.T) {
	f, b := localConsentCommandFixture(t)
	ctx := setupCommandContext(t)
	assertLocalConsent(t, f.control, false, false, false)
	s := setLocalTestConsent(t, b, true, false, true)
	before := setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json"))
	if _, err := (portablesetup.Service{ExpectedPolicy: &s.Preimage}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: s.Installation.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	if after := setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json")); after != before {
		t.Fatal("exact binding changed explicit consent")
	}
	// A rebind may not borrow any affirmative leaf, even when its locator fails.
	b.GlobalConfig = filepath.Join(f.root, "TEST-new-local-global.json")
	filename, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	locator := filepath.Join(b.DataRoot, filename)
	if err := os.WriteFile(locator, []byte("TEST-foreign-locator"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := s.Preimage
	if _, err := (portablesetup.Service{ExpectedPolicy: &expected}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: s.Installation.Ledger.Generation}); err == nil {
		t.Fatal("foreign locator overwritten")
	}
	after := assertLocalConsent(t, f.control, false, false, false)
	key, _, _, _ := b.Registration()
	if _, ok := after.Installation.Ledger.Consumers[key]; ok {
		t.Fatal("failed locator left rebound registration")
	}
	if expected != after.Preimage || after.Installation.Ledger.Generation != s.Installation.Ledger.Generation+2 || !reflect.DeepEqual(after.Installation.Ledger.Consumers, s.Installation.Ledger.Consumers) || setupCommandRead(t, locator) != "TEST-foreign-locator" {
		t.Fatal("known CAS rollback lost foreign state")
	}
}

func TestLocalFrozenRevocationIndependentAndPolicyDrift(t *testing.T) {
	for _, selection := range []copilotvscodeinstall.RevokeSelection{copilotvscodeinstall.RevokeNative, copilotvscodeinstall.RevokeManual, copilotvscodeinstall.RevokeAll} {
		t.Run(fmt.Sprint(selection), func(t *testing.T) {
			f, b := localConsentCommandFixture(t)
			s := setLocalTestConsent(t, b, true, true, true)
			policyPath := filepath.Join(f.control, "agent-notifications.json")
			raw := setupCommandRead(t, policyPath)
			// Same-generation byte drift is rejected rather than adopted as a new decision.
			setupCommandWrite(t, policyPath, raw+"\n", 0600)
			if _, _, err := revokeRecordedLocal(setupCommandContext(t), b, selection, &s.Installation.Ledger.Generation, &s.Preimage); !errors.Is(err, portablesetup.ErrConcurrentChange) {
				t.Fatal("frozen revoke adopted byte drift", err)
			}
			assertLocalConsent(t, f.control, true, true, true)
			setupCommandWrite(t, policyPath, raw, 0600)
			// No physical lookup: revocation survives damaged profile and helper assets.
			if err := os.RemoveAll(b.ScopeRoot); err != nil {
				t.Fatal(err)
			}
			setupCommandWrite(t, b.ScopeRoot, "TEST-damaged-profile", 0600)
			if err := os.Remove(filepath.Join(b.RuntimeRoot, filepath.FromSlash(b.Primary))); err != nil {
				t.Fatal(err)
			}
			ledger, after, err := revokeRecordedLocal(setupCommandContext(t), b, selection, &s.Installation.Ledger.Generation, &s.Preimage)
			if err != nil {
				t.Fatal(err)
			}
			native, manual := selection == copilotvscodeinstall.RevokeManual, selection == copilotvscodeinstall.RevokeNative
			verified, e := installruntime.ReadRevocationSnapshot(setupCommandContext(t), f.control)
			if e != nil {
				t.Fatal(e)
			}
			var doc struct {
				Route struct {
					Local struct {
						Desktop, Webhook *bool
						Manual           struct{ Enabled *bool }
					} `json:"copilotVSCodeNotifications"`
				}
			}
			if e := json.Unmarshal([]byte(setupCommandRead(t, policyPath)), &doc); e != nil || doc.Route.Local.Desktop == nil || doc.Route.Local.Webhook == nil || doc.Route.Local.Manual.Enabled == nil || *doc.Route.Local.Desktop != native || *doc.Route.Local.Webhook != native || *doc.Route.Local.Manual.Enabled != manual {
				t.Fatal("actual false-only policy readback", e)
			}
			if verified.Preimage != after || verified.Generation != ledger.Generation || !reflect.DeepEqual(s.Installation.Ledger.Consumers, ledger.Consumers) || !reflect.DeepEqual(s.Installation.Ledger.Files, ledger.Files) {
				t.Fatal("false decision disturbed owned records")
			}
		})
	}
}

func TestLocalConfirmedConsentRefusesPreparedOnlyAndDrift(t *testing.T) {
	f, b := localConsentCommandFixture(t)
	s := assertLocalConsent(t, f.control, false, false, false)
	r := setupwizard.Request{ControlRoot: f.control, ScopeRoot: b.ScopeRoot, InstallationID: b.InstallationID}
	result := setupwizard.Result{Action: "install", Outcome: "completed", InstallationID: b.InstallationID, Generation: s.Installation.Ledger.Generation, Targets: []setupwizard.TargetResult{{Client: "copilot-vscode", Unit: "agent-notify", Outcome: "completed", Reason: b.BindingID}}}
	on := true
	i := confirmedBootstrapIntent{Initial: bootstrapInitialObservation{LedgerID: s.Installation.Ledger.ID, Owner: s.Installation.Ledger.Owner, Generation: s.Installation.Ledger.Generation, Policy: s.Preimage}, Request: setupProductsArgs{Desktop: true, DesktopSet: true, Manual: &on}}
	before := setupCommandRead(t, filepath.Join(f.control, "agent-notifications.json"))
	if _, err := commitConfirmedLocalConsent(setupCommandContext(t), r, result, i); err == nil {
		t.Fatal("generic target completion manufactured installed Local acknowledgement")
	}
	setupCommandWrite(t, filepath.Join(f.control, "agent-notifications.json"), before+"\n", 0600)
	if _, err := commitConfirmedLocalConsent(setupCommandContext(t), r, result, i); !errors.Is(err, portablesetup.ErrConcurrentChange) {
		t.Fatal("same-generation byte drift borrowed", err)
	}
	assertLocalConsent(t, f.control, false, false, false)
}

func TestLocalWizardEarlyRevocationAndAmbiguity(t *testing.T) {
	for _, action := range []string{"disable-manual", "update-empty-manual-off", "update-mcp-manual-off", "remove", "remove-manual-off", "ambiguous"} {
		t.Run(action, func(t *testing.T) {
			f, b := localConsentCommandFixture(t)
			s := setLocalTestConsent(t, b, true, true, true)
			if action == "ambiguous" {
				other := b
				other.BindingID = "TEST-other-local-binding"
				key, consumer, _, _ := other.Registration()
				if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: key, Consumer: consumer, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage}); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(b.ScopeRoot); err != nil {
				t.Fatal(err)
			}
			setupCommandWrite(t, b.ScopeRoot, "TEST-damaged", 0600)
			args := []string{"--action", "install", "--agents", "copilot-vscode", "--agent-notify", "false", "--scope-root", b.ScopeRoot, "--client-executable", f.command, "--control-root", f.control, "--yes", "--json"}
			if action == "remove" || action == "remove-manual-off" {
				args[1] = "uninstall"
				if action == "remove" {
					args = append(args[:4], args[6:]...)
				}
			}
			if strings.HasPrefix(action, "update-") {
				args[1] = "update"
				mcp := "false"
				if action == "update-mcp-manual-off" {
					mcp = "true"
				}
				args = append(args, "--local-settings", filepath.Join(b.ScopeRoot, "settings.json"), "--local-native-stop", "false", "--local-mcp", mcp, "--local-skills", "false")
			}
			var out bytes.Buffer
			code := executeSetupWizardWith(setupCommandContext(t), args, &out, io.Discard, strings.NewReader(""), false)
			switch action {
			case "disable-manual":
				if code != 0 {
					t.Fatal("early manual revoke", out.String())
				}
				assertLocalConsent(t, f.control, true, true, false)
			case "remove", "remove-manual-off", "update-empty-manual-off", "update-mcp-manual-off":
				if code == 0 {
					t.Fatal("damaged cleanup falsely completed", out.String())
				}
				assertLocalConsent(t, f.control, false, false, false)
			default:
				if code == 0 {
					t.Fatal("ambiguous selection succeeded")
				}
				assertLocalConsent(t, f.control, true, true, true)
			}
		})
	}
}

func TestLocalRegistrationByteCASAndForeignPolicy(t *testing.T) {
	f, b := localConsentCommandFixture(t)
	ctx := setupCommandContext(t)
	s := setLocalTestConsent(t, b, true, true, true)
	policyPath := filepath.Join(f.control, "agent-notifications.json")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(setupCommandRead(t, policyPath)), &doc); err != nil {
		t.Fatal(err)
	}
	var route map[string]json.RawMessage
	if err := json.Unmarshal(doc["route"], &route); err != nil {
		t.Fatal(err)
	}
	var local map[string]json.RawMessage
	if err := json.Unmarshal(route["copilotVSCodeNotifications"], &local); err != nil {
		t.Fatal(err)
	}
	local["foreign"] = json.RawMessage(`{"retained":true}`)
	local["manual"] = json.RawMessage(`{"enabled":true,"foreign":"TEST-manual"}`)
	route["copilotVSCodeNotifications"], _ = json.Marshal(local)
	route["foreign"] = json.RawMessage(`{"TEST":42}`)
	doc["route"], _ = json.Marshal(route)
	body, _ := json.MarshalIndent(doc, "", "  ")
	setupCommandWrite(t, policyPath, string(body)+"\n", 0600)
	oldGeneration := s.Installation.Ledger.Generation
	if _, err := (portablesetup.Service{ExpectedPolicy: &s.Preimage}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: oldGeneration}); !errors.Is(err, portablesetup.ErrConcurrentChange) {
		t.Fatal("exact-binding registration adopted same-generation bytes", err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	if s.Installation.Ledger.Generation != oldGeneration {
		t.Fatal("fixture changed generation")
	}
	b.GlobalConfig = filepath.Join(f.root, "TEST-rebind-config.json")
	expected := s.Preimage
	if _, err := (portablesetup.Service{ExpectedPolicy: &expected}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: oldGeneration}); err != nil {
		t.Fatal(err)
	}
	after := assertLocalConsent(t, f.control, false, false, false)
	if err := json.Unmarshal(after.Fields["route"], &route); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(route["copilotVSCodeNotifications"], &local); err != nil {
		t.Fatal(err)
	}
	var manual map[string]json.RawMessage
	if err := json.Unmarshal(local["manual"], &manual); err != nil {
		t.Fatal(err)
	}
	var sibling struct{ TEST int }
	var nested struct{ Retained bool }
	var manualForeign string
	if json.Unmarshal(route["foreign"], &sibling) != nil || sibling.TEST != 42 || json.Unmarshal(local["foreign"], &nested) != nil || !nested.Retained || json.Unmarshal(manual["foreign"], &manualForeign) != nil || manualForeign != "TEST-manual" {
		t.Fatal("Local false registration lost foreign policy", string(after.Fields["route"]))
	}
}

func TestLocalPlanRunFallbackParserCompositionRoundTrip(t *testing.T) {
	f, _, _ := configureFixture(t)
	ledger, _, err := installruntime.ReadOwnership(f.control)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(setupCommandRead(t, f.command))
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: ledger.Owner, ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &ledger.Generation, Files: []installruntime.File{{Path: filepath.Join(f.runtime, filepath.FromSlash(portable.PlatformPrimary())), Data: body, Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}

	settings := filepath.Join(f.root, "TEST-fresh-local", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	setupCommandWrite(t, settings, `{"foreign":true}`, 0600)
	for bits := 1; bits < 8; bits++ {
		args := []string{"--action", "install", "--agents", "copilot-vscode", "--scope-root", filepath.Dir(settings), "--client-executable", f.command, "--local-settings", settings, "--local-native-stop", fmt.Sprint(bits&1 != 0), "--local-mcp", fmt.Sprint(bits&2 != 0), "--local-skills", fmt.Sprint(bits&4 != 0), "--control-root", f.control, "--package", filepath.Join(f.root, "TEST-missing-package"), "--yes", "--json"}
		original, _, err := parseSetupWizard(args)
		if err != nil {
			t.Fatal(err)
		}
		original.ClientExecutable = "" // exercise the real per-client executable carrier
		prepared, early, err := prepareLocalWizard(setupCommandContext(t), original, nil)
		if err != nil && (runtime.GOOS != "linux" || early == nil || early.Reason != "local_adapter_unavailable") {
			t.Fatal("initial composition", err, early)
		}
		for _, route := range []string{"Plan", "Run"} {
			var result setupwizard.Result
			if route == "Plan" {
				plan, _ := setupwizard.Plan(setupCommandContext(t), prepared)
				result = plan.Result
			} else {
				result, _ = setupwizard.Run(setupCommandContext(t), prepared)
			}
			if len(result.Command) < 3 {
				t.Fatalf("%s has no retry: %+v", route, result)
			}
			replay, _, err := parseSetupWizard(result.Command[2:])
			if err != nil {
				t.Fatalf("%s retry parser: %v %v", route, result.Command, err)
			}
			if !sameLocalWizardSelection(prepared, replay) || replay.Helper != prepared.Helper || replay.ClientExecutables["copilot-vscode"] != f.command {
				t.Fatalf("%s lost retry selection: %+v", route, replay)
			}
			restored, early, err := prepareLocalWizard(setupCommandContext(t), replay, nil)
			if (err != nil && (runtime.GOOS != "linux" || early == nil || early.Reason != "local_adapter_unavailable")) || !sameLocalWizardSelection(prepared, restored) || restored.BindingIDs["copilot-vscode"] != prepared.BindingIDs["copilot-vscode"] {
				t.Fatal("retry composition drift", route, err, early)
			}
		}
	}
}
