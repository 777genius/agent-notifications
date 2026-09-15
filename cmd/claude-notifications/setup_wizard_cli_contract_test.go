package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestSetupWizardHelpAndYesRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizard(ctx, []string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "setup-notifications wizard") || !strings.Contains(out.String(), "units") || !strings.Contains(out.String(), "stderr") || !strings.Contains(out.String(), "Omit on inspect to report both clients") || !strings.Contains(out.String(), "invalid for mutation") || !strings.Contains(out.String(), "matching pending intent") || !strings.Contains(out.String(), "update, or repair") || !strings.Contains(out.String(), "omit on update/repair to keep live units") || !strings.Contains(out.String(), "Inspect exit 0") || !strings.Contains(out.String(), "one group apply") || !strings.Contains(out.String(), "mixed live revisions") || !strings.Contains(out.String(), "then Add of the missing") || !strings.Contains(out.String(), "existing config.toml") || !strings.Contains(out.String(), "existing .claude.json") {
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

func TestSetupWizardInspectReportsDiscoveredMCP(t *testing.T) {
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
	codexHome := filepath.Join(root, "codex")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(mcp, []byte("title = 'keep'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--action", "inspect", "--agents", "codex", "--control-root", control, "--codex-home", codexHome}
	var out bytes.Buffer
	code := executeSetupWizardWith(ctx, append(append([]string{}, flags...), "--json"), &out, io.Discard, strings.NewReader(""), false)
	if code != 0 {
		t.Fatalf("inspect json: %d %s", code, out.String())
	}
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("inspect json: %v %s", err, out.String())
	}
	var mcpFile string
	for _, target := range result.Targets {
		if target.Unit == "direct-mcp" && target.Client == "codex" {
			mcpFile = target.ConfigPath
		}
	}
	if mcpFile != mcp {
		t.Fatalf("inspect json mcp: %s targets=%+v", mcpFile, result.Targets)
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, flags, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("inspect text: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "mcp="+mcp) {
		t.Fatalf("inspect text omitted mcp: %s", out.String())
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
	name := "codex"
	body := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name = "codex.bat"
		body = "@echo off\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".BAT;.COM;.EXE")
	}
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

func TestSetupWizardJSONOmittedAgentsIsInvalid(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	for _, action := range []string{"install", "update", "repair", "uninstall"} {
		var out bytes.Buffer
		code := executeSetupWizardWith(ctx, []string{"--action", action, "--yes", "--control-root", control, "--json"}, &out, io.Discard, strings.NewReader(""), false)
		if code != 2 {
			t.Fatalf("%s omitted agents: %d %s", action, code, out.String())
		}
		var result setupwizard.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s json: %v %s", action, err, out.String())
		}
		if result.Action != action || result.Outcome != "invalid" || result.Reason != "agents_required" {
			t.Fatalf("%s omitted agents result: %+v", action, result)
		}
	}
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "inspect", "--control-root", control, "--json"}, &out, io.Discard, strings.NewReader(""), false); code != 0 {
		t.Fatalf("omitted inspect: %d %s", code, out.String())
	}
	var result setupwizard.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("inspect json: %v %s", err, out.String())
	}
	if result.Action != "inspect" || result.Outcome != "completed" || result.Reason == "agents_required" {
		t.Fatalf("omitted inspect result: %+v", result)
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
