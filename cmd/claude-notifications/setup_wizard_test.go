//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
)

func TestSetupWizardHelpAndYesRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizard(ctx, []string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "setup-notifications wizard") || !strings.Contains(out.String(), "units") || !strings.Contains(out.String(), "stderr") || !strings.Contains(out.String(), "Omit on inspect to report both clients") || !strings.Contains(out.String(), "invalid for mutation") || !strings.Contains(out.String(), "update, or repair") || !strings.Contains(out.String(), "omit on update/repair to keep live units") {
		t.Fatalf("help: %d %s", code, out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex"}, &out, io.Discard, strings.NewReader(""), false); code != 2 {
		t.Fatalf("missing yes: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex", "--yes", "--control-root", root, "--json"}, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("missing runtime: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "managed_runtime_required") {
		t.Fatalf("runtime reason: %s", out.String())
	}
}

func TestSetupWizardTTYSelectsAndCancels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "install"}, &out, io.Discard, strings.NewReader("\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("tty cancel: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--control-root", root}, &out, io.Discard, strings.NewReader("2\n3\ny\n"), true); code != 1 {
		t.Fatalf("tty confirm still needs runtime: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "managed_runtime_required") || !strings.Contains(out.String(), "retry:") {
		t.Fatalf("tty retry: %s", out.String())
	}
}

func TestSetupWizardTTYOmitsActionDefaultsInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--control-root", root}, &out, io.Discard, strings.NewReader("2\n3\ny\n"), true); code != 1 {
		t.Fatalf("omitted action: %d %s", code, out.String())
	}
	if strings.Contains(out.String(), "Existing agent-notify") {
		t.Fatalf("new machine prompted existing action: %s", out.String())
	}
	if !strings.Contains(out.String(), "Units:") || !strings.Contains(out.String(), "Plan: action=install") || !strings.Contains(out.String(), "managed_runtime_required") {
		t.Fatalf("omitted action flow: %s", out.String())
	}
}

func TestSetupWizardJSONInspectIsOneObject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := executeSetupWizardWith(ctx, []string{"--action", "inspect", "--agents", "codex", "--control-root", control, "--json"}, &out, &stderr, strings.NewReader("y\n"), true)
	if code != 0 {
		t.Fatalf("inspect json: %d stdout=%s stderr=%s", code, out.String(), stderr.String())
	}
	if strings.Contains(out.String(), "phase ") {
		t.Fatalf("json stdout included progress: %s", out.String())
	}
	var result setupwizard.Result
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&result); err != nil {
		t.Fatalf("stdout json: %v %s", err, out.String())
	}
	if result.Action != "inspect" || result.Outcome == "" {
		t.Fatalf("inspect result: %+v", result)
	}
	if dec.More() {
		t.Fatalf("stdout contained more than one JSON value: %s", out.String())
	}
}

func TestSetupWizardJSONInspectOmitsAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := executeSetupWizardWith(ctx, []string{"--action", "inspect", "--control-root", control, "--json"}, &out, &stderr, strings.NewReader(""), false)
	if code != 0 {
		t.Fatalf("omitted-agents inspect: %d stdout=%s stderr=%s", code, out.String(), stderr.String())
	}
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout json: %v %s", err, out.String())
	}
	if result.Action != "inspect" || result.Outcome != "completed" || result.Reason == "empty_selection" {
		t.Fatalf("omitted-agents inspect result: %+v", result)
	}
	saw := map[string]bool{}
	for _, target := range result.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = true
		}
	}
	if !saw["claude"] || !saw["codex"] {
		t.Fatalf("omitted-agents inspect missed a client: %+v", result.Targets)
	}
}

func TestSetupWizardJSONDoesNotPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex", "--json"}, &out, io.Discard, strings.NewReader("y\n"), true); code != 2 {
		t.Fatalf("json prompt: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "noninteractive_requires_yes") {
		t.Fatalf("json must not consume TTY confirm: %s", out.String())
	}
	if strings.Contains(out.String(), "phase ") {
		t.Fatalf("json stdout included progress: %s", out.String())
	}
}

func TestSetupWizardJSONMutationRequiresYes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, action := range []string{"update", "repair", "uninstall"} {
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, []string{"--action", action, "--agents", "codex", "--json"}, &out, io.Discard, strings.NewReader("y\n"), true)
		if code != 2 {
			t.Fatalf("%s json prompt: %d %s", action, code, out.String())
		}
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s json: %v %s", action, err, out.String())
		}
		if result.Action != action || result.Outcome != "invalid" || result.Reason != "noninteractive_requires_yes" {
			t.Fatalf("%s result: %+v", action, result)
		}
	}
}

func TestSetupWizardTTYShowsDiscoverCapabilities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	var out bytes.Buffer
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--control-root", root}, &out, io.Discard, strings.NewReader("\n"), true); code != 0 {
		t.Fatalf("tty discover cancel: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Claude Code (executable not found)") || !strings.Contains(out.String(), "Codex (executable present)") {
		t.Fatalf("discover labels: %s", out.String())
	}
}

func TestSetupWizardUpdateAndRepairRequireManagedRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	for _, action := range []string{"update", "repair"} {
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, []string{"--action", action, "--agents", "codex", "--yes", "--control-root", root, "--json"}, &out, io.Discard, strings.NewReader(""), false)
		if code != 1 {
			t.Fatalf("%s exit: %d %s", action, code, out.String())
		}
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s json: %v %s", action, err, out.String())
		}
		if result.Action != action || result.Outcome != "incomplete" || result.Reason != "managed_runtime_required" {
			t.Fatalf("%s result: %+v", action, result)
		}
	}
}

func TestParseSetupWizardResolvesEnvOnce(t *testing.T) {
	envCodex := filepath.Join(t.TempDir(), "env-codex")
	envClaude := filepath.Join(t.TempDir(), "env-claude")
	explicit := filepath.Join(t.TempDir(), "explicit")
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("CODEX_HOME", envCodex)
	t.Setenv("CLAUDE_CONFIG_DIR", envClaude)
	t.Setenv("HOME", home)
	req, _, err := parseSetupWizard([]string{"--action", "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if req.CodexHome != envCodex || req.ClaudeConfig != envClaude {
		t.Fatalf("env defaults: %+v", req)
	}
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "later"))
	if req.CodexHome != envCodex {
		t.Fatal("parsed request reread env")
	}
	flagged, _, err := parseSetupWizard([]string{"--action", "inspect", "--codex-home", explicit})
	if err != nil {
		t.Fatal(err)
	}
	if flagged.CodexHome != explicit {
		t.Fatalf("explicit lost: %+v", flagged)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	empty, _, err := parseSetupWizard([]string{"--action", "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.CodexHome != "" || empty.ClaudeConfig != "" {
		t.Fatalf("HOME used as profile fallback: %+v", empty)
	}
}

func TestParseSetupWizardRejectsRelativeEnvProfile(t *testing.T) {
	t.Setenv("CODEX_HOME", "relative-codex")
	if _, _, err := parseSetupWizard([]string{"--action", "inspect"}); err == nil {
		t.Fatal("relative CODEX_HOME accepted")
	}
}

func TestQuoteWizardArgsQuotesPathsWithSpaces(t *testing.T) {
	got := quoteWizardArgs([]string{"setup-notifications", "wizard", "--codex-home", `/tmp/codex home`, "--yes"})
	if len(got) != 5 || got[3] != `"/tmp/codex home"` || got[4] != "--yes" {
		t.Fatalf("quoted: %#v", got)
	}
}

func TestReportAgentNotifySetupFailureQuotesCodexHome(t *testing.T) {
	var buf bytes.Buffer
	reportAgentNotifySetupFailure(&buf, "codex", []string{"--codex-home", `/tmp/codex home`, "--navigation", "none"})
	got := buf.String()
	if !strings.Contains(got, `Retry:`) || !strings.Contains(got, `"/tmp/codex home"`) {
		t.Fatalf("unquoted retry: %s", got)
	}
	if strings.Contains(got, "configure --provider codex --codex-home /tmp/codex home --navigation") {
		t.Fatal("space path split in retry", got)
	}
}

func TestSetupWizardJSONLifecycleE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	global := filepath.Join(root, "global", "config.json")
	probe := buildWizardProbe(t)
	pkg := filepath.Join(root, "package")
	writeWizardPackage(t, pkg, probe)
	codexHome := filepath.Join(root, "codex-profile")
	scope := filepath.Join(root, "scope")
	for _, dir := range []string{filepath.Dir(global), codexHome, scope} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: body, Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	flags := func(action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "false",
			"--package", pkg, "--control-root", control, "--runtime-root", runtime,
			"--global-config", global, "--codex-home", codexHome,
			"--client-executable", probe, "--helper", probe, "--scope-root", scope,
		}
		return append(args, extra...)
	}
	decode := func(t *testing.T, out bytes.Buffer) setupwizard.Result {
		t.Helper()
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("json: %v %s", err, out.String())
		}
		return result
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags("install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	installed := decode(t, out)
	if installed.Outcome != "completed" || installed.InstallationID == "" {
		t.Fatalf("install result: %+v", installed)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect: %d %s", code, out.String())
	}
	view := decode(t, out)
	if view.Outcome != "completed" {
		t.Fatalf("inspect result: %+v", view)
	}
	var notifyInstalled bool
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			notifyInstalled = true
		}
	}
	if !notifyInstalled {
		t.Fatalf("inspect missed notify: %+v", view.Targets)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("update"), &out, io.Discard, strings.NewReader("n\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("update cancel: %d %s", code, out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("update", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("update: %d %s", code, out.String())
	}
	updated := decode(t, out)
	if updated.Outcome != "completed" && updated.Outcome != "unchanged" {
		t.Fatalf("update result: %+v", updated)
	}
	if updated.InstallationID != installed.InstallationID {
		t.Fatalf("update changed installation: %s vs %s", installed.InstallationID, updated.InstallationID)
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(root, "uap", "state")})
	if err != nil {
		t.Fatal(err)
	}
	uapView, err := eng.Inspect(ctx)
	if err != nil || len(uapView.Installations) != 1 || len(uapView.Installations[0].Bindings) == 0 {
		t.Fatalf("uap inspect: %+v %v", uapView, err)
	}
	target := uapView.Installations[0].Bindings[0].TargetPath
	if target == "" {
		t.Fatal("missing live target")
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("repair", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("repair: %d %s", code, out.String())
	}
	repaired := decode(t, out)
	if repaired.Outcome != "completed" {
		t.Fatalf("repair result: %+v", repaired)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore target: %v", err)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("uninstall", "--yes", "--json", "--external-uninstalled"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall: %d %s", code, out.String())
	}
	removed := decode(t, out)
	if removed.Outcome != "completed" {
		t.Fatalf("uninstall result: %+v", removed)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after uninstall: %d %s", code, out.String())
	}
	after := decode(t, out)
	for _, target := range after.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("notify survived uninstall: %+v", after.Targets)
		}
	}
}

func TestSetupWizardBothClientsRemoveOneE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := setupCommandRoot(t)
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	global := filepath.Join(root, "global", "config.json")
	probe := buildWizardProbe(t)
	pkg := filepath.Join(root, "package")
	writeWizardPackage(t, pkg, probe)
	codexHome := filepath.Join(root, "codex-profile")
	claudeConfig := filepath.Join(root, "claude-profile")
	scope := filepath.Join(root, "scope")
	for _, dir := range []string{filepath.Dir(global), codexHome, claudeConfig, scope} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: body, Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	shared := []string{
		"--hooks", "false", "--package", pkg, "--control-root", control, "--runtime-root", runtime,
		"--global-config", global, "--codex-home", codexHome, "--claude-config", claudeConfig,
		"--claude-executable", probe, "--codex-executable", probe, "--helper", probe, "--scope-root", scope,
	}
	decode := func(t *testing.T, out bytes.Buffer) setupwizard.Result {
		t.Helper()
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("json: %v %s", err, out.String())
		}
		return result
	}
	notify := func(result setupwizard.Result) map[string]string {
		out := map[string]string{}
		for _, target := range result.Targets {
			if target.Unit == "agent-notify" {
				out[target.Client] = target.Outcome
			}
		}
		return out
	}
	var out bytes.Buffer
	claudeFlags := append([]string{"--action", "install", "--agents", "claude", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, claudeFlags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install claude: %d %s", code, out.String())
	}
	claude := decode(t, out)
	if claude.Outcome != "completed" || claude.InstallationID == "" {
		t.Fatalf("install claude result: %+v", claude)
	}
	out.Reset()
	codexFlags := append([]string{"--action", "install", "--agents", "codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, codexFlags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("add codex: %d %s", code, out.String())
	}
	added := decode(t, out)
	if added.Outcome != "completed" || added.InstallationID != claude.InstallationID {
		t.Fatalf("add codex result: %+v", added)
	}
	out.Reset()
	inspectFlags := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspectFlags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect both: %d %s", code, out.String())
	}
	both := notify(decode(t, out))
	if both["claude"] != "installed" || both["codex"] != "installed" {
		t.Fatalf("inspect both: %+v", both)
	}
	out.Reset()
	removeFlags := append([]string{"--action", "uninstall", "--agents", "claude", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, removeFlags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall claude: %d %s", code, out.String())
	}
	removed := decode(t, out)
	if removed.Outcome != "completed" && removed.Outcome != "unchanged" {
		t.Fatalf("uninstall claude result: %+v", removed)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, inspectFlags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after remove: %d %s", code, out.String())
	}
	after := notify(decode(t, out))
	if after["codex"] != "installed" {
		t.Fatalf("codex lost after claude remove: %+v", after)
	}
	if after["claude"] == "installed" {
		t.Fatalf("claude survived remove: %+v", after)
	}
}

func TestSetupWizardHooksOnlyDoesNotOpenUAP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	root := setupCommandRoot(t)
	control := filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	global := filepath.Join(root, "global", "config.json")
	bundle := writeWizardPluginBundle(t)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	codexHome := filepath.Join(envHome, "codex-home")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(global), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(runtime, "primary"), Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	flags := func(action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "true", "--agent-notify", "false",
			"--plugin-root", bundle, "--control-root", control, "--runtime-root", runtime,
			"--global-config", global, "--codex-home", codexHome,
		}
		return append(args, extra...)
	}
	decode := func(t *testing.T, out bytes.Buffer) setupwizard.Result {
		t.Helper()
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("json: %v %s", err, out.String())
		}
		return result
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags("install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("hooks install: %d %s", code, out.String())
	}
	installed := decode(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("hooks install result: %+v", installed)
	}
	hooks := filepath.Join(codexHome, "hooks.json")
	data, err := os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks.json: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "uap", "state", "state-v2.json")); !os.IsNotExist(err) {
		t.Fatal("hooks-only CLI install opened UAP state")
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("hooks inspect: %d %s", code, out.String())
	}
	view := decode(t, out)
	var hooksInstalled, notifyInstalled bool
	for _, target := range view.Targets {
		if target.Unit == "hooks" && target.Outcome == "installed" {
			hooksInstalled = true
		}
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			notifyInstalled = true
		}
	}
	if !hooksInstalled || notifyInstalled {
		t.Fatalf("inspect after hooks-only: %+v", view.Targets)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("uninstall", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("hooks uninstall: %d %s", code, out.String())
	}
	removed := decode(t, out)
	if removed.Outcome != "completed" {
		t.Fatalf("hooks uninstall result: %+v", removed)
	}
	data, err = os.ReadFile(hooks)
	if err == nil && strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks survived uninstall: %s", data)
	}
}

func TestSetupWizardReinstallRetainsInstallationE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	flags := func(action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "false",
			"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
			"--global-config", env.global, "--codex-home", env.codexHome,
			"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		}
		return append(args, extra...)
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags("install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" || installed.InstallationID == "" {
		t.Fatalf("install result: %+v", installed)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("uninstall", "--yes", "--json", "--external-uninstalled"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("uninstall result: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("install", "--yes", "--json", "--installation-id", installed.InstallationID), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("reinstall: %d %s", code, out.String())
	}
	reinstalled := decodeWizardJSON(t, out)
	if reinstalled.Outcome != "completed" || reinstalled.InstallationID != installed.InstallationID {
		t.Fatalf("reinstall lost installation: %+v", reinstalled)
	}
}

func TestSetupWizardMixedPerClientOptOutsE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--claude-agent-notify", "false", "--codex-agent-notify", "true",
		"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("mixed install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("mixed install result: %+v", got)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("mixed inspect: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	var claudeMCP, codexMCP string
	for _, target := range view.Targets {
		if target.Unit != "agent-notify" {
			continue
		}
		switch target.Client {
		case "claude":
			claudeMCP = target.Outcome
		case "codex":
			codexMCP = target.Outcome
		}
	}
	if claudeMCP == "installed" || codexMCP != "installed" {
		t.Fatalf("mixed opt-outs: %+v", view.Targets)
	}
}

func TestSetupWizardSpaceContainingRootsE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, true)
	flags := func(action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "false",
			"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
			"--global-config", env.global, "--codex-home", env.codexHome,
			"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		}
		return append(args, extra...)
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags("install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("space install: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("space install result: %+v", installed)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("space inspect: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" && target.Profile == env.codexHome {
			found = true
		}
	}
	if !found {
		t.Fatalf("space inspect missed profile: %+v", view.Targets)
	}
}

func TestSetupWizardDirectMCPHandoffDoesNotRestoreE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	mcpConfig := filepath.Join(env.root, "client", "config")
	if err := os.MkdirAll(filepath.Dir(mcpConfig), 0700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(env.runtime, "primary")
	if _, err := clientsetup.Apply(ctx, clientsetup.Request{
		ControlRoot: env.control, RuntimeRoot: env.runtime, Command: primary, ConfigPath: mcpConfig,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
	}); err != nil {
		t.Fatal(err)
	}
	flags := func(action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "false",
			"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
			"--global-config", env.global, "--codex-home", env.codexHome,
			"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
			"--mcp-config", mcpConfig,
		}
		return append(args, extra...)
	}
	unit := func(result setupwizard.Result, name string) string {
		for _, target := range result.Targets {
			if target.Unit == name {
				return target.Outcome
			}
		}
		return ""
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags("install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("handoff install: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("handoff install result: %+v", installed)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("handoff inspect: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	if unit(view, "agent-notify") != "installed" || unit(view, "direct-mcp") != "absent" {
		t.Fatalf("handoff inspect: %+v", view.Targets)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("uninstall", "--yes", "--json", "--external-uninstalled"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("handoff uninstall: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("handoff uninstall result: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after uninstall: %d %s", code, out.String())
	}
	after := decodeWizardJSON(t, out)
	if unit(after, "agent-notify") == "installed" {
		t.Fatalf("portable survived uninstall: %+v", after.Targets)
	}
	if unit(after, "direct-mcp") == "installed" {
		t.Fatalf("uninstall restored direct MCP: %+v", after.Targets)
	}
}

func buildWizardProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(src, []byte(`package main
import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)
func main() {
	if strings.Join(os.Args[1:], " ") == "plugin list --json" {
		root := os.Getenv("CLAUDE_CONFIG_DIR")
		listed := []map[string]any{}
		if root != "" {
			entries, err := os.ReadDir(filepath.Join(root, "skills"))
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() || entry.Name()[0] == '.' {
						continue
					}
					path := filepath.Join(root, "skills", entry.Name())
					body, readErr := os.ReadFile(filepath.Join(path, ".claude-plugin", "plugin.json"))
					if readErr != nil {
						continue
					}
					var manifest map[string]any
					if json.Unmarshal(body, &manifest) != nil {
						continue
					}
					name, _ := manifest["name"].(string)
					listed = append(listed, map[string]any{
						"id": name + "@skills-dir", "version": manifest["version"], "scope": "user",
						"enabled": true, "installPath": path,
					})
				}
			}
		}
		json.NewEncoder(os.Stdout).Encode(listed)
		return
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true})
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "probe")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %s %v", body, err)
	}
	return out
}

func writeWizardPackage(t *testing.T, root, probe string) {
	t.Helper()
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"plugin.json":                  []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.0"}`),
		"mcp.json":                     []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"agent-notify":{"type":"stdio","command":"./bin/probe","args":[],"env":{}}}}`),
		"skills/agent-notify/SKILL.md": []byte("---\nname: agent-notify\ndescription: Wizard CLI e2e\n---\n"),
		"bin/probe":                    body,
	}
	for rel, data := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if rel == "bin/probe" {
			mode = 0700
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func writeWizardPluginBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"bin/codex-hook-wrapper.sh":  "#!/bin/sh\nexit 0\n",
		"bin/codex-hook-wrapper.cmd": "@echo off\r\nexit /b 0\r\n",
		"bin/hook-wrapper.sh":        "#!/bin/sh\nexit 0\n",
		"sounds/task-complete.mp3":   "not-really-audio",
		"config/config.json":         `{"notifications":{}}`,
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func decodeWizardJSON(t *testing.T, out bytes.Buffer) setupwizard.Result {
	t.Helper()
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v %s", err, out.String())
	}
	return result
}

type wizardCLIEnv struct {
	root, control, runtime, global, probe, pkg, codexHome, claudeConfig, scope string
}

func newWizardCLIEnv(t *testing.T, ctx context.Context, spaced bool) wizardCLIEnv {
	t.Helper()
	root := setupCommandRoot(t)
	if spaced {
		root = filepath.Join(root, "install root")
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	codexName, claudeName := "codex-profile", "claude-profile"
	if spaced {
		codexName, claudeName = "codex home", "claude config"
	}
	env := wizardCLIEnv{
		root:         root,
		control:      filepath.Join(root, "control"),
		runtime:      filepath.Join(root, "runtime"),
		global:       filepath.Join(root, "global", "config.json"),
		probe:        buildWizardProbe(t),
		pkg:          filepath.Join(root, "package"),
		codexHome:    filepath.Join(root, codexName),
		claudeConfig: filepath.Join(root, claudeName),
		scope:        filepath.Join(root, "scope"),
	}
	writeWizardPackage(t, env.pkg, env.probe)
	for _, dir := range []string{filepath.Dir(env.global), env.codexHome, env.claudeConfig, env.scope} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(env.probe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: env.control, RuntimeRoot: env.runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(env.runtime, "primary"), Data: body, Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	return env
}
