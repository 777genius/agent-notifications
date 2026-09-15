//go:build linux || darwin

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
)

func TestSetupWizardHelpAndYesRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizard(ctx, []string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "setup-notifications wizard") || !strings.Contains(out.String(), "units") || !strings.Contains(out.String(), "stderr") || !strings.Contains(out.String(), "Omit on inspect to report both clients") || !strings.Contains(out.String(), "invalid for mutation") || !strings.Contains(out.String(), "update, or repair") || !strings.Contains(out.String(), "omit on update/repair to keep live units") || !strings.Contains(out.String(), "Inspect exit 0") {
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
	if req.CodexHome != "" || req.ClaudeConfig != "" {
		t.Fatalf("parse treated env as explicit: %+v", req)
	}
	if req.EnvCodexHome != envCodex || req.EnvClaudeConfig != envClaude {
		t.Fatalf("env snapshot: %+v", req)
	}
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "later"))
	if req.EnvCodexHome != envCodex {
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
	if empty.CodexHome != "" || empty.ClaudeConfig != "" || empty.EnvCodexHome != "" || empty.EnvClaudeConfig != "" {
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
	kinds := map[string]bool{}
	for _, next := range installed.NextActions {
		kinds[next.Kind] = true
		if next.Kind == "test-notification" && (len(next.Command) == 0 || next.Command[0] != "notify" || next.Reason != "delivery_not_verified") {
			t.Fatalf("test action: %+v", next)
		}
		if next.Kind == "request-permission" && (len(next.Command) < 2 || next.Command[1] != "request-permission") {
			t.Fatalf("permission action: %+v", next)
		}
	}
	if !kinds["test-notification"] || !kinds["request-permission"] || !kinds["restart-client"] {
		t.Fatalf("install next actions: %+v", installed.NextActions)
	}
	if len(installed.Readiness) == 0 || installed.Readiness[0].Delivery != "not_verified" {
		t.Fatalf("delivery treated as proven: %+v", installed.Readiness)
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
	for _, next := range removed.NextActions {
		if next.Kind == "test-notification" || next.Kind == "request-permission" {
			t.Fatalf("uninstall offered setup action: %+v", removed.NextActions)
		}
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
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(env.root, "uap", "state")})
	if err != nil {
		t.Fatal(err)
	}
	uapView, err := eng.Inspect(ctx)
	if err != nil || len(uapView.Installations) != 1 || len(uapView.Installations[0].Bindings) == 0 {
		t.Fatalf("uap inspect: %+v %v", uapView, err)
	}
	dataRoot := uapView.Installations[0].Bindings[0].DataRoot
	if dataRoot == "" {
		t.Fatal("missing plugin data root")
	}
	sentinel := filepath.Join(dataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
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
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "retain\n" {
		t.Fatalf("reinstall lost plugin data: %s %v", got, err)
	}
}

func TestSetupWizardReinstallPreservesNotificationOptOutsE2E(t *testing.T) {
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
	optOut := `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`
	if err := os.WriteFile(env.global, []byte(optOut), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: env.control, RuntimeRoot: env.runtime, Owner: "existing-installer", ConsumerID: "existing",
		RefreshOnly: true, PolicyEnabled: &disabled, ExpectedGeneration: &snap.Ledger.Generation,
	}); err != nil {
		t.Fatalf("disable policy: %v", err)
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
	policy, err := installruntime.ReadUserPolicy(env.control)
	if err != nil || policy.Enabled {
		t.Fatalf("reinstall re-enabled policy: %+v %v", policy, err)
	}
	body, err := os.ReadFile(env.global)
	if err != nil || !strings.Contains(string(body), `"enabled":false`) || !strings.Contains(string(body), `"sound":false`) || !strings.Contains(string(body), `"clickToFocus":false`) || !strings.Contains(string(body), `"keep":true`) {
		t.Fatalf("reinstall lost global opt-outs: %s %v", body, err)
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

func TestSetupWizardTTYMixedAddKeepsPerClientUnits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "claude,codex", "--package", env.pkg, "--control-root", env.control,
		"--runtime-root", env.runtime, "--global-config", env.global,
		"--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe,
		"--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{
		"--action", "install", "--hooks", "false", "--claude-agent-notify", "false",
		"--codex-agent-notify", "true", "--yes", "--json",
	}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("mixed install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("mixed install result: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("2\n1\nn\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("mixed tty keep: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "differ per client") || !strings.Contains(out.String(), "claude: hooks=false agent-notify=false") || !strings.Contains(out.String(), "codex: hooks=false agent-notify=true") {
		t.Fatalf("mixed tty hid differences: %s", out.String())
	}
	if strings.Contains(out.String(), "Units: 1) Hooks") || strings.Contains(out.String(), "hooks=on") {
		t.Fatalf("mixed tty collapsed omission: %s", out.String())
	}
	if !strings.Contains(out.String(), "hooks=per-client") || !strings.Contains(out.String(), "claude-agent-notify=false") || !strings.Contains(out.String(), "codex-agent-notify=true") {
		t.Fatalf("mixed tty plan: %s", out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("2\n1\ny\n"), true); code != 0 || !strings.Contains(out.String(), "completed") {
		t.Fatalf("mixed tty keep run: %d %s", code, out.String())
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after keep: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	var claudeMCP, codexMCP string
	hooksInstalled := false
	for _, target := range view.Targets {
		switch {
		case target.Unit == "agent-notify" && target.Client == "claude":
			claudeMCP = target.Outcome
		case target.Unit == "agent-notify" && target.Client == "codex":
			codexMCP = target.Outcome
		case target.Unit == "hooks" && target.Outcome == "installed":
			hooksInstalled = true
		}
	}
	if claudeMCP == "installed" || codexMCP != "installed" || hooksInstalled {
		t.Fatalf("keep mutated mixed units: %+v", view.Targets)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("3\n3\nn\n"), true); !strings.Contains(out.String(), "differ per client") || strings.Contains(out.String(), "Units: 1) Hooks") {
		t.Fatalf("mixed uninstall tty: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "hooks=false") || !strings.Contains(out.String(), "agent-notify=true") {
		t.Fatalf("mixed uninstall notify-only plan: %s", out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("3\n1\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("mixed uninstall keep: %d %s", code, out.String())
	}
	if strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("mixed uninstall keep asked confirm: %s", out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after uninstall keep: %d %s", code, out.String())
	}
	view = decodeWizardJSON(t, out)
	claudeMCP, codexMCP, hooksInstalled = "", "", false
	for _, target := range view.Targets {
		switch {
		case target.Unit == "agent-notify" && target.Client == "claude":
			claudeMCP = target.Outcome
		case target.Unit == "agent-notify" && target.Client == "codex":
			codexMCP = target.Outcome
		case target.Unit == "hooks" && target.Outcome == "installed":
			hooksInstalled = true
		}
	}
	if claudeMCP == "installed" || codexMCP != "installed" || hooksInstalled {
		t.Fatalf("uninstall keep mutated mixed units: %+v", view.Targets)
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

func TestSetupWizardOmitsPackageInspectRepairUninstallE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "codex", "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	notifyInstalled := func(result setupwizard.Result) bool {
		for _, target := range result.Targets {
			if target.Unit == "agent-notify" && target.Outcome == "installed" {
				return true
			}
		}
		return false
	}
	var out, stderr bytes.Buffer
	install := append([]string{"--action", "install", "--hooks", "false", "--package", env.pkg, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" || installed.InstallationID == "" {
		t.Fatalf("install result: %+v", installed)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, &stderr, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect without package: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	if view.Outcome != "completed" || !notifyInstalled(view) {
		t.Fatalf("inspect without package: %+v", view)
	}
	if strings.Contains(stderr.String(), "phase prepare") {
		t.Fatalf("inspect acquired a package: %s", stderr.String())
	}
	for _, next := range view.NextActions {
		if next.Kind == "request-permission" {
			t.Fatalf("inspect opened permission action: %+v", view.NextActions)
		}
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(env.root, "uap", "state")})
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
	repair := append([]string{"--action", "repair", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("repair without package: %d %s", code, out.String())
	}
	repaired := decodeWizardJSON(t, out)
	if repaired.Outcome != "completed" {
		t.Fatalf("repair without package: %+v", repaired)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore recorded source: %v", err)
	}
	if err := os.RemoveAll(env.pkg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.pkg, []byte("not-a-package"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := executeSetupWizardWith(ctx, inspect, &out, &stderr, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after poisoned package: %d %s", code, out.String())
	}
	if !notifyInstalled(decodeWizardJSON(t, out)) {
		t.Fatalf("inspect after poisoned package: %s", out.String())
	}
	if strings.Contains(stderr.String(), "phase prepare") {
		t.Fatalf("inspect opened poisoned package: %s", stderr.String())
	}
	out.Reset()
	uninstall := append([]string{"--action", "uninstall", "--yes", "--json", "--external-uninstalled"}, shared...)
	if code := executeSetupWizardWith(ctx, uninstall, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall without package: %d %s", code, out.String())
	}
	removed := decodeWizardJSON(t, out)
	if removed.Outcome != "completed" {
		t.Fatalf("uninstall without package: %+v", removed)
	}
	for _, next := range removed.NextActions {
		if next.Kind == "test-notification" || next.Kind == "request-permission" {
			t.Fatalf("uninstall offered setup action: %+v", removed.NextActions)
		}
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after uninstall: %d %s", code, out.String())
	}
	if notifyInstalled(decodeWizardJSON(t, out)) {
		t.Fatalf("notify survived uninstall without package: %s", out.String())
	}
}

func TestSetupWizardTTYExistingOmitsActionE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "codex", "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--hooks", "false", "--package", env.pkg, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install result: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("1\n"), true); code != 0 {
		t.Fatalf("existing inspect: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Existing agent-notify") {
		t.Fatalf("existing action not offered: %s", out.String())
	}
	if !strings.Contains(out.String(), "codex agent-notify: installed") {
		t.Fatalf("existing inspect missed notify: %s", out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("3\n2\nn\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("existing uninstall cancel: %d %s", code, out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, shared, &out, io.Discard, strings.NewReader("4\nn\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("existing update cancel: %d %s", code, out.String())
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after cancels: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if view.Outcome != "completed" || !found {
		t.Fatalf("cancels mutated install: %+v", view)
	}
}

func TestSetupWizardPendingIntentConflictE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "codex", "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--hooks", "false", "--package", env.pkg, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("install result: %+v", installed)
	}
	plantWizardCLIPendingInstall(t, ctx, env.control, env.runtime, installed.Generation)
	out.Reset()
	uninstall := append([]string{"--action", "uninstall", "--yes", "--json", "--external-uninstalled"}, shared...)
	if code := executeSetupWizardWith(ctx, uninstall, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("pending uninstall exit: %d %s", code, out.String())
	}
	conflict := decodeWizardJSON(t, out)
	if conflict.Outcome != "conflict" || conflict.Reason != "pending_intent_conflict" {
		t.Fatalf("pending uninstall: %+v", conflict)
	}
	joined := strings.Join(conflict.Command, " ")
	if !strings.Contains(joined, "--action install") || !strings.Contains(joined, "--agents codex") {
		t.Fatalf("retry dropped pending install: %v", conflict.Command)
	}
	if strings.Contains(joined, "--action uninstall") {
		t.Fatalf("repair-style mutation replaced pending install: %v", conflict.Command)
	}
	out.Reset()
	repair := append([]string{"--action", "repair", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("pending repair exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("repair replaced pending install: %+v", got)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect during pending: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	found, resume := false, false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	for _, next := range view.NextActions {
		if next.Kind == "resume" {
			resume = true
			if !strings.Contains(strings.Join(next.Command, " "), "--action install") {
				t.Fatalf("inspect resume dropped install: %+v", next)
			}
		}
	}
	if !found || !resume {
		t.Fatalf("inspect during pending: %+v", view)
	}
}

func TestSetupWizardSecondClientDifferentDigestE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
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
	binding := func(result setupwizard.Result, client string) string {
		for _, target := range result.Targets {
			if target.Unit == "agent-notify" && target.Client == client {
				return target.Reason
			}
		}
		return ""
	}
	var out bytes.Buffer
	claudeFlags := append([]string{"--action", "install", "--agents", "claude", "--package", env.pkg, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, claudeFlags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install claude: %d %s", code, out.String())
	}
	claude := decodeWizardJSON(t, out)
	if claude.Outcome != "completed" {
		t.Fatalf("install claude result: %+v", claude)
	}
	claudeBinding := binding(claude, "claude")
	if claudeBinding == "" {
		t.Fatalf("missing claude binding: %+v", claude.Targets)
	}
	other := filepath.Join(env.root, "other-package")
	writeWizardPackage(t, other, env.probe)
	if err := os.WriteFile(filepath.Join(other, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: Revised\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	ttyMismatch := append([]string{"--action", "install", "--agents", "codex", "--package", other}, shared...)
	if code := executeSetupWizardWith(ctx, ttyMismatch, &out, io.Discard, strings.NewReader(""), true); code != 1 {
		t.Fatalf("tty mismatch exit: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "required-update=claude") || !strings.Contains(out.String(), "phases=1-update:claude;2-add:codex") {
		t.Fatalf("tty mismatch hid two phases: %s", out.String())
	}
	if !strings.Contains(out.String(), "next update:") || !strings.Contains(out.String(), "next install:") {
		t.Fatalf("tty mismatch hid phase commands: %s", out.String())
	}
	out.Reset()
	mismatch := append([]string{"--action", "install", "--agents", "codex", "--package", other, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, mismatch, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("mismatch exit: %d %s", code, out.String())
	}
	blocked := decodeWizardJSON(t, out)
	if blocked.Outcome != "incomplete" || blocked.Reason != "update_required" {
		t.Fatalf("mismatch: %+v", blocked)
	}
	if len(blocked.NextActions) != 2 || blocked.NextActions[0].Kind != "update" || blocked.NextActions[1].Kind != "install" {
		t.Fatalf("mismatch next: %+v", blocked.NextActions)
	}
	if !strings.Contains(strings.Join(blocked.NextActions[0].Command, " "), "--agents claude") {
		t.Fatalf("update command missed live client: %v", blocked.NextActions[0].Command)
	}
	if !strings.Contains(strings.Join(blocked.NextActions[1].Command, " "), "--agents codex") {
		t.Fatalf("add command missed new client: %v", blocked.NextActions[1].Command)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after mismatch: %d %s", code, out.String())
	}
	afterMismatch := notify(decodeWizardJSON(t, out))
	if afterMismatch["claude"] != "installed" || afterMismatch["codex"] == "installed" {
		t.Fatalf("mismatch mutated bindings: %+v", afterMismatch)
	}
	out.Reset()
	add := append([]string{"--action", "install", "--agents", "codex", "--package", env.pkg, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, add, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("add same digest: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" || got.InstallationID != claude.InstallationID {
		t.Fatalf("add same digest: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after add: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	both := notify(view)
	if both["claude"] != "installed" || both["codex"] != "installed" {
		t.Fatalf("inspect after add: %+v", view.Targets)
	}
	if binding(view, "claude") != claudeBinding {
		t.Fatalf("claude binding revised: %s vs %s", claudeBinding, binding(view, "claude"))
	}
}

func TestSetupWizardMixedUninstallHoldsClaudeE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
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
	install := append([]string{"--action", "install", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install both: %+v", got)
	}
	out.Reset()
	hold := append([]string{"--action", "uninstall", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, hold, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("hold exit: %d %s", code, out.String())
	}
	held := decodeWizardJSON(t, out)
	if held.Outcome != "incomplete" || held.Reason != "external_uninstall_required" {
		t.Fatalf("hold: %+v", held)
	}
	for _, target := range held.Targets {
		if target.Client == "claude" && target.Unit == "agent-notify" && target.Outcome == "completed" {
			t.Fatalf("Claude removed before Codex attestation: %+v", held.Targets)
		}
	}
	joined := strings.Join(held.Command, " ")
	if !strings.Contains(joined, "claude") || !strings.Contains(joined, "codex") || !strings.Contains(joined, "--external-uninstalled") {
		t.Fatalf("retry omitted mixed uninstall: %v", held.Command)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect hold: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	both := notify(view)
	if both["claude"] != "installed" || both["codex"] != "installed" {
		t.Fatalf("hold revoked a sibling: %+v", view.Targets)
	}
	found := false
	for _, next := range view.NextActions {
		if next.Kind == "test-notification" {
			t.Fatalf("inspect offered delivery while uninstall is pending: %+v", view.NextActions)
		}
		if next.Kind != "external-uninstall" {
			continue
		}
		found = true
		cmd := strings.Join(next.Command, " ")
		if !strings.Contains(cmd, "claude") || !strings.Contains(cmd, "codex") || !strings.Contains(cmd, "--external-uninstalled") {
			t.Fatalf("inspect resume omitted mixed uninstall: %v", next.Command)
		}
	}
	if !found {
		t.Fatalf("inspect omitted pending uninstall: %+v", view.NextActions)
	}
	out.Reset()
	resume := []string{
		"--action", "uninstall", "--json", "--external-uninstalled",
		"--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--helper", env.probe,
		"--claude-executable", env.probe, "--codex-executable", env.probe,
	}
	if code := executeSetupWizardWith(ctx, resume, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("attested resume: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("attested resume: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after attested uninstall: %d %s", code, out.String())
	}
	after := notify(decodeWizardJSON(t, out))
	if after["claude"] == "installed" || after["codex"] == "installed" {
		t.Fatalf("attested uninstall left bindings: %+v", after)
	}
}

func TestSetupWizardResumeOmitsAgentsFromPendingE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	plantWizardCLIPendingIntent(t, ctx, env.control, env.runtime, snap.Ledger.Generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: snap.Ledger.Generation, SourceRevision: "1.42.0",
		Targets: []portablesetup.IntentTarget{{
			Client: "codex", Profile: env.codexHome, Units: []string{"direct-mcp"},
		}},
	})
	t.Setenv("CODEX_HOME", filepath.Join(env.root, "later-env-codex"))
	var out bytes.Buffer
	resume := []string{
		"--action", "install", "--package", env.pkg, "--json",
		"--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--client-executable", env.probe,
		"--helper", env.probe, "--scope-root", env.scope,
	}
	if code := executeSetupWizardWith(ctx, resume, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("resume omitted agents: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome == "cancelled" || installed.Reason == "empty_selection" {
		t.Fatalf("did not restore pending agents: %+v", installed)
	}
	if installed.Reason == "noninteractive_requires_yes" {
		t.Fatalf("matching pending intent still required --yes: %+v", installed)
	}
	if installed.Reason == "pending_intent_conflict" {
		t.Fatalf("compiled consumer version treated as explicit: %+v", installed)
	}
	if installed.Outcome != "completed" {
		t.Fatalf("resume omitted agents: %+v", installed)
	}
	out.Reset()
	inspect := []string{"--action", "inspect", "--json", "--control-root", env.control, "--runtime-root", env.runtime, "--helper", env.probe}
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after resume: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	found := false
	for _, target := range view.Targets {
		if target.Client == "codex" && target.Unit == "agent-notify" && target.Outcome == "installed" && target.Profile == env.codexHome {
			found = true
		}
	}
	if !found {
		t.Fatalf("resume missed restored profile: %+v", view.Targets)
	}
}

func TestSetupWizardUninstallExplicitFalsePreservesNotifyE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	env := newWizardCLIEnv(t, ctx, false)
	bundle := writeWizardPluginBundle(t)
	shared := []string{
		"--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{
		"--action", "install", "--agents", "codex", "--hooks", "true", "--agent-notify", "true",
		"--package", env.pkg, "--plugin-root", bundle, "--yes", "--json",
	}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both units: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install both units: %+v", got)
	}
	out.Reset()
	uninstall := append([]string{"--action", "uninstall", "--agents", "codex", "--agent-notify", "false", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, uninstall, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall hooks only: %d %s", code, out.String())
	}
	removed := decodeWizardJSON(t, out)
	if removed.Outcome != "completed" {
		t.Fatalf("uninstall hooks only: %+v", removed)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after hooks-only uninstall: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	var hooksInstalled, notifyInstalled bool
	for _, target := range view.Targets {
		if target.Unit == "hooks" && target.Outcome == "installed" {
			hooksInstalled = true
		}
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			notifyInstalled = true
		}
	}
	if hooksInstalled || !notifyInstalled {
		t.Fatalf("explicit false did not keep notify: %+v", view.Targets)
	}
}

func TestSetupWizardUninstallNotifyOnlyKeepsHooksE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	env := newWizardCLIEnv(t, ctx, false)
	bundle := writeWizardPluginBundle(t)
	shared := []string{
		"--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out, stderr bytes.Buffer
	install := append([]string{
		"--action", "install", "--agents", "codex", "--hooks", "true", "--agent-notify", "true",
		"--package", env.pkg, "--plugin-root", bundle, "--yes", "--json",
	}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both units: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install both units: %+v", got)
	}
	out.Reset()
	uninstall := append([]string{
		"--action", "uninstall", "--agents", "codex", "--hooks", "false", "--agent-notify", "true",
		"--yes", "--json", "--external-uninstalled",
	}, shared...)
	if code := executeSetupWizardWith(ctx, uninstall, &out, &stderr, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall notify-only: %d %s", code, out.String())
	}
	removed := decodeWizardJSON(t, out)
	if removed.Outcome != "completed" {
		t.Fatalf("uninstall notify-only: %+v", removed)
	}
	if strings.Contains(stderr.String(), "phase prepare") {
		t.Fatalf("notify-only uninstall acquired a package: %s", stderr.String())
	}
	for _, next := range removed.NextActions {
		if next.Kind == "test-notification" || next.Kind == "request-permission" {
			t.Fatalf("uninstall offered setup action: %+v", removed.NextActions)
		}
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after notify-only uninstall: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
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
		t.Fatalf("notify-only uninstall dropped hooks or kept notify: %+v", view.Targets)
	}
}

func TestSetupWizardUpdateOneClientKeepsSiblingE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
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
	install := append([]string{"--action", "install", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("install both: %+v", installed)
	}
	if err := os.WriteFile(filepath.Join(env.pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	update := append([]string{"--action", "update", "--agents", "codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, update, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("update codex: %d %s", code, out.String())
	}
	updated := decodeWizardJSON(t, out)
	if updated.Outcome != "completed" || updated.InstallationID != installed.InstallationID {
		t.Fatalf("update codex: %+v", updated)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after update: %d %s", code, out.String())
	}
	both := notify(decodeWizardJSON(t, out))
	if both["claude"] != "installed" || both["codex"] != "installed" {
		t.Fatalf("sibling lost after one-client update: %+v", both)
	}
}

func TestSetupWizardInspectPendingJournalE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	plantWizardCLIPendingJournal(t, env.control)
	var out bytes.Buffer
	inspect := []string{
		"--action", "inspect", "--json", "--control-root", env.control,
		"--runtime-root", env.runtime, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("pending journal inspect exit: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	if view.Outcome != "incomplete" || view.Reason != "recovery_required" {
		t.Fatalf("pending journal inspect: %+v", view)
	}
	found := false
	for _, next := range view.NextActions {
		if next.Kind == "recover" && strings.Contains(next.Reason, "wizard-pending-op") {
			found = true
		}
	}
	if !found {
		t.Fatalf("inspect omitted recover action: %+v", view.NextActions)
	}
}

func TestSetupWizardInspectPendingKernelJournalE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	plantWizardCLIKernelJournal(t, ctx, env.control, env.runtime)
	var out bytes.Buffer
	inspect := []string{
		"--action", "inspect", "--json", "--control-root", env.control,
		"--runtime-root", env.runtime, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("pending kernel inspect exit: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	if view.Outcome != "incomplete" || view.Reason != "recovery_required" {
		t.Fatalf("pending kernel inspect: %+v", view)
	}
	found := false
	for _, next := range view.NextActions {
		if next.Kind == "recover" && next.Reason == "kernel-journal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inspect omitted kernel recover action: %+v", view.NextActions)
	}
	if _, err := os.Lstat(filepath.Join(env.control, "transaction.json")); err != nil {
		t.Fatalf("inspect recovered kernel journal: %v", err)
	}
}

func TestSetupWizardInstallRecoversPendingJournalE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	plantWizardCLIPendingJournal(t, env.control)
	var out bytes.Buffer
	install := []string{
		"--action", "install", "--agents", "codex", "--hooks", "false", "--yes", "--json",
		"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install recover: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("install recover: %+v", installed)
	}
	journal := filepath.Join(filepath.Dir(env.control), "uap", "state", "operations", "wizard-pending-op.json")
	if _, err := os.Lstat(journal); !os.IsNotExist(err) {
		t.Fatalf("install left pending journal: %v", err)
	}
	out.Reset()
	inspect := []string{
		"--action", "inspect", "--json", "--control-root", env.control,
		"--runtime-root", env.runtime, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after recover: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	if view.Outcome == "incomplete" && view.Reason == "recovery_required" {
		t.Fatalf("inspect still recovery_required: %+v", view)
	}
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Client == "codex" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inspect after recover missed install: %+v", view.Targets)
	}
}

func TestSetupWizardRepairDifferentDigestRequiresUpdateE2E(t *testing.T) {
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
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(env.pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("repair", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("repair exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "incomplete" || got.Reason != "update_required" {
		t.Fatalf("repair rewrote revision: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags("inspect", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after refused repair: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	found := false
	for _, target := range view.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refused repair dropped binding: %+v", view.Targets)
	}
}

func TestSetupWizardMixedUninstallHoldsCodexHooksE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	envHome := t.TempDir()
	testenv.Set(t, envHome)
	canonical := filepath.Join(envHome, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	env := newWizardCLIEnv(t, ctx, false)
	bundle := writeWizardPluginBundle(t)
	shared := []string{
		"--package", env.pkg, "--plugin-root", bundle, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install both: %+v", got)
	}
	hooks := filepath.Join(env.codexHome, "hooks.json")
	data, err := os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("hooks.json after install: %s %v", data, err)
	}
	out.Reset()
	hold := append([]string{"--action", "uninstall", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, hold, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("hold exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "incomplete" || got.Reason != "external_uninstall_required" {
		t.Fatalf("hold: %+v", got)
	}
	data, err = os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("Codex hooks removed before attestation: %s %v", data, err)
	}
}

func TestSetupWizardRetainedDifferentDigestRequiresUpdateE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	flags := func(pkg, action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "false",
			"--package", pkg, "--control-root", env.control, "--runtime-root", env.runtime,
			"--global-config", env.global, "--codex-home", env.codexHome,
			"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		}
		return append(args, extra...)
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags(env.pkg, "install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	installed := decodeWizardJSON(t, out)
	if installed.Outcome != "completed" {
		t.Fatalf("install: %+v", installed)
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(env.root, "uap", "state")})
	if err != nil {
		t.Fatal(err)
	}
	uapView, err := eng.Inspect(ctx)
	if err != nil || len(uapView.Installations) != 1 || len(uapView.Installations[0].Bindings) == 0 {
		t.Fatalf("uap inspect: %+v %v", uapView, err)
	}
	dataRoot := uapView.Installations[0].Bindings[0].DataRoot
	if dataRoot == "" {
		t.Fatal("missing plugin data root")
	}
	sentinel := filepath.Join(dataRoot, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("retain\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags(env.pkg, "uninstall", "--yes", "--json", "--external-uninstalled"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("uninstall: %+v", got)
	}
	other := filepath.Join(env.root, "other-package")
	writeWizardPackage(t, other, env.probe)
	if err := os.WriteFile(filepath.Join(other, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags(other, "install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("retained digest exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "incomplete" || got.Reason != "update_required" {
		t.Fatalf("retained digest mismatch: %+v", got)
	}
	if len(got.NextActions) != 2 || got.NextActions[0].Kind != "update" || got.NextActions[1].Kind != "install" {
		t.Fatalf("retained update phases: %+v", got.NextActions)
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "retain\n" {
		t.Fatalf("PLUGIN_DATA sentinel: %s %v", body, err)
	}
	out.Reset()
	inspect := []string{
		"--action", "inspect", "--json", "--control-root", env.control,
		"--runtime-root", env.runtime, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect after retained mismatch: %d %s", code, out.String())
	}
	for _, target := range decodeWizardJSON(t, out).Targets {
		if target.Unit == "agent-notify" && target.Outcome == "installed" {
			t.Fatalf("hidden retained migration: %+v", target)
		}
	}
}

func TestSetupWizardRepairOmittedUnitsPreservesNotifyE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "codex", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--hooks", "false", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install: %+v", got)
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(env.root, "uap", "state")})
	if err != nil {
		t.Fatal(err)
	}
	uapView, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, installation := range uapView.Installations {
		for _, binding := range installation.Bindings {
			if binding.ClientID == "codex" && binding.TargetPath != "" {
				target = binding.TargetPath
			}
		}
	}
	if target == "" {
		t.Fatal("missing live target")
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	repair := append([]string{"--action", "repair", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("omitted-unit repair: %d %s", code, out.String())
	}
	repaired := decodeWizardJSON(t, out)
	if repaired.Outcome != "completed" {
		t.Fatalf("omitted-unit repair: %+v", repaired)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not restore target: %v", err)
	}
	for _, item := range repaired.Targets {
		if item.Unit == "hooks" && item.Outcome != "absent" && item.Outcome != "" {
			t.Fatalf("omitted repair added hooks: %+v", repaired.Targets)
		}
	}
}

func TestSetupWizardInstallFromReleaseZipE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	pkg := filepath.Join(env.root, "release-pkg")
	archive := filepath.Join(env.root, portableasset.AssetName(runtime.GOOS, runtime.GOARCH))
	if _, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: env.probe, OutputRoot: pkg, Archive: archive,
	}); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--action", "install", "--agents", "codex", "--hooks", "false",
		"--package", archive, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		"--yes", "--json",
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("zip install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("zip install: %+v", got)
	}
}

func TestSetupWizardRefusesSymlinkPackageE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	link := filepath.Join(env.root, "package-link")
	if err := os.Symlink(env.pkg, link); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--action", "install", "--agents", "codex", "--hooks", "false",
		"--package", link, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		"--yes", "--json",
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("symlink package exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Reason != "package_acquisition_failed" {
		t.Fatalf("symlink package: %+v", got)
	}
}

func TestSetupWizardRefusesUnsafeZipE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	archive := filepath.Join(env.root, "escape.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--action", "install", "--agents", "codex", "--hooks", "false",
		"--package", archive, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		"--yes", "--json",
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("unsafe zip exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Reason != "package_acquisition_failed" {
		t.Fatalf("unsafe zip: %+v", got)
	}
	if _, err := os.Lstat(filepath.Join(env.root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("zip slip wrote outside dest")
	}
}

func TestSetupWizardAmbiguousInstallationsConflictE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	first := append([]string{"--action", "install", "--agents", "codex", "--installation-id", "00000000-0000-4000-8000-000000000080", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, first, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("first: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("first: %+v", got)
	}
	other := filepath.Join(env.root, "other-package")
	writeWizardPackage(t, other, env.probe)
	if err := os.WriteFile(filepath.Join(other, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: Other installation\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	secondShared := append([]string{}, shared...)
	secondShared[3] = other
	second := append([]string{"--action", "install", "--agents", "claude", "--installation-id", "00000000-0000-4000-8000-000000000081", "--yes", "--json"}, secondShared...)
	if code := executeSetupWizardWith(ctx, second, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("second: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("second: %+v", got)
	}
	out.Reset()
	omitted := append([]string{"--action", "install", "--agents", "codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, omitted, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("omitted install exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "conflict" || got.Reason != "ambiguous_installation" {
		t.Fatalf("omitted install: %+v", got)
	}
	if len(got.NextActions) == 0 || got.NextActions[0].Kind != "inspect" {
		t.Fatalf("inspect action: %+v", got.NextActions)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect: %d %s", code, out.String())
	}
	if view := decodeWizardJSON(t, out); view.Outcome != "completed" {
		t.Fatalf("inspect: %+v", view)
	}
	out.Reset()
	uninstall := append([]string{"--action", "uninstall", "--agents", "codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, uninstall, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("omitted uninstall exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "conflict" || got.Reason != "ambiguous_installation" {
		t.Fatalf("omitted uninstall: %+v", got)
	}
	out.Reset()
	explicit := append([]string{"--action", "uninstall", "--agents", "codex", "--installation-id", "00000000-0000-4000-8000-000000000080", "--yes", "--json", "--external-uninstalled"}, shared...)
	if code := executeSetupWizardWith(ctx, explicit, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("explicit uninstall: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("explicit uninstall: %+v", got)
	}
}

func TestSetupWizardCodexLiveProfileConflictE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	other := filepath.Join(env.root, "codex-other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	flags := func(home, action string, extra ...string) []string {
		args := []string{
			"--action", action, "--agents", "codex", "--hooks", "false",
			"--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
			"--global-config", env.global, "--codex-home", home,
			"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
		}
		return append(args, extra...)
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, flags(env.codexHome, "install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("install: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags(other, "install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("mismatch install exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "conflict" || got.Reason != "live_profile_conflict" {
		t.Fatalf("install mismatch: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags(other, "uninstall", "--yes", "--json", "--external-uninstalled"), &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("mismatch uninstall exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "conflict" || got.Reason != "live_profile_conflict" {
		t.Fatalf("uninstall mismatch: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags(env.codexHome, "uninstall", "--yes", "--json", "--external-uninstalled"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("matching uninstall: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("matching uninstall: %+v", got)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags(other, "install", "--yes", "--json"), &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("reinstall other profile: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("reinstall other profile: %+v", got)
	}
}

func TestSetupWizardRepairOmittedZipUsesDurableSourceE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	pkg := filepath.Join(env.root, "release-pkg")
	archive := filepath.Join(env.root, portableasset.AssetName(runtime.GOOS, runtime.GOARCH))
	if _, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: env.probe, OutputRoot: pkg, Archive: archive,
	}); err != nil {
		t.Fatal(err)
	}
	shared := []string{
		"--agents", "codex", "--hooks", "false", "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--package", archive, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("zip install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("zip install: %+v", got)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	eng, err := uapinstaller.New(uapinstaller.Config{StateRoot: filepath.Join(env.root, "uap", "state")})
	if err != nil {
		t.Fatal(err)
	}
	uapView, err := eng.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, installation := range uapView.Installations {
		for _, binding := range installation.Bindings {
			if binding.ClientID == "codex" && binding.TargetPath != "" {
				target = binding.TargetPath
			}
		}
	}
	if target == "" {
		t.Fatal("missing live target")
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	repair := append([]string{"--action", "repair", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("omitted zip repair: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("omitted zip repair: %+v", got)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("repair did not use durable acquired source: %v", err)
	}
}

func TestSetupWizardRepairMissingDurableZipIsUnavailableE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	pkg := filepath.Join(env.root, "release-pkg")
	archive := filepath.Join(env.root, portableasset.AssetName(runtime.GOOS, runtime.GOARCH))
	if _, err := portableasset.Build(portableasset.BuildRequest{
		Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Executable: env.probe, OutputRoot: pkg, Archive: archive,
	}); err != nil {
		t.Fatal(err)
	}
	shared := []string{
		"--agents", "codex", "--hooks", "false", "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--package", archive, "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("zip install: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "completed" {
		t.Fatalf("zip install: %+v", got)
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(env.root, "uap", "acquired-source")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	repair := append([]string{"--action", "repair", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("missing durable zip exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "incomplete" || got.Reason != "package_acquisition_failed" {
		t.Fatalf("missing durable zip: %+v", got)
	}
}

func TestSetupWizardRepairExplicitNewHooksRequiresInstallE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "codex", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--hooks", "false", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	out.Reset()
	repair := append([]string{"--action", "repair", "--hooks", "true", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("repair added hooks exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Reason != "install_required" {
		t.Fatalf("repair added hooks: %+v", got)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "install" {
		t.Fatalf("repair missing install next action: %+v", got.NextActions)
	}
}

func TestSetupWizardRepairMissingSiblingDoesNotMutateLiveE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both: %d %s", code, out.String())
	}
	out.Reset()
	dropClaude := append([]string{"--action", "uninstall", "--agents", "claude", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, dropClaude, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("uninstall claude: %d %s", code, out.String())
	}
	out.Reset()
	repair := append([]string{"--action", "repair", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, repair, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("repair missing sibling exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Reason != "not_installed" {
		t.Fatalf("repair missing sibling: %+v", got)
	}
	if len(got.NextActions) != 2 || got.NextActions[0].Kind != "install" || got.NextActions[1].Kind != "repair" {
		t.Fatalf("repair missing sibling next actions: %+v", got.NextActions)
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect: %d %s", code, out.String())
	}
	both := map[string]string{}
	for _, target := range decodeWizardJSON(t, out).Targets {
		if target.Unit == "agent-notify" {
			both[target.Client] = target.Outcome
		}
	}
	if both["codex"] != "installed" || both["claude"] == "installed" {
		t.Fatalf("repair mutated live sibling: %+v", both)
	}
}

func TestSetupWizardUpdateOmittedUnitsPreservesNotifyE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--agents", "codex", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--hooks", "false", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	if err := os.WriteFile(filepath.Join(env.pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	update := append([]string{"--action", "update", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, update, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("omitted-unit update: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "completed" {
		t.Fatalf("omitted-unit update: %+v", got)
	}
	for _, target := range got.Targets {
		if target.Unit == "hooks" && target.Outcome != "absent" && target.Outcome != "" {
			t.Fatalf("omitted update added hooks: %+v", got.Targets)
		}
	}
	out.Reset()
	inspect := append([]string{"--action", "inspect", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect: %d %s", code, out.String())
	}
	for _, target := range decodeWizardJSON(t, out).Targets {
		if target.Unit == "hooks" && target.Outcome == "installed" {
			t.Fatalf("omitted update installed hooks: %+v", target)
		}
	}
}

func TestSetupWizardUninstallBothWhenOneMissingE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	shared := []string{
		"--hooks", "false", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome, "--claude-config", env.claudeConfig,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--agents", "claude,codex", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install both: %d %s", code, out.String())
	}
	out.Reset()
	dropClaude := append([]string{"--action", "uninstall", "--agents", "claude", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, dropClaude, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("remove claude: %d %s", code, out.String())
	}
	out.Reset()
	both := append([]string{"--action", "uninstall", "--agents", "claude,codex", "--yes", "--json", "--external-uninstalled"}, shared...)
	if code := executeSetupWizardWith(ctx, both, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("remove both after one missing: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "completed" {
		t.Fatalf("remove both after one missing: %+v", got)
	}
	var claudeAbsent, codexRemoved bool
	for _, target := range got.Targets {
		if target.Unit != "agent-notify" {
			continue
		}
		if target.Client == "claude" && target.Outcome == "unchanged" && target.Reason == "already_absent" {
			claudeAbsent = true
		}
		if target.Client == "codex" && target.Outcome == "completed" {
			codexRemoved = true
		}
	}
	if !claudeAbsent || !codexRemoved {
		t.Fatalf("mixed remove targets: %+v", got.Targets)
	}
}

func TestSetupWizardCodexUninstallAttestsFromEmptyPluginListE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	stub := writeWizardCodexListStub(t, env.root, `{"installed":[]}`)
	shared := []string{
		"--agents", "codex", "--hooks", "false", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime,
		"--global-config", env.global, "--codex-home", env.codexHome,
		"--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	install := append([]string{"--action", "install", "--yes", "--json"}, shared...)
	if code := executeSetupWizardWith(ctx, install, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("install: %d %s", code, out.String())
	}
	out.Reset()
	uninstall := []string{
		"--action", "uninstall", "--agents", "codex", "--hooks", "false", "--yes", "--json",
		"--control-root", env.control, "--runtime-root", env.runtime, "--global-config", env.global,
		"--codex-home", env.codexHome, "--client-executable", stub, "--helper", env.probe, "--scope-root", env.scope,
	}
	if code := executeSetupWizardWith(ctx, uninstall, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("observed uninstall: %d %s", code, out.String())
	}
	removed := decodeWizardJSON(t, out)
	if removed.Outcome != "completed" {
		t.Fatalf("observed uninstall: %+v", removed)
	}
	if strings.Contains(strings.Join(removed.Command, " "), "--external-uninstalled") {
		t.Fatalf("empty list still required flag: %v", removed.Command)
	}
}

func TestSetupWizardFailedHooksKeepsPendingIntentE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	args := []string{
		"--action", "install", "--agents", "codex", "--package", env.pkg, "--yes", "--json",
		"--control-root", env.control, "--runtime-root", env.runtime, "--global-config", env.global,
		"--codex-home", env.codexHome, "--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("hooks preflight exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "incomplete" || got.Reason != "plugin_root_required" {
		t.Fatalf("hooks preflight: %+v", got)
	}
	out.Reset()
	inspect := []string{
		"--action", "inspect", "--json", "--control-root", env.control,
		"--runtime-root", env.runtime, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, inspect, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect pending install: %d %s", code, out.String())
	}
	view := decodeWizardJSON(t, out)
	foundResume := false
	for _, next := range view.NextActions {
		if next.Kind == "test-notification" {
			t.Fatalf("inspect offered delivery while install is pending: %+v", view.NextActions)
		}
		if next.Kind != "resume" {
			continue
		}
		foundResume = true
		cmd := strings.Join(next.Command, " ")
		if !strings.Contains(cmd, "install") || !strings.Contains(cmd, "codex") {
			t.Fatalf("inspect omitted pending install resume: %v", next.Command)
		}
	}
	if !foundResume {
		t.Fatalf("inspect omitted pending install: %+v", view.NextActions)
	}
}

func TestSetupWizardResumeRejectsDifferentAgentsE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	plantWizardCLIPendingIntent(t, ctx, env.control, env.runtime, snap.Ledger.Generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: snap.Ledger.Generation,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
	var out bytes.Buffer
	args := []string{
		"--action", "install", "--agents", "claude", "--yes", "--json",
		"--control-root", env.control, "--runtime-root", env.runtime, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("different agents exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different agents: %+v", got)
	}
	joined := strings.Join(got.Command, " ")
	if !strings.Contains(joined, "--action install") || !strings.Contains(joined, "--agents codex") {
		t.Fatalf("retry dropped pending codex install: %v", got.Command)
	}
}

func TestSetupWizardResumeRejectsDifferentProfileE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	other := filepath.Join(env.root, "other-codex")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	plantWizardCLIPendingIntent(t, ctx, env.control, env.runtime, snap.Ledger.Generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: snap.Ledger.Generation,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Profile: env.codexHome, Units: []string{"direct-mcp"}}},
	})
	var out bytes.Buffer
	args := []string{
		"--action", "install", "--agents", "codex", "--yes", "--json",
		"--control-root", env.control, "--runtime-root", env.runtime,
		"--codex-home", other, "--helper", env.probe,
	}
	if code := executeSetupWizardWith(ctx, args, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("different profile exit: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("different profile: %+v", got)
	}
}

func TestSetupWizardResumeRestoresMixedPerClientUnitsE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	plantWizardCLIPendingIntent(t, ctx, env.control, env.runtime, snap.Ledger.Generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: snap.Ledger.Generation,
		Targets: []portablesetup.IntentTarget{
			{Client: "claude", Profile: env.claudeConfig, Units: []string{"hooks"}},
			{Client: "codex", Profile: env.codexHome, Units: []string{"agent-notify"}},
		},
	})
	t.Setenv("CODEX_HOME", filepath.Join(env.root, "later-env-codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(env.root, "later-env-claude"))
	var out bytes.Buffer
	resume := []string{
		"--action", "install", "--json",
		"--control-root", env.control, "--runtime-root", env.runtime, "--global-config", env.global,
		"--claude-executable", env.probe, "--codex-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope,
	}
	if code := executeSetupWizardWith(ctx, resume, &out, io.Discard, strings.NewReader(""), false); code == 2 {
		t.Fatalf("mixed resume invalid: %s", out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Outcome == "cancelled" || got.Reason == "empty_selection" {
		t.Fatalf("did not restore pending agents: %+v", got)
	}
	if got.Reason == "noninteractive_requires_yes" {
		t.Fatalf("matching pending intent still required --yes: %+v", got)
	}
	joined := strings.Join(got.Command, " ")
	for _, want := range []string{
		"--agents claude,codex",
		"--claude-hooks true",
		"--codex-hooks false",
		"--claude-agent-notify false",
		"--codex-agent-notify true",
		"--claude-config " + env.claudeConfig,
		"--codex-home " + env.codexHome,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("retry omitted %q: %v", want, got.Command)
		}
	}
	if strings.Contains(joined, "--hooks ") || strings.Contains(joined, "--agent-notify ") {
		t.Fatalf("mixed units collapsed to global flags: %v", got.Command)
	}
	out.Reset()
	conflict := []string{
		"--action", "install", "--agents", "claude,codex", "--hooks", "true", "--agent-notify", "true",
		"--yes", "--json", "--control-root", env.control, "--runtime-root", env.runtime,
	}
	if code := executeSetupWizardWith(ctx, conflict, &out, io.Discard, strings.NewReader(""), false); code != 1 {
		t.Fatalf("global units exit: %d %s", code, out.String())
	}
	if got := decodeWizardJSON(t, out); got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
		t.Fatalf("global units matched mixed intent: %+v", got)
	}
}

func TestSetupWizardResumeOmittedUninstallFromPendingE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := newWizardCLIEnv(t, ctx, false)
	snap, err := installruntime.ReadInstalledSnapshot(env.control)
	if err != nil {
		t.Fatal(err)
	}
	plantWizardCLIPendingIntent(t, ctx, env.control, env.runtime, snap.Ledger.Generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-uninstall-intent", Action: "uninstall", Stage: "revoke-locator",
		ExpectedGeneration: snap.Ledger.Generation,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Profile: env.codexHome, Units: []string{"direct-mcp"}}},
	})
	var out bytes.Buffer
	resume := []string{"--action", "uninstall", "--json", "--control-root", env.control, "--runtime-root", env.runtime}
	if code := executeSetupWizardWith(ctx, resume, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("resume omitted uninstall: %d %s", code, out.String())
	}
	got := decodeWizardJSON(t, out)
	if got.Reason == "empty_selection" || got.Reason == "noninteractive_requires_yes" {
		t.Fatalf("did not restore pending uninstall: %+v", got)
	}
	if got.Outcome != "unchanged" || got.Reason != "portable_absent" {
		t.Fatalf("resume uninstall: %+v", got)
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

func plantWizardCLIPendingInstall(t *testing.T, ctx context.Context, control, runtime string, generation uint64) {
	t.Helper()
	plantWizardCLIPendingIntent(t, ctx, control, runtime, generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-install-intent", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: generation,
		Targets:            []portablesetup.IntentTarget{{Client: "codex", Units: []string{"direct-mcp"}}},
	})
}

func plantWizardCLIPendingIntent(t *testing.T, ctx context.Context, control, runtime string, generation uint64, intent portablesetup.Intent) {
	t.Helper()
	if intent.SetupIntentID == "" {
		intent.SetupIntentID = "pending-install-intent"
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	path := portablesetup.IntentPath(control)
	res := installruntime.PendingMutation{ID: intent.SetupIntentID, Owner: "existing-installer", IntentRef: path}
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, Owner: "existing-installer", RuntimeRoot: runtime, ConsumerID: "existing",
		RefreshOnly: true, ExpectedGeneration: &generation, Reservation: &res,
		Files: []installruntime.File{{Path: path, Data: append(payload, '\n'), Mode: 0600}},
	}); err != nil {
		t.Fatal(err)
	}
}

func plantWizardCLIPendingJournal(t *testing.T, controlRoot string) {
	t.Helper()
	owned := filepath.Join(filepath.Dir(controlRoot), "uap", "managed")
	staging := filepath.Join(owned, ".agentplugins-staging-pending")
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	opID := "wizard-pending-op"
	sum := sha256.Sum256([]byte(opID))
	receipt := dirswap.Receipt{
		SchemaVersion: 3, Operation: dirswap.OperationSwap, OperationID: opID,
		ClientBindingID: "client-binding-1", Sequence: 1, OwnedBase: owned,
		ActivePath: filepath.Join(owned, "plugin"), StagingPath: staging,
		BackupPath: filepath.Join(owned, ".agentplugins-backup-"+hex.EncodeToString(sum[:8])),
		Phase:      dirswap.PhaseIntent,
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	ops := filepath.Join(filepath.Dir(controlRoot), "uap", "state", "operations")
	if err := os.MkdirAll(ops, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ops, opID+".json"), append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func plantWizardCLIKernelJournal(t *testing.T, ctx context.Context, control, runtime string) {
	t.Helper()
	hook := filepath.Join(runtime, "kernel-pending-hook")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime,
		Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: hook, Data: []byte("new"), Mode: 0700}},
		Fault: func(phase string) error {
			if phase == "transaction" {
				return errors.New("crash")
			}
			return nil
		},
	}); err == nil {
		t.Fatal("kernel fault not reached")
	}
	if _, err := os.Lstat(filepath.Join(control, "transaction.json")); err != nil {
		t.Fatal("missing kernel journal")
	}
}

func writeWizardCodexListStub(t *testing.T, dir, listJSON string) string {
	t.Helper()
	path := filepath.Join(dir, "codex-stub")
	script := "#!/bin/sh\ncase \"$*\" in\n  \"plugin list --json\") printf '%s\\n' '" + listJSON + "';;\n  \"plugin remove \"*) echo '{\"ok\":true}';;\n  *) exit 1;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
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
