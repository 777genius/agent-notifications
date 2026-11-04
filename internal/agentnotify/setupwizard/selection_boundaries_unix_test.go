//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func installedClaudeSelectionRequest(t *testing.T) Request {
	t.Helper()
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
	installed, err := Run(testCtx(t), req)
	if err != nil || installed.Outcome != "completed" {
		t.Fatalf("install fixture: %+v %v", installed, err)
	}
	req.InstallationID = installed.InstallationID
	return req
}

func TestRepairDoesNotMutateCallerPackageRoots(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Action = ActionRepair
	for _, entry := range []string{"plan", "run"} {
		t.Run(entry, func(t *testing.T) {
			req.PackageRoots = map[string]string{"codex": req.PackageRoot}
			before := copyPackageRoots(req.PackageRoots)
			if entry == "plan" {
				plan, err := Plan(testCtx(t), req)
				if err != nil || !plan.Ready || plan.Request.PackageRoots["claude"] == "" || plan.Request.PackageRoots["codex"] != before["codex"] {
					t.Fatalf("repair plan did not retain and fill package roots: %+v %v", plan, err)
				}
			} else if got, err := Run(testCtx(t), req); err != nil || got.Outcome != "completed" {
				t.Fatalf("repair: %+v %v", got, err)
			}
			if !reflect.DeepEqual(req.PackageRoots, before) {
				t.Fatalf("%s changed caller package roots: got %v want %v", entry, req.PackageRoots, before)
			}
		})
	}
}

func TestOmittedUnitsHonorSelectedInstallation(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Action, req.Hooks, req.AgentNotify = ActionUpdate, nil, nil
	plan, err := Plan(testCtx(t), req)
	if err != nil || !plan.Ready || plan.Request.ClaudeAgentNotify == nil || !*plan.Request.ClaudeAgentNotify {
		t.Fatalf("selected live notification unit lost: %+v %v", plan, err)
	}
	req.InstallationID = "00000000-0000-4000-8000-000000000399"
	plan, err = Plan(testCtx(t), req)
	if err != nil || plan.Ready || plan.Result.Outcome != "cancelled" || plan.Result.Reason != "empty_units" ||
		plan.Request.ClaudeAgentNotify == nil || *plan.Request.ClaudeAgentNotify {
		t.Fatalf("unrelated installation selected notification unit: %+v %v", plan, err)
	}
}

func TestOmittedUnitsRefuseUnreadableStateWithoutChangingSelection(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	req.Hooks, req.AgentNotify = nil, nil
	plantWizardJournal(t, req.ControlRoot)
	journal := wizardPendingJournalPath(req.ControlRoot)
	pending, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{ActionUpdate, ActionRepair} {
		req.Action = action
		plan, err := Plan(testCtx(t), req)
		if err != nil || !plan.Ready || plan.Request.ClaudeAgentNotify == nil || !*plan.Request.ClaudeAgentNotify ||
			!strings.Contains(plan.Text, "recovery-pending=wizard-pending-op") {
			t.Fatalf("known pending recovery changed omitted selection: %+v %v", plan, err)
		}
	}
	if body, err := os.ReadFile(journal); err != nil || !bytes.Equal(body, pending) {
		t.Fatalf("planning changed pending journal: %v", err)
	}
	if err := os.WriteFile(journal, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{ActionUpdate, ActionRepair} {
		req.Action = action
		plan, err := Plan(testCtx(t), req)
		if err == nil || plan.Ready || plan.Result.Outcome != "incomplete" || plan.Result.Reason != "existing_units_unavailable" ||
			plan.Request.ClaudeHooks != nil || plan.Request.ClaudeAgentNotify != nil {
			t.Fatalf("%s plan converted unknown units into opt-out: %+v %v", action, plan, err)
		}
		got, err := Run(testCtx(t), req)
		if err == nil || got.Outcome != "incomplete" || got.Reason != "existing_units_unavailable" || got.ExitCode() == 0 {
			t.Fatalf("%s run accepted unknown units: %+v %v", action, got, err)
		}
	}
	if body, err := os.ReadFile(journal); err != nil || string(body) != "{" {
		t.Fatalf("unreadable journal changed: %q %v", body, err)
	}
	after, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil || !reflect.DeepEqual(after.Ledger, snapshot.Ledger) {
		t.Fatalf("failed selection changed managed state: %+v %v", after.Ledger, err)
	}
}
