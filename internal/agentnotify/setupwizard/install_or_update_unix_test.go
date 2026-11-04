//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A pending journal can coexist with installed client records. Inspect reports
// that condition without an error; post-install verification must still refuse
// success and preserve its recovery advice without replaying the journal.
func TestVerifyInstallOrUpdateRejectsIncompleteInspection(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	probe := buildProbe(t)
	base := filepath.Dir(control)
	pkg := filepath.Join(base, "package")
	writePackage(t, pkg, probe)
	profile, scope := filepath.Join(base, "claude"), filepath.Join(base, "scope")
	for _, path := range []string{profile, scope} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	req := Request{Action: ActionInstall, Agents: []string{"claude"}, Yes: true,
		Hooks: boolPtr(false), AgentNotify: boolPtr(true), PackageRoot: pkg,
		ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global,
		ClaudeConfig: profile, ClientExecutable: probe, Helper: probe, ScopeRoot: scope,
		ClaudeRunner: listingRunner{configRoot: profile},
	}
	installed, err := Run(ctx, req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install fixture: %+v %v", installed, err)
	}
	if got, err := verifyInstallOrUpdate(ctx, req, installed); err != nil || !reflect.DeepEqual(got, installed) {
		t.Fatalf("completed inspection changed install result: %+v %v", got, err)
	}
	// A completed inspection still requires every selected client.
	missing := req
	missing.Agents = []string{"claude", "codex"}
	if got, err := verifyInstallOrUpdate(ctx, missing, installed); !errors.Is(err, ErrRefused) || got.Reason != "post_install_incomplete" {
		t.Fatalf("completed inspection accepted missing client: %+v %v", got, err)
	}
	plantWizardJournal(t, control)
	path := wizardPendingJournalPath(control)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inspect := req
	inspect.Action = ActionInspect
	view, err := Run(ctx, inspect)
	if err != nil || view.Outcome != "incomplete" || view.Reason != "recovery_required" || len(view.NextActions) == 0 {
		t.Fatalf("fixture did not reach incomplete inspection: %+v %v", view, err)
	}
	found := false
	for _, target := range view.Targets {
		found = found || target.Client == "claude" && target.Unit == "agent-notify" && target.Outcome == "installed"
	}
	if !found {
		t.Fatalf("fixture lost installed target: %+v", view.Targets)
	}
	got, err := verifyInstallOrUpdate(ctx, req, installed)
	if !errors.Is(err, ErrRefused) || got.Action != string(req.Action) || got.Outcome != "incomplete" || got.Reason != view.Reason ||
		!reflect.DeepEqual(got.Targets, view.Targets) || !reflect.DeepEqual(got.NextActions, view.NextActions) {
		t.Fatalf("incomplete inspection accepted or evidence lost: %+v %v", got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("verification replayed pending journal: %v", err)
	}
	// An inspection error must retain the mutation action too. Result.ExitCode
	// carries inspection's reporting convention; the CLI separately checks errors.
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	view, inspectErr := Run(ctx, inspect)
	if inspectErr == nil || view.Outcome != "incomplete" || view.ExitCode() != 0 {
		t.Fatalf("fixture did not reach errored read-only inspection: %+v %v", view, inspectErr)
	}
	got, err = verifyInstallOrUpdate(ctx, req, installed)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), inspectErr.Error()) || got.Action != string(req.Action) || got.Outcome != "incomplete" || got.ExitCode() == 0 ||
		got.Reason != view.Reason || !reflect.DeepEqual(got.Targets, view.Targets) || !reflect.DeepEqual(got.NextActions, view.NextActions) {
		t.Fatalf("errored inspection retained inspect success semantics or lost cause: %+v %v", got, err)
	}
	initial, initialErr := InstallOrUpdate(ctx, req, false)
	if initialErr == nil || !strings.Contains(initialErr.Error(), inspectErr.Error()) || initial.Action != string(req.Action) || initial.Outcome != "incomplete" || initial.ExitCode() == 0 ||
		initial.Reason != view.Reason || !reflect.DeepEqual(initial.Targets, view.Targets) || !reflect.DeepEqual(initial.NextActions, view.NextActions) {
		t.Fatalf("initial inspection retained inspect success semantics or lost cause: %+v %v", initial, initialErr)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "{" {
		t.Fatalf("verification changed unreadable journal: %q %v", body, err)
	}
}
