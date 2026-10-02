//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
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
