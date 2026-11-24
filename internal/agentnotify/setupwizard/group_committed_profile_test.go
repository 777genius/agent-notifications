//go:build linux || darwin

package setupwizard

import (
	"os"
	"path/filepath"
	"testing"
)

// ApplyGroup commits both bindings before the wizard records their live
// profiles. A damaged retained profile file must not hide the second commit.
func TestGroupInstallReportsBothCommittedClientsAfterProfileWriteFailure(t *testing.T) {
	ctx := testCtx(t)
	req, _, sentinel, originalPackage := prepareRetainedCodexWizard(t, ctx)
	req.PackageRoot = originalPackage
	req.Agents = []string{"codex", "claude"}
	req.Yes = true
	req.ClaudeConfig = filepath.Join(filepath.Dir(req.ControlRoot), "claude-profile")
	if err := os.MkdirAll(req.ClaudeConfig, 0700); err != nil {
		t.Fatal(err)
	}
	req.ClaudeRunner = listingRunner{configRoot: req.ClaudeConfig}
	// This file lives in the retained PLUGIN_DATA root, which the grouped
	// reinstall reuses. The write fails only after the UAP group is committed.
	if err := os.WriteFile(filepath.Join(filepath.Dir(sentinel), liveProfileFile), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, req)
	if err == nil || result.Outcome != "incomplete" {
		t.Fatalf("profile write failure not reported: %+v %v", result, err)
	}
	committed := map[string]bool{}
	for _, target := range result.Targets {
		if target.Unit == "agent-notify" && target.Outcome == "completed" {
			committed[target.Client] = true
		}
	}
	if !committed["codex"] || !committed["claude"] || len(committed) != 2 {
		t.Fatalf("group lost committed client results: %+v", result.Targets)
	}
	if live := LiveNotifyClients(req.ControlRoot, []string{"codex", "claude"}); len(live) != 2 {
		t.Fatalf("group failed to commit both clients: %v", live)
	}
}
