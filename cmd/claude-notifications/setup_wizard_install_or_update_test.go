//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/loader"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/packagedigest"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/profileauthority"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/specregistry"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/planner"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"
)

func TestWizardInstallOrUpdateBootstrapE2E(t *testing.T) {
	for _, tc := range []struct {
		name, existing, selected string
		asZip                    bool
	}{
		{name: "fresh_both", selected: "claude,codex"},
		{name: "upgrade_codex", existing: "codex", selected: "codex"},
		{name: "upgrade_both", existing: "claude,codex", selected: "claude,codex"},
		{name: "upgrade_claude_then_add_codex", existing: "claude", selected: "claude,codex"},
		{name: "upgrade_codex_then_add_claude", existing: "codex", selected: "claude,codex"},
		{name: "fresh_both_zip", selected: "claude,codex", asZip: true},
		{name: "upgrade_both_zip", existing: "claude,codex", selected: "claude,codex", asZip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			env := newWizardCLIEnv(t, ctx, false)
			oldPackage := filepath.Join(env.root, "old-package")
			copyWizardPackage(t, env.pkg, oldPackage)
			shared := []string{
				"--hooks", "false", "--agent-notify", "true", "--yes", "--json",
				"--control-root", env.control, "--runtime-root", env.runtime,
				"--global-config", env.global, "--codex-home", env.codexHome,
				"--claude-config", env.claudeConfig, "--claude-executable", env.probe,
				"--codex-executable", env.probe, "--helper", env.probe,
				"--scope-root", env.scope,
			}
			invoke := func(agents, pkg string, auto bool) (int, setupwizard.Result) {
				t.Helper()
				args := []string{"--action", "install", "--agents", agents, "--package", pkg}
				if auto {
					args = append(args, "--install-or-update")
				}
				args = append(args, shared...)
				var out bytes.Buffer
				code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false)
				return code, decodeWizardJSON(t, out)
			}
			var old setupwizard.Result
			if tc.existing != "" {
				code, result := invoke(tc.existing, oldPackage, false)
				if code != 0 || result.Outcome != "completed" {
					t.Fatalf("old install: %d %+v", code, result)
				}
				old = result
			}
			if err := os.WriteFile(filepath.Join(env.pkg, "skills", "agent-notifications", "SKILL.md"), []byte("---\nname: agent-notifications\ndescription: Updated bootstrap fixture\n---\n"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate := env.pkg
			if tc.asZip {
				candidate = filepath.Join(env.root, "candidate.zip")
				if _, err := portableasset.Build(portableasset.BuildRequest{
					Version: "1.43.1", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
					Executable: env.probe, OutputRoot: filepath.Join(env.root, "candidate-built"), Archive: candidate,
				}); err != nil {
					t.Fatal(err)
				}
			}
			code, result := invoke(tc.selected, candidate, true)
			if code != 0 || (result.Outcome != "completed" && result.Outcome != "unchanged") {
				t.Fatalf("auto install/update: %d %+v", code, result)
			}
			if old.InstallationID != "" && result.InstallationID != old.InstallationID {
				t.Fatalf("installation identity changed: %s -> %s", old.InstallationID, result.InstallationID)
			}
			var out bytes.Buffer
			inspectArgs := append([]string{"--action", "inspect", "--agents", tc.selected}, shared...)
			if code := executeSetupWizardWith(ctx, inspectArgs, &out, io.Discard, strings.NewReader(""), false); code != 0 {
				t.Fatalf("inspect: %d %s", code, out.String())
			}
			view := decodeWizardJSON(t, out)
			for _, agent := range strings.Split(tc.selected, ",") {
				digest := wizardCLINotifyDigest(view, agent)
				if digest == "" {
					t.Fatalf("%s not installed: %+v", agent, view.Targets)
				}
				if strings.Contains(","+tc.existing+",", ","+agent+",") && digest == wizardCLINotifyDigest(old, agent) {
					t.Fatalf("%s kept old package digest: %s", agent, digest)
				}
			}
		})
	}
}

// A repeated automatic bootstrap must not turn a previously absent sibling
// into a new MCP binding. The explicit request may add that sibling.
func TestWizardInstallOrUpdatePreservesMixedOptOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--action", "install", "--hooks", "false", "--agent-notify", "true", "--yes", "--json",
		"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe,
		"--helper", env.probe, "--scope-root", env.scope,
	}
	invoke := func(agents string, flags ...string) (int, setupwizard.Result) {
		t.Helper()
		args := append([]string{"--agents", agents}, shared...)
		args = append(args, flags...)
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false)
		return code, decodeWizardJSON(t, out)
	}
	if code, first := invoke("codex"); code != 0 || first.Outcome != "completed" {
		t.Fatalf("initial Codex: %d %+v", code, first)
	}
	code, repeated := invoke("claude,codex", "--install-or-update", "--preserve-existing-units")
	if code != 0 || (repeated.Outcome != "completed" && repeated.Outcome != "unchanged") {
		t.Fatalf("auto repeat: %d %+v", code, repeated)
	}
	if wizardCLINotifyDigest(repeated, "claude") != "" {
		t.Fatalf("auto repeat added opted-out Claude: %+v", repeated.Targets)
	}
	if code, added := invoke("claude,codex", "--install-or-update"); code != 0 || wizardCLINotifyDigest(added, "claude") == "" {
		t.Fatalf("explicit Add: %d %+v", code, added)
	}
}

// /init must update to the current binary's verified release asset even when
// an older portable package remains recorded in the installation.
func TestWizardInstallOrUpdateFetchesCurrentRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	off, on := false, true
	req := setupwizard.Request{
		Action: setupwizard.ActionInstall, Agents: []string{"codex"}, Hooks: &off, AgentNotify: &on, Yes: true,
		PackageRoot: env.pkg, ControlRoot: env.control, RuntimeRoot: env.runtime, GlobalConfig: env.global,
		CodexHome: env.codexHome, ClientExecutable: env.probe, Helper: env.probe, ScopeRoot: env.scope,
	}
	old, err := setupwizard.Run(ctx, req)
	if err != nil || old.Outcome != "completed" {
		t.Fatalf("old package install: %+v %v", old, err)
	}
	archive := filepath.Join(env.root, "new-release.zip")
	if _, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.1", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: env.probe, OutputRoot: filepath.Join(env.root, "new-release-build"), Archive: archive,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	asset := portableasset.AssetName(runtime.GOOS, runtime.GOARCH)
	digest := sha256.Sum256(data)
	checksums := []byte(hex.EncodeToString(digest[:]) + "  " + asset + "\n")
	req.PackageRoot = ""
	req.DefaultReleaseVersion = "1.43.1"
	req.ReleaseDownloadRoot = "https://fixture.invalid/releases"
	req.PackageFetcher = func(_ context.Context, url string) ([]byte, error) {
		if strings.HasSuffix(url, "/checksums.txt") {
			return checksums, nil
		}
		if strings.HasSuffix(url, "/"+asset) {
			return data, nil
		}
		return nil, os.ErrNotExist
	}
	updated, err := runInstallOrUpdate(ctx, req, false)
	if err != nil || (updated.Outcome != "completed" && updated.Outcome != "unchanged") {
		t.Fatalf("new release update: %+v %v", updated, err)
	}
	if wizardCLINotifyDigest(old, "codex") == wizardCLINotifyDigest(updated, "codex") {
		t.Fatalf("old portable digest retained: old=%+v new=%+v", old.Targets, updated.Targets)
	}
}

// A user can remove a binding while a release is downloading. The automatic
// update must not reinterpret that removal as consent to add it again.
func TestWizardInstallOrUpdateDoesNotUndoConcurrentOptOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	off, on := false, true
	req := setupwizard.Request{
		Action: setupwizard.ActionInstall, Agents: []string{"codex"}, Hooks: &off, AgentNotify: &on, Yes: true,
		PackageRoot: env.pkg, ControlRoot: env.control, RuntimeRoot: env.runtime, GlobalConfig: env.global,
		CodexHome: env.codexHome, ClientExecutable: env.probe, Helper: env.probe, ScopeRoot: env.scope,
	}
	if installed, err := setupwizard.Run(ctx, req); err != nil || installed.Outcome != "completed" {
		t.Fatalf("initial install: %+v %v", installed, err)
	}
	archive := filepath.Join(env.root, "new-release.zip")
	if _, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.1", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: env.probe, OutputRoot: filepath.Join(env.root, "new-release-build"), Archive: archive,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	asset := portableasset.AssetName(runtime.GOOS, runtime.GOARCH)
	digest := sha256.Sum256(data)
	checksums := []byte(hex.EncodeToString(digest[:]) + "  " + asset + "\n")
	req.PackageRoot = ""
	req.DefaultReleaseVersion = "1.43.1"
	req.ReleaseDownloadRoot = "https://fixture.invalid/releases"
	removed := false
	req.PackageFetcher = func(_ context.Context, url string) ([]byte, error) {
		if !removed {
			removed = true
			uninstall := req
			uninstall.Action = setupwizard.ActionUninstall
			uninstall.PackageFetcher = nil
			uninstall.ExternalUninstalled = true
			if result, err := setupwizard.Run(ctx, uninstall); err != nil || result.Outcome != "completed" {
				t.Fatalf("concurrent opt-out: %+v %v", result, err)
			}
		}
		if strings.HasSuffix(url, "/checksums.txt") {
			return checksums, nil
		}
		if strings.HasSuffix(url, "/"+asset) {
			return data, nil
		}
		return nil, os.ErrNotExist
	}
	result, err := runInstallOrUpdate(ctx, req, true)
	if !removed || err == nil || result.Outcome != "conflict" || result.Reason != "concurrent_change" {
		t.Fatalf("auto update restored concurrent opt-out: %+v %v", result, err)
	}
	if len(result.Command) != 0 || len(result.NextActions) != 0 {
		t.Fatalf("stale auto selection offered an unsafe direct retry: %+v", result)
	}
	inspect := req
	inspect.Action = setupwizard.ActionInspect
	inspect.PackageFetcher = nil
	view, err := setupwizard.Run(ctx, inspect)
	if err != nil || wizardCLINotifyDigest(view, "codex") != "" {
		t.Fatalf("opt-out did not remain absent: %+v %v", view, err)
	}
}

func TestWizardInstallOrUpdateRejectsOptOutBeforeIntentPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	off, on := false, true
	req := setupwizard.Request{
		Action: setupwizard.ActionInstall, Agents: []string{"codex"}, Hooks: &off, AgentNotify: &on, Yes: true,
		PackageRoot: env.pkg, ControlRoot: env.control, RuntimeRoot: env.runtime, GlobalConfig: env.global,
		CodexHome: env.codexHome, ClientExecutable: env.probe, Helper: env.probe, ScopeRoot: env.scope,
	}
	if installed, err := setupwizard.Run(ctx, req); err != nil || installed.Outcome != "completed" {
		t.Fatalf("initial install: %+v %v", installed, err)
	}
	candidate := filepath.Join(env.root, "candidate")
	copyWizardPackage(t, env.pkg, candidate)
	if err := os.WriteFile(filepath.Join(candidate, "skills", "agent-notifications", "SKILL.md"), []byte("---\nname: agent-notifications\ndescription: Candidate\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req.PackageRoot = candidate
	removed := false
	req.Progress = func(phase string) {
		if phase != "preflight" || removed {
			return
		}
		removed = true
		uninstall := req
		uninstall.Action = setupwizard.ActionUninstall
		uninstall.Progress = nil
		uninstall.ExternalUninstalled = true
		if result, err := setupwizard.Run(ctx, uninstall); err != nil || result.Outcome != "completed" {
			t.Fatalf("concurrent opt-out: %+v %v", result, err)
		}
	}
	result, err := runInstallOrUpdate(ctx, req, true)
	if !removed || err == nil || result.Outcome != "conflict" || result.Reason != "concurrent_change" {
		t.Fatalf("stale update published intent: %+v %v", result, err)
	}
	if len(result.Command) != 0 || len(result.NextActions) != 0 {
		t.Fatalf("stale auto selection offered an unsafe direct retry: %+v", result)
	}
	inspect := req
	inspect.Action, inspect.Progress = setupwizard.ActionInspect, nil
	view, err := setupwizard.Run(ctx, inspect)
	if err != nil || wizardCLINotifyDigest(view, "codex") != "" {
		t.Fatalf("opt-out did not remain absent: %+v %v", view, err)
	}
}

func TestWizardInstallOrUpdateRejectsUnselectedPrerequisite(t *testing.T) {
	selected := []string{"claude"}
	next := []setupwizard.NextAction{
		{Kind: "update", Agents: []string{"codex"}},
		{Kind: "install", Agents: []string{"claude"}},
	}
	if validInstallOrUpdateActions(selected, next) {
		t.Fatal("bootstrap may not mutate an unselected sibling")
	}
	if validInstallOrUpdateActions(selected, []setupwizard.NextAction{{Kind: "update", Agents: []string{"claude", "claude"}}}) {
		t.Fatal("duplicate client action accepted")
	}
	if validInstallOrUpdateActions(selected, []setupwizard.NextAction{{Kind: "repair", Agents: selected}}) {
		t.Fatal("unexpected action accepted")
	}
}

func TestWizardInstallOrUpdateRetainedDataE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	oldPackage := filepath.Join(env.root, "old-package")
	copyWizardPackage(t, env.pkg, oldPackage)
	shared := []string{
		"--agents", "codex", "--hooks", "false", "--agent-notify", "true", "--yes", "--json",
		"--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	invoke := func(action, pkg string, extra ...string) (int, setupwizard.Result) {
		t.Helper()
		args := append([]string{"--action", action, "--package", pkg}, shared...)
		args = append(args, extra...)
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false)
		return code, decodeWizardJSON(t, out)
	}
	code, old := invoke("install", oldPackage)
	if code != 0 || old.Outcome != "completed" {
		t.Fatalf("old install: %d %+v", code, old)
	}
	dataRoot := wizardCLIBinding(t, ctx, env.root, "codex").DataRoot
	sentinel := filepath.Join(dataRoot, "retain.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	code, removed := invoke("uninstall", oldPackage, "--external-uninstalled")
	if code != 0 || !removed.DataRetained {
		t.Fatalf("retained uninstall: %d %+v", code, removed)
	}
	if err := os.WriteFile(filepath.Join(env.pkg, "skills", "agent-notifications", "SKILL.md"), []byte("---\nname: agent-notifications\ndescription: Retained bootstrap update\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, restored := invoke("install", env.pkg, "--install-or-update")
	if code != 0 || restored.Outcome != "completed" || restored.InstallationID != old.InstallationID {
		t.Fatalf("retained bootstrap restore: %d %+v", code, restored)
	}
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "keep" {
		t.Fatalf("retained data changed: %q %v", body, err)
	}
}

func TestWizardInstallOrUpdateRequiresBootstrapContract(t *testing.T) {
	for _, args := range [][]string{
		{"--action", "update", "--agents", "claude", "--hooks", "false", "--agent-notify", "true", "--yes", "--install-or-update"},
		{"--action", "install", "--agents", "claude", "--hooks", "true", "--agent-notify", "true", "--yes", "--install-or-update"},
		{"--action", "install", "--agents", "claude", "--hooks", "false", "--agent-notify", "true", "--yes", "--install-or-update", "--install-or-update"},
	} {
		var out bytes.Buffer
		if code := executeSetupWizardWith(context.Background(), append(args, "--json"), &out, io.Discard, strings.NewReader(""), false); code != 2 {
			t.Fatalf("invalid bootstrap contract accepted: %d %s", code, out.String())
		}
	}
}

// Regression: existing caller uses historical Cursor to verify a valid original
// token or refuses a matching retry. Real public token/validated TEST state;
// installed/native proof is NOT_RUN.
func TestCursorCallerAcknowledgedTokenAndMatchingPendingRetry(t *testing.T) {
	ctx := setupCommandContext(t)
	root := setupCommandRoot(t)
	profile := filepath.Join(root, ".cursor")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := cursorQualifiedNamespace(profile); err != nil {
		t.Logf("NOT_RUN positive caller/P1/resume: TEST full ancestry outside fixed qualified tuple: %v", err)
		return
	}
	token, err := profileauthority.Capture(ctx, profile)
	if err != nil || token.IsZero() {
		t.Fatalf("qualified TEST Capture: %v", err)
	}
	control, runtimeRoot := filepath.Join(root, "control"), filepath.Join(root, "runtime")
	observer := filepath.Join(runtimeRoot, filepath.FromSlash(portable.PlatformPrimary()))
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-owner", Files: []installruntime.File{{Path: observer, Data: []byte("TEST-inert" + installruntime.WriterProtocolMarker), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "TEST-version-agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf '%s\\n' '"+selectedCursorVersion+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "TEST-package")
	writeWizardPackage(t, pkg, agent)
	on, off := true, false
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, Hooks: &off, AgentNotify: &on, Yes: true, CursorConfig: profile, ScopeRoot: profile, ControlRoot: control, GlobalConfig: filepath.Join(root, "TEST-global.json"), ClientExecutable: agent, PackageRoot: pkg}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	r, cfg, _, _, err := reserveCursorCaller(ctx, r, snap)
	if err != nil {
		t.Fatal(err)
	}
	b, err := portablesetup.Complete(portablesetup.Identity{InstallationID: r.InstallationID, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner, ScopeRoot: profile, ControlRoot: control, GlobalConfig: r.GlobalConfig, RuntimeRoot: runtimeRoot, Primary: r.Primary}, portable.Cursor, "cursor", "user", filepath.Join(profile, "plugins", "local", domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID)), filepath.Dir(r.CursorAuthority.Selector))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	r.CursorAuthority.ObjectID = "TEST-retained-stop"
	adapter, err := cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, r.CursorAuthority)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.SourceIdentity{RequestedSource: pkg, CanonicalSource: pkg, SourceBindingHint: "direct-local"}
	snapshotRoot := filepath.Join(root, "TEST-snapshot")
	if err := os.Mkdir(snapshotRoot, 0700); err != nil {
		t.Fatal(err)
	}
	packageSnapshot, err := (packagedigest.Builder{TempRoot: snapshotRoot}).SnapshotWithExecutables(ctx, pkg, source, []string{"bin/probe"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = packagedigest.Remove(packageSnapshot) }()
	specs, err := specregistry.New()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := (loader.Loader{Registry: specs}).Load(ctx, domain.LoadInput{SnapshotRoot: packageSnapshot.Root, TreeDigest: packageSnapshot.TreeDigest, ExecutableFiles: packageSnapshot.ExecutableFiles, Source: packageSnapshot.Source})
	if err != nil {
		t.Fatal(err)
	}
	digest := envelope.TreeDigest
	selectedRegistry, err := clients.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := (planner.Planner{ManagedRoot: cfg.ManagedRoot, Paths: pathpolicy.Policy{}, Registry: selectedRegistry}).Plan(ctx, domain.PlanRequest{Envelope: envelope, Client: domain.DetectedClient{ClientID: domain.ClientCursor, Status: domain.DetectionDetected, ConfigRoot: profile, ProfileAuthority: &token}, Scope: domain.ScopeUser, PhysicalArtifactID: domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID)})
	if err != nil {
		t.Fatal(err)
	}
	dataReceipt, _, err := (providers.PluginDataManager{Base: cfg.PluginDataBase}).EnsureData(ctx, r.InstallationID, delivery.PhysicalArtifactID, "user")
	if err != nil {
		t.Fatal(err)
	}
	stagedDelivery, err := (providers.Stager{Registry: selectedRegistry, Paths: pathpolicy.Policy{}, SnapshotBuilder: packagesnapshot.Builder{TempRoot: filepath.Join(root, "TEST-stager")}}).StageWithPluginData(ctx, envelope, delivery, "TEST-public-pre-native", domain.CompatibilityHints{}, dataReceipt.Locator)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stagedDelivery.StagingPath, stagedDelivery.ActivePath); err != nil {
		t.Fatal(err)
	}
	selected, err := delivery.SelectedDelivery.WithProjectionDigest(stagedDelivery.ArtifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	facts, _ := selected.CursorFacts()
	client := domain.ClientBinding{ClientBindingID: b.BindingID, ClientID: "cursor", Scope: "user", TargetLocator: filepath.Join(profile, "plugins", "local", domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID)), PhysicalArtifact: domain.ComputePhysicalArtifactID("agent-notify", r.InstallationID),
		Materialization: domain.MaterializationMaterialized, Activation: domain.ActivationActive, Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed, Verification: domain.VerificationInstalled,
		DataReceiptID: dataReceipt.DataReceiptID, PackageRevision: &domain.ClientPackageRevision{Version: envelope.Manifest.Version, TreeDigest: envelope.TreeDigest, ManifestDigest: envelope.ManifestDigest}, NativeProfileRoot: profile, ProfileNamespace: cfg.StateRoot, ProfileAuthority: &token,
		SelectedDelivery: selected, NativeObjects: append(stagedDelivery.NativeObjects, selected.CursorOwnership(facts.PlannedReceipt))}
	store := statev2.Store{Path: filepath.Join(cfg.StateRoot, "state-v2.json")}
	if err := store.Save(domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion, Installations: []domain.Installation{{InstallationID: r.InstallationID, DeclaredName: "agent-notify",
		Source: domain.SourceBinding{SourceBindingID: domain.ComputeSourceBindingID(packageSnapshot.Source), RequestedSource: pkg, CanonicalSource: pkg, TreeDigest: digest}, Package: domain.PackageBinding{DeclaredName: "agent-notify", LoaderKind: envelope.LoaderKind, FormatID: envelope.FormatID, Version: envelope.Manifest.Version, ManifestDigest: envelope.ManifestDigest},
		Clients:      map[string]domain.ClientBinding{b.BindingID: client},
		DataReceipts: map[string]domain.DataReceipt{dataReceipt.DataReceiptID: dataReceipt}}}}); err != nil {
		t.Fatal(err)
	}
	// The public durable state exists, but the AN consumer and all native
	// attempts/objects are absent. No in-memory authority survives the parser.
	firstState, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	firstClient := client
	firstClient.Activation, firstClient.Verification, firstClient.NativeObjects = domain.ActivationPrepared, domain.VerificationPackageValid, stagedDelivery.NativeObjects
	firstState.Installations[0].Clients[b.BindingID] = firstClient
	if err := store.Save(firstState); err != nil {
		t.Fatal(err)
	}
	intentBody := portablesetup.ConfirmedIntent{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: snap.Ledger.Owner, ExpectedGeneration: snap.Ledger.Generation, Action: "install", Stage: "confirmed", SourceRevision: "1.0.0", TreeDigest: digest, HelperDigest: snap.Ledger.Files[observer].SHA256, HelperVersion: "agent-notify-portable-v1", Primary: r.Primary, GlobalConfig: r.GlobalConfig, Targets: []portablesetup.IntentTarget{{Client: "cursor", InstallationID: b.InstallationID, BindingID: b.BindingID, Profile: profile, Units: []string{"agent-notify"}}}}
	published, reservation, err := (portablesetup.Service{}).PublishConfirmedIntent(ctx, intentBody)
	if err != nil {
		t.Fatal(err)
	}
	retryArgs := []string{"--action", "install", "--agents", "cursor", "--hooks", "false", "--agent-notify", "true", "--control-root", control, "--scope-root", profile, "--global-config", r.GlobalConfig, "--client-executable", agent}
	fresh, _, err := parseSetupWizard(retryArgs)
	if err != nil || fresh.CursorAuthority != nil {
		t.Fatal("fresh parser", err)
	}
	noToken := firstClient
	noToken.ProfileAuthority = nil
	uncertain := firstClient
	uncertain.NativeActivationAttempt = "TEST-uncertain"
	uncertain.PendingNativeIntent = &domain.PendingNativeIntent{ProfileAuthority: &token, ProfileNamespace: cfg.StateRoot, AttemptID: uncertain.NativeActivationAttempt, Direction: domain.NativeIntentRegister, Delivery: selected}
	failed := firstClient
	failed.Activation, failed.Verification = domain.ActivationFailed, domain.VerificationFailed
	for _, refusal := range []domain.ClientBinding{noToken, uncertain, failed} {
		firstState.Installations[0].Clients[b.BindingID] = refusal
		if err := store.Save(firstState); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(store.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := composeCursorWizard(ctx, fresh); err == nil {
			t.Fatal("first handoff without original token or with native uncertainty admitted")
		}
		after, err := os.ReadFile(store.Path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("first handoff refusal rewrote state", err)
		}
	}
	firstState.Installations[0].Clients[b.BindingID] = firstClient
	if err := store.Save(firstState); err != nil {
		t.Fatal(err)
	}
	resumed, err := composeCursorWizard(ctx, fresh)
	if err != nil || resumed.CursorAuthority.ObjectID != facts.ObjectID || portable.ExactCommittedBinding(published, b) {
		t.Fatalf("unregistered first handoff: %+v %v", resumed, err)
	}
	pendingSnapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cursorInstalledInputs(ctx, b, pendingSnapshot, cfg.StateRoot); err == nil {
		t.Fatal("unregistered first handoff became eligible for installed delivery")
	}
	adapter, err = cursorinstall.New(nativeconfig.New(), pathpolicy.Policy{}, resumed.CursorAuthority)
	if err != nil {
		t.Fatal(err)
	}
	base, err := portablesetup.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Registry, err = clients.NewRegistry(append(base.All(), adapter)...)
	if err != nil {
		t.Fatal(err)
	}
	handoffs := 0
	interrupted := errors.New("TEST-interrupt-after-AN-handoff-before-native")
	cfg.TrustedLocalPackages, cfg.HelperExecutable, cfg.HelperVersion = true, observer, "agent-notify-portable-v1"
	cfg.OnCommittedBinding = func(ctx context.Context, f uapinstaller.BindingFacts) error {
		if f.BindingID != b.BindingID || f.ProfileAuthority == nil || !reflect.DeepEqual(*f.ProfileAuthority, token) {
			t.Fatal("public handoff lost original authority")
		}
		handoffs++
		if _, err := (portablesetup.Service{}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: published.Generation, Reservation: reservation}); err != nil {
			return err
		}
		return interrupted
	}
	handoffEngine, err := uapinstaller.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := handoffEngine.Prepare(ctx, uapinstaller.Request{Operation: uapinstaller.OpInstall, ClientID: "cursor", InstallationID: b.InstallationID, ClientConfigRoot: profile, ClientExecutable: agent, PackageRoot: pkg})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handoffEngine.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	_ = prepared.Close()
	if !errors.Is(err, interrupted) || handoffs != 1 {
		t.Fatalf("actual public retry handoff: calls=%d %v", handoffs, err)
	}
	afterHandoff, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || !portable.ExactCommittedBinding(afterHandoff.Ledger, b) {
		t.Fatal("AN handoff not committed", err)
	}
	acknowledgedRetry, err := composeCursorWizard(ctx, fresh)
	if err != nil || acknowledgedRetry.PackageRoot != pkg || acknowledgedRetry.CursorAuthority.ObjectID != facts.ObjectID {
		t.Fatalf("same-source acknowledged handoff retry: %+v %v", acknowledgedRetry, err)
	}
	if _, _, err := (portablesetup.Service{}).FinishConfirmedIntent(ctx, intentBody, reservation); err != nil {
		t.Fatal(err)
	}
	firstState.Installations[0].Clients[b.BindingID] = client
	if err := store.Save(firstState); err != nil {
		t.Fatal(err)
	}
	// Acknowledged fixture remains a read-only caller boundary, not native proof.
	r.BootstrapExpectedGeneration = nil
	got, err := composeCursorWizard(ctx, r)
	if err != nil || got.CursorAuthority.ObjectID != "TEST-retained-stop" || got.CursorAuthority.Selector != facts.Selector {
		t.Fatalf("existing token caller: %+v %v", got, err)
	}
	// A is acknowledged. Acquire candidate B, then interrupt immediately
	// after the same confirmed publisher used by the wizard, before UAP B commit.
	archive := filepath.Join(root, "TEST-B.zip")
	built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: agent, OutputRoot: filepath.Join(root, "TEST-B"), Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	archiveBody, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	update := r
	update.Action, update.PackageRoot, update.ReleaseVersion, update.CursorAuthority = setupwizard.ActionUpdate, "", "1.1.0", nil
	update.PackageFetcher = func(_ context.Context, url string) ([]byte, error) {
		fetches++
		if strings.HasSuffix(url, "/checksums.txt") {
			return []byte(built.ArchiveSHA256 + "  " + portableasset.AssetName(runtime.GOOS, runtime.GOARCH) + "\n"), nil
		}
		return archiveBody, nil
	}
	candidate, err := composeCursorWizard(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	intentBody.Action, intentBody.ExpectedGeneration, intentBody.SourceRevision, intentBody.TreeDigest = "update", *candidate.BootstrapExpectedGeneration, candidate.ReleaseVersion, candidate.TreeDigest
	intentBody.Targets[0].DataReceiptID = dataReceipt.DataReceiptID
	published, reservation, err = (portablesetup.Service{}).PublishConfirmedIntent(ctx, intentBody)
	if err != nil {
		t.Fatal(err)
	}
	pending := *reservation
	retryArgs[1] = "update"
	r, _, err = parseSetupWizard(retryArgs)
	if err != nil {
		t.Fatal(err)
	}
	r.PackageFetcher = update.PackageFetcher
	before, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = composeCursorWizard(ctx, r)
	if err != nil || got.InstallationID != b.InstallationID || got.BindingIDs["cursor"] != b.BindingID || got.PackageRoot != candidate.PackageRoot || got.CursorAuthority.ObjectID != facts.ObjectID {
		t.Fatalf("matching caller retry: %+v %v", got, err)
	}
	explicit := r
	explicit.PackageRoot = candidate.PackageRoot
	if _, err := composeCursorWizard(ctx, explicit); err != nil {
		t.Fatal("exact explicit B refused", err)
	}
	wrong := r
	wrong.PackageRoot = pkg
	if _, err := composeCursorWizard(ctx, wrong); err == nil {
		t.Fatal("predecessor A substituted for pending B")
	}
	wrong.PackageRoot = built.Root
	if err := os.WriteFile(filepath.Join(built.Root, "skills", "agent-notifications", "SKILL.md"), []byte("TEST-conflicting-B"), 0600); err != nil {
		t.Fatal(err)
	}
	assertCursorPackageRefusal(t, control, []string{pkg, candidate.PackageRoot, built.Root}, func() {
		if _, err := composeCursorWizard(ctx, wrong); err == nil {
			t.Fatal("wrong same-release B admitted")
		}
	})
	otherExe := filepath.Join(root, "TEST-other-executable")
	if err := os.WriteFile(otherExe, []byte("TEST-different-B"), 0700); err != nil {
		t.Fatal(err)
	}
	wrongB, err := portableasset.Build(portableasset.BuildRequest{Version: "1.1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: otherExe, OutputRoot: filepath.Join(root, "TEST-wrong-B"), Archive: filepath.Join(root, "TEST-wrong-B.zip")})
	if err != nil {
		t.Fatal(err)
	}
	wrong.PackageRoot = wrongB.Archive
	assertCursorPackageRefusal(t, control, []string{pkg, candidate.PackageRoot, wrongB.Archive}, func() {
		if _, err := composeCursorWizard(ctx, wrong); err == nil {
			t.Fatal("wrong same-release archive B admitted")
		}
	})
	if raw, err := os.ReadFile(filepath.Join(built.Root, "skills", "agent-notifications", "SKILL.md")); err != nil || string(raw) != "TEST-conflicting-B" {
		t.Fatal("conflicting B cleaned", err)
	}
	if fetches != 2 {
		t.Fatal("retry fetched", fetches)
	}
	for _, source := range []string{pkg, candidate.PackageRoot} {
		if _, err := os.Stat(source); err != nil {
			t.Fatal("retry cleaned source", err)
		}
	}
	plan, err := setupwizard.Plan(ctx, got)
	if err == nil || !strings.Contains(err.Error(), "selected Cursor portable ProjectArgs composition is unavailable") || plan.Request.DataReceiptIDs["cursor"] != dataReceipt.DataReceiptID {
		t.Fatalf("owner did not resume through unchanged materializer denial: %+v %v", plan, err)
	}
	bad := r
	bad.Action = setupwizard.ActionRepair
	if _, err := composeCursorWizard(ctx, bad); err == nil {
		t.Fatal("conflicting pending action admitted")
	}
	if err := os.Rename(profile, profile+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", archive} {
		replaced := r
		replaced.PackageRoot = source
		assertCursorPackageRefusal(t, control, []string{pkg, candidate.PackageRoot, archive}, func() {
			if _, err := composeCursorWizard(ctx, replaced); err == nil {
				t.Fatal("same-path replacement repaired original token", source)
			}
		})
	}
	if fetches != 2 {
		t.Fatal("token refusal fetched", fetches)
	}
	after, err := os.ReadFile(store.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("caller replaced token/selected packet: %v", err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || snapshot.Ledger.PendingMutation == nil || *snapshot.Ledger.PendingMutation != pending || snapshot.Ledger.Generation != *got.BootstrapExpectedGeneration {
		t.Fatalf("retry replaced pending intent: %+v %v", snapshot, err)
	}
	t.Log("QUALIFIED_TEST_FIRST_HANDOFF=true; QUALIFIED_TEST_PENDING_A_TO_B=true; QUALIFIED_TEST_CALLER_ORIGINAL_TOKEN_AND_MATCHING_RESUME=true; installed/native NOT_RUN")
}

// Missing physical authority must refuse even at the public pre-native handoff.
func TestCursorCallerFirstHandoffMissingToken(t *testing.T) {
	ctx := setupCommandContext(t)
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, ControlRoot: env.control, CursorConfig: env.scope, ScopeRoot: env.scope, GlobalConfig: env.global, PackageRoot: env.pkg}
	reserved, cfg, _, _, err := reserveCursorCaller(ctx, r, snap)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(reserved.CursorAuthority.Selector)
	state := domain.StateFileV2{SchemaVersion: domain.StateSchemaVersion, Installations: []domain.Installation{{InstallationID: reserved.InstallationID, DeclaredName: "agent-notify", Source: domain.SourceBinding{SourceBindingID: "TEST-source", CanonicalSource: env.pkg}, Package: domain.PackageBinding{DeclaredName: "agent-notify"}, Clients: map[string]domain.ClientBinding{reserved.BindingIDs["cursor"]: {ClientBindingID: reserved.BindingIDs["cursor"], ClientID: "cursor", Scope: "user", TargetLocator: filepath.Join(env.scope, "plugins", "local", domain.ComputePhysicalArtifactID("agent-notify", reserved.InstallationID)), PhysicalArtifact: domain.ComputePhysicalArtifactID("agent-notify", reserved.InstallationID), Materialization: domain.MaterializationMaterialized, Activation: domain.ActivationPrepared, Verification: domain.VerificationPackageValid, Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed, DataReceiptID: "TEST-data", NativeProfileRoot: env.scope, ProfileNamespace: cfg.StateRoot}}, DataReceipts: map[string]domain.DataReceipt{"TEST-data": {DataReceiptID: "TEST-data", PhysicalBackend: "TEST-backend", Scope: "user", Locator: root, OwnershipDigest: "sha256:" + strings.Repeat("a", 64), State: domain.DataReceiptOwned}}}}}
	store := statev2.Store{Path: filepath.Join(cfg.StateRoot, "state-v2.json")}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	_, pending, err := (portablesetup.Service{}).PublishConfirmedIntent(ctx, portablesetup.ConfirmedIntent{ControlRoot: env.control, RuntimeRoot: env.runtime, Owner: snap.Ledger.Owner, ExpectedGeneration: snap.Ledger.Generation, Action: "install", Stage: "confirmed", Primary: reserved.Primary, GlobalConfig: reserved.GlobalConfig, Targets: []portablesetup.IntentTarget{{Client: "cursor", InstallationID: reserved.InstallationID, BindingID: reserved.BindingIDs["cursor"], Profile: env.scope, Units: []string{"agent-notify"}}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r.PackageRoot = ""
	r.PackageFetcher = func(context.Context, string) ([]byte, error) {
		fetches++
		return nil, errors.New("TEST-forbidden-fetch")
	}
	if _, _, _, _, err := reserveCursorCaller(ctx, r, snap); err == nil {
		t.Fatal("missing token admitted")
	}
	after, err := os.ReadFile(store.Path)
	current, e := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil || e != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(snap.Ledger, current.Ledger) || current.Ledger.PendingMutation == nil || *current.Ledger.PendingMutation != *pending || fetches != 0 {
		t.Fatal("missing token refusal allocated, fetched, or changed owners", err, e)
	}
}

// Regression: a missing-package caller fetches a release before helper or
// generation refusal, or leaves its new acquisition after binding conflict.
func TestCursorCallerMissingPackageRefusalAndOwnedCleanup(t *testing.T) {
	ctx := setupCommandContext(t)
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(env.root, "TEST-release.zip")
	built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: env.probe, OutputRoot: filepath.Join(env.root, "TEST-release"), Archive: archive})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"cursor"}, ControlRoot: env.control, CursorConfig: env.scope, ScopeRoot: env.scope, GlobalConfig: env.global, ReleaseVersion: "1.43.0", PackageFetcher: func(_ context.Context, url string) ([]byte, error) {
		fetches++
		if strings.HasSuffix(url, "/checksums.txt") {
			return []byte(built.ArchiveSHA256 + "  " + portableasset.AssetName(runtime.GOOS, runtime.GOARCH) + "\n"), nil
		}
		return data, nil
	}}
	wrongGeneration := snap.Ledger.Generation + 1
	for _, bad := range []setupwizard.Request{func() setupwizard.Request { b := r; b.Helper = env.probe; return b }(), func() setupwizard.Request { b := r; b.BootstrapExpectedGeneration = &wrongGeneration; return b }()} {
		if _, _, _, _, err := reserveCursorCaller(ctx, bad, snap); err == nil {
			t.Fatal("refusal admitted")
		}
		if fetches != 0 {
			t.Fatal("release fetched before helper/generation refusal", fetches)
		}
	}
	r.ClientExecutable = env.probe // Existing inert version fixture, never a real agent.
	if _, err := composeCursorWizard(ctx, r); err == nil || !strings.Contains(err.Error(), "version is outside") {
		t.Fatalf("wrong version: %v", err)
	}
	if fetches != 0 {
		t.Fatal("release fetched before refusal", fetches)
	}
	uap := filepath.Join(env.root, "uap")
	if _, err := os.Stat(uap); !os.IsNotExist(err) {
		t.Fatalf("refusal retained directories/files/locks: %v", err)
	}
	r.BindingIDs = map[string]string{"cursor": "TEST-conflicting-binding"}
	if _, _, _, _, err := reserveCursorCaller(ctx, r, snap); err == nil || fetches != 2 {
		t.Fatalf("post-acquisition conflict: fetches=%d %v", fetches, err)
	}
	if _, err := os.Stat(uap); !os.IsNotExist(err) {
		t.Fatalf("composition retained owned staging: %v", err)
	}
	after, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil || after.Ledger.Generation != snap.Ledger.Generation || after.Ledger.PendingMutation != nil {
		t.Fatalf("refusal saved intent: %+v %v", after, err)
	}
	// A conflicting durable action is refused by the real caller even here,
	// where positive physical admission is NOT_RUN.
	path := portablesetup.IntentPath(env.control)
	intent := portablesetup.Intent{Version: 1, SetupIntentID: "TEST-conflicting-intent", Action: "install", Targets: []portablesetup.IntentTarget{{Client: "cursor", InstallationID: "TEST-existing", BindingID: "TEST-binding", Profile: env.scope, Units: []string{"agent-notify"}}}}
	payload, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	pending := installruntime.PendingMutation{ID: intent.SetupIntentID, Owner: "existing-installer", IntentRef: path}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: env.control, RuntimeRoot: env.runtime, Owner: "existing-installer", ConsumerID: "existing", RefreshOnly: true, ExpectedGeneration: &snap.Ledger.Generation, Reservation: &pending, Files: []installruntime.File{{Path: path, Data: payload, Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	r.Action = setupwizard.ActionUninstall
	if _, err := composeCursorWizard(ctx, r); err == nil {
		t.Fatal("conflicting pending action admitted")
	}
	retained, err := os.ReadFile(path)
	after, readErr := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil || readErr != nil || !bytes.Equal(payload, retained) || after.Ledger.Generation != ledger.Generation || after.Ledger.PendingMutation == nil || *after.Ledger.PendingMutation != pending || fetches != 2 {
		t.Fatalf("pending conflict had effects: %+v %v %v", after, err, readErr)
	}
	if _, err := os.Stat(uap); !os.IsNotExist(err) {
		t.Fatalf("pending conflict created state: %v", err)
	}
}

// Regression: a fresh selected caller requires predecessor ownership, computes
// a different selector than public Complete/Filename, or binds the vendor/future
// executable in place of the live ledger-owned observer. No agent is executed.
func TestCursorCallerInertReservation(t *testing.T) {
	ctx := setupCommandContext(t)
	root := setupCommandRoot(t)
	control, runtimeRoot := filepath.Join(root, "control"), filepath.Join(root, "runtime")
	observer := filepath.Join(runtimeRoot, filepath.FromSlash(portable.PlatformPrimary()))
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-existing", Files: []installruntime.File{{Path: observer, Data: []byte("TEST-inert-observer" + installruntime.WriterProtocolMarker), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	pkg, profile := filepath.Join(root, "TEST-package"), filepath.Join(root, "TEST-profile")
	for _, path := range []string{pkg, profile} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), []byte(`{"name":"agent-notify"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req, _, err := parseSetupWizard([]string{"--action", "install", "--agents", "cursor", "--scope-root", profile, "--client-executable", filepath.Join(root, "TEST-vendor"), "--package", pkg, "--control-root", control, "--global-config", filepath.Join(root, "TEST-config.json"), "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	got, cfg, previous, _, err := reserveCursorCaller(ctx, req, snapshot)
	if err != nil || previous || got.CursorAuthority == nil {
		t.Fatalf("fresh: %+v %t %v", got, previous, err)
	}
	fixed := got.CursorAuthority
	physical := domain.ComputePhysicalArtifactID("agent-notify", got.InstallationID)
	binding, err := portablesetup.Complete(portablesetup.Identity{InstallationID: got.InstallationID, ComponentID: snapshot.Ledger.ID, Owner: snapshot.Ledger.Owner, ScopeRoot: profile, ControlRoot: control, GlobalConfig: got.GlobalConfig, RuntimeRoot: runtimeRoot, Primary: got.Primary}, portable.Cursor, "cursor", string(domain.ScopeUser), filepath.Join(profile, "plugins", "local", physical), filepath.Join(cfg.PluginDataBase, physical))
	if err != nil {
		t.Fatal(err)
	}
	name, err := binding.Filename()
	if err != nil || fixed.Selector != filepath.Join(binding.DataRoot, name) || got.BindingIDs["cursor"] != binding.BindingID || fixed.ObjectID != "cursor-user-stop-v1" || fixed.Executable != observer || fixed.ExecutableDigest != "sha256:"+snapshot.Ledger.Files[observer].SHA256 || got.ClientExecutables["cursor"] == observer {
		t.Fatalf("reservation: %+v %+v %v", got, binding, err)
	}
	for _, helper := range []string{req.ClientExecutable, filepath.Join(runtimeRoot, "future-helper")} {
		bad := got
		bad.Helper = helper
		if _, _, _, _, err := reserveCursorCaller(ctx, bad, snapshot); err == nil {
			t.Fatal("unowned observer admitted", helper)
		}
	}
	if err := os.WriteFile(observer, []byte("TEST-replaced-observer"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := reserveCursorCaller(ctx, got, snapshot); err == nil {
		t.Fatal("changed observer admitted")
	}
	if _, err := os.Stat(filepath.Dir(cfg.StateRoot)); !os.IsNotExist(err) {
		t.Fatalf("inert reservation wrote UAP state: %v", err)
	}
}

// Regression: a direct Cursor request reaches installer mutation using only a
// vendor path, or omission/explicit opt-out tries to allocate selected authority.
func TestCursorCallerRefusalAndOptOut(t *testing.T) {
	root := setupCommandRoot(t)
	flags := []string{"--action", "install", "--agents", "cursor", "--scope-root", root, "--client-executable", filepath.Join(root, "TEST-vendor"), "--control-root", filepath.Join(root, "control"), "--yes", "--json"}
	var out bytes.Buffer
	if code := executeSetupWizardWith(setupCommandContext(t), flags, &out, io.Discard, strings.NewReader(""), false); code != 1 || !strings.Contains(out.String(), "cursor_composition_required") {
		t.Fatalf("refusal: %d %s", code, out.String())
	}
	req, _, err := parseSetupWizard(flags)
	if err != nil {
		t.Fatal(err)
	}
	off := false
	req.AgentNotify = &off
	got, err := composeCursorWizard(setupCommandContext(t), req)
	if err != nil || got.CursorAuthority != nil {
		t.Fatalf("opt-out: %+v %v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("refusal wrote files: %v %v", entries, err)
	}
}

// Directory timestamps observe even a transient staging/snapshot creation and
// removal. Durable bytes alone would miss that rejected admission performed work.
func assertCursorPackageRefusal(t *testing.T, control string, sources []string, refuse func()) {
	t.Helper()
	uap := filepath.Join(filepath.Dir(control), "uap")
	for _, dir := range []string{filepath.Join(uap, "state", "tmp"), filepath.Join(uap, "acquired-source")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1234567890, 0)
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() map[string]string {
		result := map[string]string{}
		for _, root := range append([]string{control, uap}, sources...) {
			err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				value := fmt.Sprint(info.Mode())
				if info.IsDir() {
					value += fmt.Sprint(info.ModTime().UnixNano())
				} else if info.Mode().IsRegular() {
					body, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					value += string(body)
				} else if info.Mode()&os.ModeSymlink != 0 {
					target, err := os.Readlink(path)
					if err != nil {
						return err
					}
					value += target
				}
				result[path] = value
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := capture()
	refuse()
	after := capture()
	if !reflect.DeepEqual(before, after) {
		for path, value := range after {
			if before[path] != value {
				t.Errorf("refusal mutated package/state or transient staging directory: %s", path)
			}
		}
		for path := range before {
			if _, ok := after[path]; !ok {
				t.Errorf("refusal cleaned source/state: %s", path)
			}
		}
	}
}
