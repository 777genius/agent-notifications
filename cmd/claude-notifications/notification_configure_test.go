//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func configureFixture(t *testing.T) (setupCommandFixture, notificationConfigureRequest, notificationConfigureDependencies) {
	t.Helper()
	f := newSetupCommandFixture(t)
	f.composition.permission = func(_ context.Context, _ string, _ installruntime.InstalledSnapshot, request bool) (string, error) {
		if request {
			t.Fatal("unexpected permission request")
		}
		return "undetermined", nil
	}
	t.Setenv("HOME", f.root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(f.root, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(f.root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(f.root)
	if err := os.MkdirAll(filepath.Join(f.runtime, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	setupCommandWrite(t, filepath.Join(f.runtime, "config", "config.json"), setupCommandRead(t, f.global), 0600)
	_, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, Files: []installruntime.File{{Path: filepath.Join(f.runtime, "skills", "agent-notify", "SKILL.md"), Data: []byte("canonical test skill"), Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	return f, notificationConfigureRequest{Provider: "both", CodexHome: filepath.Join(f.root, "codex"), Route: &notifysetup.Route{}}, notificationConfigureDependencies{Home: f.root, BundleRoot: f.runtime, ControlRoot: f.control, Composition: f.composition}
}
func TestNotificationConfigureBothAndRetry(t *testing.T) {
	f, request, deps := configureFixture(t)
	ctx := setupCommandContext(t)
	result, err := configureNotifications(ctx, request, deps)
	if err != nil || !result.ExplicitIntent || result.Activation != "activation_required" {
		t.Fatal(result, err)
	}
	if result.Permission != "undetermined" {
		t.Fatal(result)
	}
	for _, path := range []string{filepath.Join(request.CodexHome, "config.toml"), filepath.Join(f.root, ".claude.json")} {
		if !strings.Contains(setupCommandRead(t, path), f.command) {
			t.Fatal("primary command missing", path)
		}
	}
	global := filepath.Join(f.root, ".claude", "claude-notifications-go", "config.json")
	if setupCommandRead(t, global) != setupCommandRead(t, f.global) {
		t.Fatal("global restrictions changed")
	}
	request.Route = nil
	if _, err = configureNotifications(ctx, request, deps); err != nil {
		t.Fatal("retry:", err)
	}
	if setupCommandRead(t, filepath.Join(request.CodexHome, "skills", "agent-notify", "SKILL.md")) != "canonical test skill" {
		t.Fatal("skill source")
	}
}
func TestNotificationConfigurePreflightNoWrites(t *testing.T) {
	for _, kind := range []string{"provider", "relative_home", "fresh_route", "unknown", "collision", "malformed_global", "foreign_registration"} {
		t.Run(kind, func(t *testing.T) {
			f, request, deps := configureFixture(t)
			switch kind {
			case "provider":
				request.Provider = "auto"
			case "relative_home":
				request.CodexHome = "relative"
			case "fresh_route":
				request.Route = nil
			case "unknown", "collision":
				deps.Inventory = func(context.Context, registration.Provider, string) (notificationInventory, error) {
					return notificationInventory{State: kind}, nil
				}
			case "malformed_global":
				setupCommandWrite(t, filepath.Join(f.runtime, "config", "config.json"), `{"notifications":false}`, 0600)
			case "foreign_registration":
				setupCommandWrite(t, filepath.Join(f.root, ".claude.json"), `{"mcpServers":{"agent_notifications":{"command":"foreign"}}}`, 0600)
			}
			before := setupCommandTree(t, f.root)
			if _, err := configureNotifications(setupCommandContext(t), request, deps); err == nil {
				t.Fatal("accepted", kind)
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("preflight wrote", kind)
			}
		})
	}
}
func TestNotificationConfigurePermissionPartial(t *testing.T) {
	f, request, deps := configureFixture(t)
	calls := 0
	deps.Composition.permission = func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
		calls++
		return "denied", nil
	}
	request.RequestPermission = true
	result, err := configureNotifications(setupCommandContext(t), request, deps)
	if err == nil || calls != 1 || result.ExplicitIntent || result.Permission != "denied" || result.Activation != "activation_required" {
		t.Fatal(result, err, calls)
	}
	if !strings.Contains(setupCommandRead(t, filepath.Join(f.root, ".claude.json")), f.command) {
		t.Fatal("registration not saved")
	}
	deps.Composition.permission = func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
		calls++
		return "allowed", nil
	}
	result, err = configureNotifications(setupCommandContext(t), request, deps)
	if err != nil || calls != 2 || !result.ExplicitIntent {
		t.Fatal(result, err, calls)
	}
}
func TestNotificationConfigureSecondClientFailure(t *testing.T) {
	proveConfigureSecondClientFailure(t)
}

func proveConfigureSecondClientFailure(t *testing.T) {
	t.Helper()
	f, request, deps := configureFixture(t)
	checks := 0
	deps.Inventory = func(ctx context.Context, p registration.Provider, home string) (notificationInventory, error) {
		inv, err := inspectNotificationInventory(ctx, p, home)
		if p == registration.Claude {
			original := inv.Revalidate
			inv.Revalidate = func(ctx context.Context) error {
				checks++
				if checks == 3 {
					return errors.New("fixture inventory race")
				}
				return original(ctx)
			}
		}
		return inv, err
	}
	result, err := configureNotifications(setupCommandContext(t), request, deps)
	if err == nil || result.ExplicitIntent {
		t.Fatal(result, err)
	}
	codex := setupCommandRead(t, filepath.Join(request.CodexHome, "config.toml"))
	if _, err := os.Stat(filepath.Join(f.root, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("second client wrote")
	}
	deps.Inventory = nil
	if _, err = configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	if setupCommandRead(t, filepath.Join(request.CodexHome, "config.toml")) != codex {
		t.Fatal("first client lost")
	}
}
func TestNotificationConfigureSharedRoute(t *testing.T) {
	f, request, deps := configureFixture(t)
	request.Provider = "codex"
	request.Route = &notifysetup.Route{LocalRouting: true, ApplicationPath: f.app, TeamID: "TEAM123456", AllowUnknownCaller: true}
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	request.Provider = "claude"
	request.Route = nil
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadPolicySnapshot(setupCommandContext(t), f.control)
	if err != nil {
		t.Fatal(err)
	}
	var route notifysetup.Route
	if err = json.Unmarshal(snapshot.Fields["route"], &route); err != nil || route.ApplicationPath != f.app || !route.AllowUnknownCaller {
		t.Fatal(route, err)
	}
}

func TestNotificationConfigureRejectsUnownedBundle(t *testing.T) {
	_, request, deps := configureFixture(t)
	deps.BundleRoot = filepath.Join(t.TempDir(), "source-bundle")
	if err := os.Mkdir(deps.BundleRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err == nil {
		t.Fatal("source bundle accepted")
	}
}

func TestNotificationConfigurePrimaryOrders(t *testing.T) {
	for _, first := range []string{"codex", "claude"} {
		t.Run(first, func(t *testing.T) {
			f, request, deps := configureFixture(t)
			secondary := filepath.Join(f.root, "secondary")
			_, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: secondary, Owner: "existing-installer", ConsumerID: "secondary", Consumer: installruntime.Consumer{Commands: []string{"secondary hook"}}, Files: []installruntime.File{{Path: filepath.Join(secondary, "bin", "claude-notifications"), Data: []byte("inert secondary " + installruntime.WriterProtocolMarker), Mode: 0700}}})
			if err != nil {
				t.Fatal(err)
			}
			// Invoke from the other installed consumer; primary authority remains stable.
			deps.BundleRoot = secondary
			request.Provider = first
			if _, err = configureNotifications(setupCommandContext(t), request, deps); err != nil {
				t.Fatal(err)
			}
			request.Route = nil
			if first == "codex" {
				request.Provider = "claude"
			} else {
				request.Provider = "codex"
			}
			if _, err = configureNotifications(setupCommandContext(t), request, deps); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{filepath.Join(f.root, ".claude.json"), filepath.Join(request.CodexHome, "config.toml")} {
				raw := setupCommandRead(t, p)
				if !strings.Contains(raw, f.command) || strings.Contains(raw, secondary) {
					t.Fatal("secondary command selected", raw)
				}
			}
		})
	}
}
func TestNotificationConfigureSkillConflictAndRefresh(t *testing.T) {
	f, request, deps := configureFixture(t)
	request.Provider = "codex"
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	original := deps
	deps.Inventory = func(context.Context, registration.Provider, string) (notificationInventory, error) {
		return notificationInventory{State: "clear", Skill: true, Revalidate: func(context.Context) error { return nil }}, nil
	}
	before := setupCommandTree(t, f.root)
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err == nil {
		t.Fatal("projection plus plugin accepted")
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("conflict wrote")
	}
	source := filepath.Join(f.runtime, "skills", "agent-notify", "SKILL.md")
	fp, err := installruntime.Fingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, Files: []installruntime.File{{Path: source, Before: fp, Data: []byte("refreshed canonical skill"), Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	request.Route = nil
	if _, err = configureNotifications(setupCommandContext(t), request, original); err != nil {
		t.Fatal(err)
	}
	if setupCommandRead(t, filepath.Join(request.CodexHome, "skills", "agent-notify", "SKILL.md")) != "refreshed canonical skill" {
		t.Fatal("skill not refreshed")
	}
}

func TestNotificationConfigurePreservesClientRestrictions(t *testing.T) {
	f, request, deps := configureFixture(t)
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(request.CodexHome, "config.toml")
	restricted := setupCommandRead(t, codex) + "\nenabled = false\nenabled_tools = [\"notify\"]\ndisabled_tools = [\"danger\"]\n"
	setupCommandWrite(t, codex, restricted, 0600)
	claude := filepath.Join(f.root, ".claude.json")
	var document map[string]any
	if err := json.Unmarshal([]byte(setupCommandRead(t, claude)), &document); err != nil {
		t.Fatal(err)
	}
	server := document["mcpServers"].(map[string]any)["agent_notifications"].(map[string]any)
	server["enabled"] = false
	server["enabled_tools"] = []string{"notify"}
	raw, _ := json.Marshal(document)
	setupCommandWrite(t, claude, string(raw), 0600)
	request.Route = nil
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	if setupCommandRead(t, codex) != restricted || setupCommandRead(t, claude) != string(raw) {
		t.Fatal("client restrictions rewritten")
	}
}

func TestNotificationConfigurePluginSingleSource(t *testing.T) {
	_, request, deps := configureFixture(t)
	request.Provider = "codex"
	deps.Inventory = func(context.Context, registration.Provider, string) (notificationInventory, error) {
		return notificationInventory{State: "clear", Skill: true, Revalidate: func(context.Context) error { return nil }}, nil
	}
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(request.CodexHome, "skills", "agent-notify", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("duplicate user projection", err)
	}
	request.Route = nil
	if _, err := configureNotifications(setupCommandContext(t), request, deps); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationConfigureReportsPersistedGlobal(t *testing.T) {
	f, request, deps := configureFixture(t)
	request.Route = nil
	result, err := configureNotifications(setupCommandContext(t), request, deps)
	if err == nil || result.DesktopEnabled != nil || result.GlobalConfiguration != "configuration_required" {
		t.Fatal("reported an uncommitted candidate", result, err)
	}
	request.Route = &notifysetup.Route{}
	request.RequestPermission = true
	deps.Composition.permission = func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
		setupCommandWrite(t, filepath.Join(f.root, ".claude", "claude-notifications-go", "config.json"), `{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":false}}}`, 0600)
		return "denied", nil
	}
	result, err = configureNotifications(setupCommandContext(t), request, deps)
	if err == nil || result.DesktopEnabled == nil || !*result.DesktopEnabled || result.GlobalConfiguration != "configured" || result.ExplicitIntent {
		t.Fatal("stale global outcome", result, err)
	}
}

func TestNotificationConfigureUnknownStatusIsNotDisabled(t *testing.T) {
	f, request, deps := configureFixture(t)
	setupCommandWrite(t, filepath.Join(f.control, "agent-notifications.json"), "invalid policy", 0600)
	result, err := configureNotifications(setupCommandContext(t), request, deps)
	if err == nil || result.IntentObserved || result.RuntimeObserved {
		t.Fatal(result, err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err = json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"explicitIntent", "runtimeEligible"} {
		value, present := output[key]
		if !present || value != nil {
			t.Fatal("unknown status asserted disabled", string(raw))
		}
	}
}

func TestNotificationConfigureSkillAbsenceRace(t *testing.T) {
	for _, at := range []string{"registration", "enable"} {
		t.Run(at, func(t *testing.T) {
			f, request, deps := configureFixture(t)
			request.Provider = "codex"
			destination := filepath.Join(request.CodexHome, "skills", "agent-notify", "SKILL.md")
			checks := 0
			deps.Inventory = func(context.Context, registration.Provider, string) (notificationInventory, error) {
				return notificationInventory{State: "clear", Skill: true, Revalidate: func(context.Context) error {
					checks++
					if at == "registration" && checks == 2 {
						if e := os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
							t.Fatal(e)
						}
						setupCommandWrite(t, destination, "foreign skill", 0600)
					}
					return nil
				}}, nil
			}
			deps.Composition.permission = func(_ context.Context, _ string, _ installruntime.InstalledSnapshot, requested bool) (string, error) {
				if requested {
					t.Fatal("prompt")
				}
				if at == "enable" {
					if e := os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
						t.Fatal(e)
					}
					setupCommandWrite(t, destination, "foreign skill", 0600)
				}
				return "allowed", nil
			}
			result, err := configureNotifications(setupCommandContext(t), request, deps)
			if err == nil || result.Reason != "skill_collision" || result.ExplicitIntent {
				t.Fatal(result, err)
			}
			if setupCommandRead(t, destination) != "foreign skill" {
				t.Fatal("foreign skill changed")
			}
			_, e := os.Stat(filepath.Join(request.CodexHome, "config.toml"))
			if at == "registration" && !os.IsNotExist(e) {
				t.Fatal("registered after collision")
			}
			if at == "enable" && e != nil {
				t.Fatal("completed registration lost", e)
			}
			_ = f
		})
	}
}

func TestNotificationConfigureReadOnlyPermission(t *testing.T) {
	for _, outcome := range []string{"allowed", "denied", "undetermined", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			_, request, deps := configureFixture(t)
			calls := 0
			deps.Composition.permission = func(_ context.Context, _ string, _ installruntime.InstalledSnapshot, requested bool) (string, error) {
				calls++
				if requested {
					t.Fatal("implicit request")
				}
				return outcome, nil
			}
			result, err := configureNotifications(setupCommandContext(t), request, deps)
			if err != nil || calls != 1 || result.Permission != outcome || !result.ExplicitIntent {
				t.Fatal(result, err, calls)
			}
		})
	}
}

func TestNotificationProductionRejectsGlobalOverride(t *testing.T) {
	f, _, _ := configureFixture(t)
	for _, operation := range []string{"status", "prepare", "enable"} {
		t.Run(operation, func(t *testing.T) {
			before := setupCommandTree(t, f.root)
			args := []string{operation, "--control-root", f.control, "--global-config", f.global, "--json"}
			if operation != "status" {
				args = append(args, "--expected-generation", "1")
			}
			if operation == "prepare" {
				args = append(args, "--legacy-config", f.global, "--defaults-config", f.global)
			}
			var out strings.Builder
			code := agentNotifySetupExecute(context.Background(), args, &out, agentNotifySetupComposition{})
			if code != 2 || !strings.Contains(out.String(), "unsupported_global_config") {
				t.Fatal(code, out.String())
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("override wrote")
			}
		})
	}
}
