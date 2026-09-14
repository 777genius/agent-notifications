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
	if code := executeSetupWizard(ctx, []string{"--action", "install", "--agents", "codex"}, &out); code != 2 {
		t.Fatalf("missing yes: %d %s", code, out.String())
	}
	out.Reset()
	root := setupCommandRoot(t)
	if code := executeSetupWizard(ctx, []string{"--action", "install", "--agents", "codex", "--yes", "--control-root", root, "--json"}, &out); code != 1 {
		t.Fatalf("missing runtime: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "managed_runtime_required") {
		t.Fatalf("runtime reason: %s", out.String())
	}
}
