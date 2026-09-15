//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
	if code := executeSetupWizard(ctx, []string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "setup-notifications wizard") || !strings.Contains(out.String(), "units") || !strings.Contains(out.String(), "stderr") {
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

func TestSetupWizardUnpublishedUpdateAndRepair(t *testing.T) {
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
		if result.Action != action || result.Outcome != "incomplete" || result.Reason != "action_not_published" {
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
