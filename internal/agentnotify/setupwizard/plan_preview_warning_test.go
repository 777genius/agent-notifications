//go:build linux || darwin

package setupwizard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestRepairPlanWarnsOnlyWhenEveryPreviewIsUnavailable(t *testing.T) {
	// Both clients have real installed package/receipt state. Codex fixture
	// creation uses supported manual activation; this test observes Plan only.
	req, mat, _, _ := uninstallProfileFixture(t)
	req.ClaudeConfig = t.TempDir()
	req.ClaudeRunner = listingRunner{configRoot: req.ClaudeConfig}
	mat.Roots.ClaudeRunner = req.ClaudeRunner
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := mat.Install(testCtx(t), portablesetup.MaterializeRequest{
		Identity: portablesetup.Identity{InstallationID: req.InstallationID, ComponentID: snap.Ledger.ID, Owner: snap.Ledger.Owner,
			ScopeRoot: req.ScopeRoot, ControlRoot: req.ControlRoot, GlobalConfig: req.GlobalConfig, RuntimeRoot: req.RuntimeRoot, Primary: req.Primary},
		Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation, PackageRoot: req.PackageRoot,
		ClientConfigRoot: req.ClaudeConfig, ClientExecutable: req.ClientExecutable, OperationID: "TEST-plan-preview-claude",
	})
	if err != nil {
		t.Fatal("install Claude preview fixture", err)
	}
	if err := recordLiveProfile(installed.DataRoot, "claude", req.ClaudeConfig); err != nil {
		t.Fatal(err)
	}
	matching := filepath.Join(filepath.Dir(req.ControlRoot), "matching-package")
	copyPackage(t, req.PackageRoot, matching)
	if err := os.WriteFile(filepath.Join(req.PackageRoot, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action, req.Agents = ActionRepair, []string{"claude", "codex"}
	for _, somePreviewed := range []bool{false, true} {
		req.PackageRoots = nil
		if somePreviewed {
			req.PackageRoots = map[string]string{"claude": matching}
		}
		before := policyConflictFiles(t, filepath.Dir(req.ControlRoot))
		plan, err := Plan(testCtx(t), req)
		if err != nil || !plan.Ready {
			t.Fatalf("repair plan changed existing admission: %+v %v", plan, err)
		}
		rows, err := ConfirmationRows(plan)
		if err != nil {
			t.Fatal(err)
		}
		const warning = "no repair preview succeeded for claude, codex"
		if strings.Contains(plan.Text, warning) == somePreviewed || strings.Contains(strings.Join(rows, "\n"), warning) == somePreviewed {
			t.Errorf("repair preview warning differs from actual success (some=%t): text=%q rows=%v", somePreviewed, plan.Text, rows)
		}
		if after := policyConflictFiles(t, filepath.Dir(req.ControlRoot)); !reflect.DeepEqual(before, after) {
			t.Fatal("repair Plan changed installed bindings, receipts or package files")
		}
	}
}
