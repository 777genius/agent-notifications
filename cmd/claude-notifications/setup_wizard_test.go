//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSetupWizardHelpAndYesRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizard(ctx, []string{"--help"}, &out); code != 0 || !strings.Contains(out.String(), "setup-notifications wizard") {
		t.Fatalf("help: %d %s", code, out.String())
	}
	out.Reset()
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex"}, &out, strings.NewReader(""), false); code != 2 {
		t.Fatalf("missing yes: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex", "--yes", "--control-root", root, "--json"}, &out, strings.NewReader(""), false); code != 1 {
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
	if code := executeSetupWizardWith(ctx, []string{"--action", "install"}, &out, strings.NewReader("\n"), true); code != 0 || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("tty cancel: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--control-root", root}, &out, strings.NewReader("2\ny\n"), true); code != 1 {
		t.Fatalf("tty confirm still needs runtime: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "managed_runtime_required") || !strings.Contains(out.String(), "retry:") {
		t.Fatalf("tty retry: %s", out.String())
	}
}

func TestSetupWizardJSONDoesNotPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	if code := executeSetupWizardWith(ctx, []string{"--action", "install", "--agents", "codex", "--json"}, &out, strings.NewReader("y\n"), true); code != 2 {
		t.Fatalf("json prompt: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "noninteractive_requires_yes") {
		t.Fatalf("json must not consume TTY confirm: %s", out.String())
	}
}
