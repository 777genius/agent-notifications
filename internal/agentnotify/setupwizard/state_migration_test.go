//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func legacyRegistryRequest(t *testing.T, body []byte) (Request, uint64) {
	t.Helper()
	control, runtimeRoot, global, _, generation := managedRuntime(t)
	probe := buildProbe(t)
	pkg := filepath.Join(filepath.Dir(control), "package")
	writePackage(t, pkg, probe)
	profile := filepath.Join(filepath.Dir(control), "claude-profile")
	scope := filepath.Join(filepath.Dir(control), "scope")
	for _, dir := range []string{profile, scope, filepath.Dir(uapStateFile(control))} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(uapStateFile(control), body, 0600); err != nil {
		t.Fatal(err)
	}
	return Request{Action: ActionInstall, Agents: []string{"claude"}, Yes: true,
		Hooks: boolPtr(false), PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtimeRoot,
		GlobalConfig: global, ClaudeConfig: profile, ClientExecutable: probe, Helper: probe, ScopeRoot: scope, ClaudeRunner: listingRunner{configRoot: profile}}, generation
}

func registryBackups(t *testing.T, control string) []string {
	t.Helper()
	backups, err := filepath.Glob(uapStateFile(control) + ".schema*.backup-agentplugins-*")
	if err != nil {
		t.Fatal(err)
	}
	return backups
}

func assertRegistryPreflightUnchanged(t *testing.T, req Request, generation uint64, body []byte) {
	t.Helper()
	after, err := os.ReadFile(uapStateFile(req.ControlRoot))
	if err != nil || !bytes.Equal(after, body) {
		t.Fatalf("registry changed: %v", err)
	}
	ledger, recovering, err := installruntime.ReadOwnership(req.ControlRoot)
	if err != nil || recovering || ledger.Generation != generation || ledger.PendingMutation != nil {
		t.Fatalf("preflight changed managed ownership: %+v recovering=%t err=%v", ledger, recovering, err)
	}
	entries, err := os.ReadDir(req.ClaudeConfig)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preflight changed client profile: %v %v", entries, err)
	}
	if backups := registryBackups(t, req.ControlRoot); len(backups) != 0 {
		t.Fatalf("preflight created backup: %v", backups)
	}
}

// Regression first run against the pinned old dependency: schemas 2, 3 and 4
// failed at mutation readiness after publishing the managed setup reservation.
func TestWizardMigratesLegacyRegistryBeforeClientWrites(t *testing.T) {
	for _, schema := range []int{2, 3, 4} {
		t.Run(fmt.Sprintf("schema-%d", schema), func(t *testing.T) {
			original := []byte(fmt.Sprintf("{\"schema_version\":%d,\"installations\":[]}\n", schema))
			req, generation := legacyRegistryRequest(t, original)
			plan, err := Plan(testCtx(t), req)
			if err != nil || !plan.Ready || plan.Request.StateMigration == nil || !plan.Request.StateMigration.Required || !strings.Contains(plan.Text, fmt.Sprintf("schema %d ->", schema)) {
				t.Fatalf("migration preflight: %+v %v", plan, err)
			}
			assertRegistryPreflightUnchanged(t, req, generation, original)
			migrationReported := false
			plan.Request.StateMigrationProgress = func(outcome StateMigrationOutcome) {
				migrationReported = true
				ledger, _, err := installruntime.ReadOwnership(req.ControlRoot)
				if err != nil || ledger.Generation != generation || ledger.PendingMutation != nil {
					t.Errorf("client work preceded migration: %+v %v", ledger, err)
				}
				entries, err := os.ReadDir(req.ClaudeConfig)
				if err != nil || len(entries) != 0 {
					t.Errorf("client registration preceded migration: %v %v", entries, err)
				}
				if outcome.SourceSchema != schema || outcome.TargetSchema != 5 || outcome.BackupPath == "" {
					t.Errorf("migration diagnostics: %+v", outcome)
				}
			}
			result, err := Run(testCtx(t), plan.Request)
			if err != nil || result.Outcome != "completed" || result.StateMigration == nil || !migrationReported {
				t.Fatalf("old registry upgrade/install: %+v %v", result, err)
			}
			backups := registryBackups(t, req.ControlRoot)
			if len(backups) != 1 {
				t.Fatalf("expected one backup, got %v", backups)
			}
			backup, err := os.ReadFile(backups[0])
			if err != nil || !bytes.Equal(original, backup) {
				t.Fatalf("backup differs from approved bytes: %v", err)
			}
			var state struct {
				Schema int `json:"schema_version"`
			}
			body, err := os.ReadFile(uapStateFile(req.ControlRoot))
			if err != nil || json.Unmarshal(body, &state) != nil || state.Schema != 5 {
				t.Fatalf("registry did not upgrade: %v", err)
			}
			retried, retryErr := Run(testCtx(t), req)
			if retryErr != nil || retried.ExitCode() != 0 || retried.StateMigration != nil || len(registryBackups(t, req.ControlRoot)) != 1 {
				t.Fatalf("retry repeated migration: %+v %v", retried, retryErr)
			}
		})
	}
}

func TestWizardRegistryChangeRequiresFreshConfirmation(t *testing.T) {
	original := []byte(`{"schema_version":4,"installations":[]}`)
	req, generation := legacyRegistryRequest(t, original)
	plan, err := Plan(testCtx(t), req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	changed := append(append([]byte(nil), original...), '\n')
	if err := os.WriteFile(uapStateFile(req.ControlRoot), changed, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Run(testCtx(t), plan.Request)
	if err == nil || result.Reason != "state_migration_changed" || result.Outcome != "conflict" || len(result.Command) != 0 || len(result.NextActions) != 0 {
		t.Fatalf("stale plan: %+v %v", result, err)
	}
	assertRegistryPreflightUnchanged(t, req, generation, changed)
}

func TestWizardRegistryFailureAndCancellationDoNotWriteClients(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		cancel             bool
	}{
		{"corrupt", `{"schema_version":4,"installations":[`, "state_migration_corrupt", false},
		{"future", `{"schema_version":999,"installations":[]}`, "state_migration_future", false},
		{"canceled", `{"schema_version":4,"installations":[]}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := []byte(tc.body)
			req, generation := legacyRegistryRequest(t, original)
			ctx := testCtx(t)
			if tc.cancel {
				plan, err := Plan(ctx, req)
				if err != nil || !plan.Ready {
					t.Fatalf("plan: %+v %v", plan, err)
				}
				req = plan.Request
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			result, err := Run(ctx, req)
			if err == nil || result.ExitCode() == 0 {
				t.Fatalf("unsafe registry admitted: %+v %v", result, err)
			}
			if !tc.cancel && (result.Reason != tc.reason || len(result.Command) != 0 || len(result.NextActions) != 0) {
				t.Fatalf("wrong migration recovery: %+v", result)
			}
			assertRegistryPreflightUnchanged(t, req, generation, original)
		})
	}
}

func TestBootstrapWorkerCannotMigrateRegistry(t *testing.T) {
	original := []byte(`{"schema_version":4,"installations":[]}`)
	req, generation := legacyRegistryRequest(t, original)
	req.BootstrapMCP = &BootstrapMCPSelection{}
	result, err := applyRequestedStateMigration(testCtx(t), &req)
	if err != uapinstaller.ErrMigrationRequired || result != nil {
		t.Fatalf("worker performed migration: %+v %v", result, err)
	}
	assertRegistryPreflightUnchanged(t, req, generation, original)
}

func TestWizardMigratedRegistryActivationRetryKeepsOneBackup(t *testing.T) {
	original := []byte(`{"schema_version":4,"installations":[]}`)
	req, _ := legacyRegistryRequest(t, original)
	runner := &committedTimeoutRunner{listingRunner: listingRunner{configRoot: req.ClaudeConfig}, fail: true}
	req.ClaudeRunner = runner
	first, err := Run(testCtx(t), req)
	if err == nil || first.Reason != "activation_incomplete" || first.StateMigration == nil || first.StateMigration.Outcome != "completed" {
		t.Fatalf("migration/activation failure were conflated: %+v %v", first, err)
	}
	if len(registryBackups(t, req.ControlRoot)) != 1 {
		t.Fatal("missing migration backup")
	}
	runner.fail = false
	retried, err := Run(testCtx(t), req)
	if err != nil || retried.Outcome != "completed" || retried.StateMigration != nil {
		t.Fatalf("saved activation retry: %+v %v", retried, err)
	}
	if len(registryBackups(t, req.ControlRoot)) != 1 {
		t.Fatal("activation retry migrated again")
	}
}

func TestWizardMigratesExistingRegistryWithoutLosingBinding(t *testing.T) {
	req, _ := legacyRegistryRequest(t, []byte(`{"schema_version":5,"installations":[]}`))
	initial, err := Run(testCtx(t), req)
	if err != nil || initial.Outcome != "completed" {
		t.Fatalf("initial install: %+v %v", initial, err)
	}
	before := inspectedWizardBinding(t, testCtx(t), req.ControlRoot, "claude")
	state, err := os.ReadFile(uapStateFile(req.ControlRoot))
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(state, []byte(`"schema_version": 5`), []byte(`"schema_version": 4`), 1)
	if bytes.Equal(state, legacy) {
		t.Fatal("fixture header not downgraded")
	}
	if err := os.WriteFile(uapStateFile(req.ControlRoot), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionRepair
	plan, err := Plan(testCtx(t), req)
	if err != nil || !plan.Ready || plan.Request.StateMigration == nil || plan.Request.StateMigration.Installations != 1 {
		t.Fatalf("existing registry plan: %+v %v", plan, err)
	}
	result, err := Run(testCtx(t), plan.Request)
	if err != nil || result.Outcome != "completed" || result.StateMigration == nil || result.StateMigration.Migrated != 1 {
		t.Fatalf("existing registry repair: %+v %v", result, err)
	}
	after := inspectedWizardBinding(t, testCtx(t), req.ControlRoot, "claude")
	if after.BindingID != before.BindingID || result.InstallationID != initial.InstallationID || after.TargetPath != before.TargetPath {
		t.Fatalf("migration changed binding identity: before=%+v after=%+v", before, after)
	}
	backups := registryBackups(t, req.ControlRoot)
	if len(backups) != 1 {
		t.Fatalf("backup count: %v", backups)
	}
	saved, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(saved, legacy) {
		t.Fatalf("existing state backup changed: %v", err)
	}
}
