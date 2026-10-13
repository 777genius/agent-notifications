//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

// Actual file boundary: lifecycle rows stay distinct, untrusted CLI bytes never
// become terminal events, and public/symlinked/trailing input is rejected.
func TestInstallerResultPrivateFiniteProtocol(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "result.json")
	result := setupwizard.Result{Action: "install", Outcome: "incomplete", Reason: "SECRET\x1b[31m", Targets: []setupwizard.TargetResult{
		{Client: "claude", Unit: "agent-notify", Outcome: "activation_timeout", Materialization: "materialized", Reason: "SECRET\x1b[31m"},
		{Client: "codex", Unit: "agent-notify", Outcome: "activation_not_attempted", Materialization: "materialized"},
		{Client: "gemini\nINJECT", Unit: "agent-notify", Outcome: "completed"},
		{Client: "cursor", Unit: "agent-notify", Outcome: "activation_timeout"},
	}, NextActions: []setupwizard.NextAction{{Kind: "activate", Command: []string{"setup-notifications", "wizard", "--action", "install", "--control-root", filepath.Join(root, "control with spaces"), "--package", filepath.Join(root, "retained"), "--yes"}}}}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeInstallerResult(path, &out); err != nil {
		t.Fatal(err)
	}
	want := "claude\tMCP\tactivation_timeout\ncodex\tMCP\tactivation_not_attempted\ncursor\tMCP\tstate_requires_inspection\nreason\tcategory\tprobe_timeout\nnext\tcategory\tactivation_available\n"
	if out.String() != want {
		t.Fatalf("unsafe or fabricated protocol %q", out.String())
	}
	out.Reset()
	if err := writeInstallerRecovery(path, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Resume: ") || !strings.Contains(out.String(), "control with spaces") || strings.Contains(out.String(), "SECRET") {
		t.Fatalf("recovery missing or unsafe: %q", out.String())
	}
	out.Reset()
	writeSetupWizardSummary(&out, result)
	for _, want := range []string{"Claude plugin verification timed out", "registration saved; activation timed out", "registration saved; activation not started", "Resume saved activation"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	for _, bad := range []string{"SECRET", "INJECT", "\x1b", "--package", "retry:", "ready:true"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("default leaked %q: %s", bad, out.String())
		}
	}
	for _, bad := range []string{"public", "symlink", "trailing", "oversized"} {
		t.Run(bad, func(t *testing.T) {
			candidate := filepath.Join(root, bad)
			switch bad {
			case "public":
				err = os.WriteFile(candidate, body, 0644)
			case "symlink":
				err = os.Symlink(path, candidate)
			case "trailing":
				err = os.WriteFile(candidate, append(append([]byte{}, body...), []byte(` {}`)...), 0600)
			case "oversized":
				err = os.WriteFile(candidate, bytes.Repeat([]byte(" "), 1024*1024+1), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err := writeInstallerResult(candidate, &out); err == nil || out.Len() != 0 {
				t.Fatalf("unsafe file accepted: %q %v", out.String(), err)
			}
		})
	}
}
