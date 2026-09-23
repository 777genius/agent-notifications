//go:build linux || darwin

package main

import (
	"bytes"
	"context"
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
			if err := os.WriteFile(filepath.Join(env.pkg, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: Updated bootstrap fixture\n---\n"), 0600); err != nil {
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
	if err := os.WriteFile(filepath.Join(env.pkg, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: Retained bootstrap update\n---\n"), 0600); err != nil {
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
