//go:build linux || darwin

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func bootstrapMigrationFixture(t *testing.T) (confirmedBootstrapIntent, wizardCLIEnv, []byte) {
	t.Helper()
	ctx := setupCommandContext(t)
	env := newWizardCLIEnv(t, ctx, false)
	state := filepath.Join(filepath.Dir(env.control), "uap", "state", "state-v2.json")
	if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"schema_version\":4,\"installations\":[]}\n")
	if err := os.WriteFile(state, original, 0600); err != nil {
		t.Fatal(err)
	}
	args, err := parseSetupProducts([]string{"confirm", "--products", "claude", "--agent-notify", "--control-root", env.control, "--claude-config", env.claudeConfig, "--claude-executable", env.probe})
	if err != nil {
		t.Fatal(err)
	}
	provenance := selectorProvenance{Version: "TEST", SourceCommit: strings.Repeat("1", 40), SHA256: strings.Repeat("2", 64), Stage: []byte(env.root)}
	intent, rows, err := buildConfirmedBootstrapIntent(ctx, args, productEnvironment{Home: env.root, PATH: "", DefaultControlRoot: env.control, Values: map[string]string{}, Config: config.SnapshotEnv()}, provenance)
	if err != nil {
		t.Fatal(err)
	}
	if intent.StateMigration == nil || !intent.StateMigration.Required || !strings.Contains(strings.Join(rows, "\n"), "schema 4 -> 5") {
		t.Fatalf("upgrade missing from combined confirmation: %+v %v", intent.StateMigration, rows)
	}
	return intent, env, original
}

func TestBootstrapRegistryMigrationRoundTripAndParentApply(t *testing.T) {
	intent, env, original := bootstrapMigrationFixture(t)
	path := filepath.Join(env.root, "selector-intent.json")
	before, _, err := installruntime.ReadOwnership(env.control)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBootstrapIntent(path, intent); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBootstrapIntent(path, intent.Provenance)
	if err != nil || loaded.StateMigration == nil || *loaded.StateMigration != *intent.StateMigration {
		t.Fatalf("migration plan not preserved: %+v %v", loaded.StateMigration, err)
	}
	body, err := os.ReadFile(intent.StateMigration.StateFile)
	if err != nil || !bytes.Equal(body, original) {
		t.Fatalf("preparation migrated registry: %v", err)
	}
	var scalars, output, prompts bytes.Buffer
	if err := writeIntentScalars(&scalars, loaded); err != nil || !bytes.Contains(scalars.Bytes(), []byte("state-migration-required\x00true\x00")) {
		t.Fatalf("migration scalar: %q %v", scalars.Bytes(), err)
	}
	if err := applyBootstrapStateMigration(setupCommandContext(t), loaded, &output, &prompts); err != nil {
		t.Fatal(err)
	}
	if output.String() != "migrated\n" || !strings.Contains(prompts.String(), "UAP registry upgraded: schema 4 -> 5") || !strings.Contains(prompts.String(), "Backup:") {
		t.Fatalf("parent diagnostics: stdout=%s stderr=%s", output.String(), prompts.String())
	}
	backups, err := filepath.Glob(intent.StateMigration.StateFile + ".schema4.backup-agentplugins-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v %v", backups, err)
	}
	backedUp, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(backedUp, original) {
		t.Fatalf("backup lost original bytes: %v", err)
	}
	after, _, err := installruntime.ReadOwnership(env.control)
	if err != nil || after.Generation != before.Generation || after.PendingMutation != nil {
		t.Fatalf("migration wrote primary registrations: %+v %v", after, err)
	}
	entries, err := os.ReadDir(env.claudeConfig)
	if err != nil || len(entries) != 0 {
		t.Fatalf("migration touched selected profile: %v %v", entries, err)
	}
}

func TestBootstrapRegistryChangedAfterConfirmationRefusesBeforeWrites(t *testing.T) {
	intent, env, original := bootstrapMigrationFixture(t)
	changed := append(append([]byte(nil), original...), '\n')
	if err := os.WriteFile(intent.StateMigration.StateFile, changed, 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := installruntime.ReadOwnership(env.control)
	if err != nil {
		t.Fatal(err)
	}
	var output, prompts bytes.Buffer
	err = applyBootstrapStateMigration(setupCommandContext(t), intent, &output, &prompts)
	if !errors.Is(err, uapinstaller.ErrPlanChanged) || !strings.Contains(err.Error(), "confirm the new installation plan") {
		t.Fatalf("stale confirmation: %v", err)
	}
	backups, _ := filepath.Glob(intent.StateMigration.StateFile + ".schema*.backup-agentplugins-*")
	if len(backups) != 0 || output.Len() != 0 {
		t.Fatalf("stale apply wrote: backups=%v output=%s", backups, output.String())
	}
	after, _, err := installruntime.ReadOwnership(env.control)
	if err != nil || after.Generation != before.Generation {
		t.Fatalf("stale apply changed registration: %v", err)
	}
}

func TestSetupProductsRegistryMigrationCommandGrammar(t *testing.T) {
	valid := []string{"migration-apply", "--intent-file", filepath.Join(t.TempDir(), "intent.json")}
	if parsed, err := parseSetupProducts(valid); err != nil || parsed.Operation != "migration-apply" {
		t.Fatalf("command rejected: %+v %v", parsed, err)
	}
	for _, args := range [][]string{{"migration-apply"}, append(append([]string(nil), valid...), "--yes"), append(append([]string(nil), valid...), "--products", "claude")} {
		if _, err := parseSetupProducts(args); err == nil {
			t.Fatalf("extra authority accepted: %v", args)
		}
	}
}

func TestDirectWizardLegacyRegistryCancellationIsReadOnly(t *testing.T) {
	ctx := setupCommandContext(t)
	env := newWizardCLIEnv(t, ctx, false)
	path := filepath.Join(filepath.Dir(env.control), "uap", "state", "state-v2.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"schema_version":4,"installations":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := installruntime.ReadOwnership(env.control)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--action", "install", "--agents", "codex", "--hooks", "false", "--agent-notify", "true", "--package", env.pkg, "--control-root", env.control, "--runtime-root", env.runtime, "--global-config", env.global, "--codex-home", env.codexHome, "--client-executable", env.probe, "--helper", env.probe, "--scope-root", env.scope}
	var output, progress bytes.Buffer
	code := executeSetupWizardWith(ctx, args, &output, &progress, strings.NewReader("n\n"), true)
	if code != 0 || !strings.Contains(output.String(), "UAP registry upgrade: schema 4 -> 5") || !strings.Contains(output.String(), "cancelled") {
		t.Fatalf("direct cancellation: code=%d stdout=%s stderr=%s", code, output.String(), progress.String())
	}
	afterBody, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(afterBody, original) {
		t.Fatalf("cancel changed registry: %v", err)
	}
	after, _, err := installruntime.ReadOwnership(env.control)
	if err != nil || after.Generation != before.Generation || after.PendingMutation != nil {
		t.Fatalf("cancel changed registration: %+v %v", after, err)
	}
	backups, _ := filepath.Glob(path + ".schema*.backup-agentplugins-*")
	if len(backups) != 0 {
		t.Fatalf("cancel created backup: %v", backups)
	}
}

func TestSetupProductsRegistryMigrationCapabilities(t *testing.T) {
	var output, prompts bytes.Buffer
	code := runSetupProducts([]string{"migration-capabilities"}, strings.NewReader(""), &output, &prompts)
	if code != 0 || output.String() != "state-migration-v1\n" || prompts.Len() != 0 {
		t.Fatalf("capability: code=%d stdout=%q stderr=%q", code, output.String(), prompts.String())
	}
	if _, err := parseSetupProducts([]string{"migration-capabilities", "--yes"}); err == nil {
		t.Fatal("capability accepted effect arguments")
	}
}

func TestRegistryMigrationDiagnosticsExplainDistinctNextSteps(t *testing.T) {
	for _, tc := range []struct {
		reason, want string
		cause        error
	}{
		{"state_migration_corrupt", "Restore a valid", uapinstaller.ErrStateCorrupt},
		{"state_migration_future", "newer installer", &uapinstaller.UnsupportedStateSchemaError{SourceSchema: 99, TargetSchema: 5}},
		{"state_migration_pending", "previous installer", uapinstaller.ErrRecoveryRequired},
		{"state_migration_changed", "confirm", uapinstaller.ErrPlanChanged},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			diagnostic := bootstrapStateMigrationError(tc.cause).Error()
			if !strings.Contains(diagnostic, tc.want) {
				t.Fatalf("bootstrap next step missing: %s", diagnostic)
			}
			var output bytes.Buffer
			writeSetupWizardSummary(&output, setupwizard.Result{Action: "install", Outcome: "incomplete", Reason: tc.reason})
			if !strings.Contains(output.String(), tc.want) || strings.Contains(output.String(), "Resume saved activation") || strings.Contains(output.String(), "retry:") {
				t.Fatalf("wizard migration advice: %s", output.String())
			}
		})
	}
}

func TestBootstrapChangedInvalidRegistryStillRequiresReconfirmation(t *testing.T) {
	for _, body := range []string{`{`, `{"schema_version":99,"installations":[]}`} {
		t.Run(body, func(t *testing.T) {
			intent, _, _ := bootstrapMigrationFixture(t)
			if err := os.WriteFile(intent.StateMigration.StateFile, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			var output, prompts bytes.Buffer
			err := applyBootstrapStateMigration(setupCommandContext(t), intent, &output, &prompts)
			if !errors.Is(err, uapinstaller.ErrPlanChanged) || !strings.Contains(err.Error(), "confirm the new installation plan") {
				t.Fatalf("changed registry was adopted: %v", err)
			}
			backups, _ := filepath.Glob(intent.StateMigration.StateFile + ".schema*.backup-agentplugins-*")
			if len(backups) != 0 || output.Len() != 0 {
				t.Fatalf("changed registry wrote backups=%v output=%q", backups, output.String())
			}
		})
	}
}
