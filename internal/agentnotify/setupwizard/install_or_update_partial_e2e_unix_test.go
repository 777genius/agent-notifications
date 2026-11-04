//go:build linux || darwin

package setupwizard

import (
	"os"
	"path/filepath"
	"testing"
)

// Exercise actual public Run -> Update -> failed following Plan, rather than
// assuming an earlier phase succeeded based on NextActions or fixture rows.
func TestInstallOrUpdateRetainsCommittedUpdateAfterLaterPlanFailure(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	root := filepath.Dir(control)
	probe := buildProbe(t)
	oldPackage := filepath.Join(root, "old-package")
	candidate := filepath.Join(root, "new-package")
	writePackage(t, oldPackage, probe)
	writePackage(t, candidate, probe)
	if err := os.WriteFile(filepath.Join(candidate, "skills", "agent-notifications", "SKILL.md"),
		[]byte("---\nname: agent-notifications\ndescription: Updated fixture\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	claudeProfile := filepath.Join(root, "claude")
	codexProfile := filepath.Join(root, "codex")
	scope := filepath.Join(root, "scope")
	for _, path := range []string{claudeProfile, codexProfile, scope} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	baseline := Request{
		Action: ActionInstall, Agents: []string{"claude"}, Yes: true,
		Hooks: boolPtr(false), AgentNotify: boolPtr(true),
		PackageRoot: oldPackage, ControlRoot: control, RuntimeRoot: runtimeRoot,
		GlobalConfig: global, ClaudeConfig: claudeProfile, CodexHome: codexProfile,
		ClientExecutable: probe, Helper: probe, ScopeRoot: scope,
		ClaudeRunner: listingRunner{configRoot: claudeProfile},
	}
	first, err := Run(ctx, baseline)
	if err != nil || first.Outcome != "completed" {
		t.Fatalf("baseline install: %+v %v", first, err)
	}
	selected := baseline
	selected.Agents = []string{"claude", "codex"}
	selected.PackageRoot = candidate
	renamedProfile := codexProfile + "-unavailable"
	injected := false
	selected.ClientProgress = func(client, phase, outcome string) {
		if injected || client != "claude" || phase != "verification" || outcome != "done" {
			return
		}
		// The actual first phase reached its native activation boundary.
		// Make only the next, unselected-in-this-phase profile unavailable.
		if err := os.Rename(codexProfile, renamedProfile); err != nil {
			t.Fatal(err)
		}
		// A file in place of the profile must not be adopted as a directory
		// by the following client's preflight.
		if err := os.WriteFile(codexProfile, []byte("TEST:not-a-directory"), 0600); err != nil {
			t.Fatal(err)
		}
		injected = true
	}
	got, installErr := InstallOrUpdate(ctx, selected, false)
	if !injected {
		t.Fatalf("fixture did not reach the committed-client verification phase: %+v %v", got, installErr)
	}
	if installErr == nil || got.ExitCode() == 0 || got.Outcome == "completed" || got.Reason == "" {
		t.Fatalf("late Plan failure was masked: %+v %v", got, installErr)
	}
	committed := false
	for _, target := range got.Targets {
		if target.Client == "claude" && target.Unit == "agent-notify" &&
			target.Outcome == "completed" && target.Reason != "" && target.TreeDigest != "" {
			committed = true
		}
	}
	if !committed {
		t.Fatalf("earlier committed Claude update omitted: %+v (error %v)", got, installErr)
	}
	if err := os.Remove(codexProfile); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(renamedProfile, codexProfile); err != nil {
		t.Fatal(err)
	}
	inspect := baseline
	inspect.Action = ActionInspect
	inspect.Agents = []string{"claude", "codex"}
	view, err := Run(ctx, inspect)
	if err != nil || view.Outcome != "completed" {
		t.Fatalf("read-only installed observation: %+v %v", view, err)
	}
	claudeInstalled, codexInstalled := false, false
	for _, target := range view.Targets {
		if target.Unit != "agent-notify" || target.Outcome != "installed" {
			continue
		}
		claudeInstalled = claudeInstalled || target.Client == "claude"
		codexInstalled = codexInstalled || target.Client == "codex"
	}
	if !claudeInstalled || codexInstalled {
		t.Fatalf("later failed phase changed installed clients: %+v", view.Targets)
	}
}
