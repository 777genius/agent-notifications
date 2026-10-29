//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
)

// Exercise real managed state and auto-update orchestration while replacing only
// the native Claude listing subprocess with the finite listing it would return.
type autoMigrationListingRunner struct{ profile string }

func (r autoMigrationListingRunner) Run(context.Context, ports.Command) (ports.CommandResult, error) {
	paths, err := filepath.Glob(filepath.Join(r.profile, "skills", "*", ".claude-plugin", "plugin.json"))
	if err != nil {
		return ports.CommandResult{}, err
	}
	listed := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return ports.CommandResult{}, err
		}
		var manifest map[string]any
		if err := json.Unmarshal(body, &manifest); err != nil {
			return ports.CommandResult{}, err
		}
		name, _ := manifest["name"].(string)
		listed = append(listed, map[string]any{"id": name + "@skills-dir", "version": manifest["version"], "scope": "user", "enabled": true, "installPath": filepath.Dir(filepath.Dir(path))})
	}
	body, err := json.Marshal(listed)
	return ports.CommandResult{Stdout: body}, err
}
func autoMigrationRequest(env wizardCLIEnv) setupwizard.Request {
	off, on := false, true
	return setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"claude"}, Hooks: &off, AgentNotify: &on, Yes: true,
		PackageRoot: env.pkg, ControlRoot: env.control, RuntimeRoot: env.runtime, GlobalConfig: env.global, ClaudeConfig: env.claudeConfig,
		ClientExecutable: env.probe, Helper: env.probe, ScopeRoot: env.scope, ClaudeRunner: autoMigrationListingRunner{profile: env.claudeConfig}}
}

func TestWizardInstallOrUpdateRetainsRegistryMigration(t *testing.T) {
	for _, tc := range []struct {
		name                                                string
		approved, lateFailure, preserve, lateRegistryChange bool
	}{
		{name: "version-update"}, {name: "approved-snapshot", approved: true}, {name: "preserved-existing-units", preserve: true}, {name: "later-plan-failure", lateFailure: true}, {name: "later-registry-change", lateRegistryChange: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := setupCommandContext(t)
			env := newWizardCLIEnv(t, ctx, false)
			req := autoMigrationRequest(env)
			first, err := setupwizard.Run(ctx, req)
			if err != nil || first.ExitCode() != 0 {
				t.Fatalf("initial install: %+v %v", first, err)
			}
			stateFile := filepath.Join(filepath.Dir(env.control), "uap", "state", "state-v2.json")
			state, err := os.ReadFile(stateFile)
			if err != nil {
				t.Fatal(err)
			}
			legacy := bytes.Replace(state, []byte(`"schema_version": 5`), []byte(`"schema_version": 4`), 1)
			if bytes.Equal(state, legacy) {
				t.Fatal("legacy fixture header not replaced")
			}
			if err := os.WriteFile(stateFile, legacy, 0600); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(env.pkg, "plugin.json")
			manifest, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			manifest = bytes.Replace(manifest, []byte(`"version":"1.0.0"`), []byte(`"version":"1.0.1"`), 1)
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(env.pkg, "skills", "agent-notifications", "SKILL.md"), []byte("---\nname: agent-notifications\ndescription: New auto-update revision\n---\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.approved {
				approved, err := setupwizard.PrepareStateMigration(ctx, env.control)
				if err != nil {
					t.Fatal(err)
				}
				req.StateMigration = &approved
			}
			reported, completed := 0, 0
			req.StateMigrationProgress = func(setupwizard.StateMigrationOutcome) { reported++ }
			req.Progress = func(phase string) {
				if phase == "complete" {
					completed++
					if tc.lateRegistryChange {
						if err := os.WriteFile(stateFile, legacy, 0600); err != nil {
							t.Error(err)
						}
					}
					if tc.lateFailure {
						if err := os.RemoveAll(env.pkg); err != nil {
							t.Error(err)
						}
					}
				}
			}
			result, err := runInstallOrUpdate(ctx, req, tc.preserve)
			if tc.lateRegistryChange {
				if err == nil || result.Reason != "state_migration_changed" || result.Outcome != "conflict" || len(result.Command) != 0 || len(result.NextActions) != 0 {
					t.Fatalf("second unapproved migration was admitted: %+v %v", result, err)
				}
			} else if tc.lateFailure {
				if err == nil || result.ExitCode() == 0 || completed == 0 {
					t.Fatalf("missing later failure after completed phase: %+v %v completed=%d", result, err, completed)
				}
			} else if err != nil || result.ExitCode() != 0 {
				t.Fatalf("auto version update: %+v %v", result, err)
			}
			backups, globErr := filepath.Glob(stateFile + ".schema4.backup-agentplugins-*")
			if globErr != nil || len(backups) != 1 || reported != 1 {
				t.Fatalf("migration proof: backups=%v reports=%d err=%v", backups, reported, globErr)
			}
			if result.StateMigration == nil || result.StateMigration.Outcome != "completed" || result.StateMigration.BackupPath != backups[0] || result.StateMigration.SourceSchema != 4 || result.StateMigration.TargetSchema != 5 {
				t.Fatalf("completed migration lost across auto phases: %+v", result)
			}
			backup, err := os.ReadFile(backups[0])
			if err != nil || !bytes.Equal(backup, legacy) {
				t.Fatalf("backup differs from approved legacy state: %v", err)
			}
		})
	}
}

func TestWizardInstallOrUpdateRegistryPreflight(t *testing.T) {
	for _, tc := range []struct{ name, body, reason, advice string }{
		{"corrupt", `{"schema_version":5,"PRIVATE_CONFIG_CANARY":true,"installations":[]}`, "state_migration_corrupt", "Restore a valid"},
		{"future", `{"schema_version":999,"installations":[]}`, "state_migration_future", "newer installer"},
		{"pending", `{"schema_version":4,"installations":[],"transaction_receipts":[{"operation_id":"pending-auto","client_binding_id":"binding-auto","sequence":1,"mutation_type":"install","phase":"state_committed"}]}`, "state_migration_pending", "previous installer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := setupCommandContext(t)
			env := newWizardCLIEnv(t, ctx, false)
			req := autoMigrationRequest(env)
			stateFile := filepath.Join(filepath.Dir(env.control), "uap", "state", "state-v2.json")
			if err := os.MkdirAll(filepath.Dir(stateFile), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stateFile, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			before, _, err := installruntime.ReadOwnership(env.control)
			if err != nil {
				t.Fatal(err)
			}
			result, err := runInstallOrUpdate(ctx, req, false)
			if err == nil || result.Action != "install" || result.Reason != tc.reason || len(result.Command) != 0 || len(result.NextActions) != 0 {
				t.Fatalf("unsafe auto preflight: %+v %v", result, err)
			}
			var output bytes.Buffer
			writeSetupWizardSummary(&output, result)
			if !strings.Contains(output.String(), tc.advice) || strings.Contains(output.String(), "PRIVATE_CONFIG_CANARY") || strings.Contains(output.String(), "Resume saved activation") {
				t.Fatalf("unsafe migration advice: %s", output.String())
			}
			body, readErr := os.ReadFile(stateFile)
			if readErr != nil || !bytes.Equal(body, []byte(tc.body)) {
				t.Fatalf("preflight changed registry: %v", readErr)
			}
			after, _, readErr := installruntime.ReadOwnership(env.control)
			if readErr != nil || before.Generation != after.Generation || after.PendingMutation != nil {
				t.Fatalf("preflight changed ownership: %+v %v", after, readErr)
			}
			entries, readErr := os.ReadDir(env.claudeConfig)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("preflight changed client: %v %v", entries, readErr)
			}
			backups, _ := filepath.Glob(stateFile + ".schema*.backup-agentplugins-*")
			if len(backups) != 0 {
				t.Fatalf("preflight created backups: %v", backups)
			}
		})
	}
}

func TestWizardInstallOrUpdateRejectsChangedMigrationApproval(t *testing.T) {
	for _, replacement := range []string{"{\"schema_version\":4,\"installations\":[]}\n", `{`, `{"schema_version":99,"installations":[]}`} {
		t.Run(replacement, func(t *testing.T) {
			ctx := setupCommandContext(t)
			env := newWizardCLIEnv(t, ctx, false)
			req := autoMigrationRequest(env)
			stateFile := filepath.Join(filepath.Dir(env.control), "uap", "state", "state-v2.json")
			if err := os.MkdirAll(filepath.Dir(stateFile), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"schema_version":4,"installations":[]}`)
			if err := os.WriteFile(stateFile, original, 0600); err != nil {
				t.Fatal(err)
			}
			approved, err := setupwizard.PrepareStateMigration(ctx, env.control)
			if err != nil {
				t.Fatal(err)
			}
			req.StateMigration = &approved
			if err := os.WriteFile(stateFile, []byte(replacement), 0600); err != nil {
				t.Fatal(err)
			}
			before, _, err := installruntime.ReadOwnership(env.control)
			if err != nil {
				t.Fatal(err)
			}
			result, err := runInstallOrUpdate(ctx, req, false)
			if err == nil || result.Reason != "state_migration_changed" || result.Outcome != "conflict" || len(result.Command) != 0 || len(result.NextActions) != 0 {
				t.Fatalf("auto mode replaced approved snapshot: %+v %v", result, err)
			}
			after, _, readErr := installruntime.ReadOwnership(env.control)
			if readErr != nil || after.Generation != before.Generation || after.PendingMutation != nil {
				t.Fatalf("stale approval changed ownership: %+v %v", after, readErr)
			}
			backups, _ := filepath.Glob(stateFile + ".schema*.backup-agentplugins-*")
			if len(backups) != 0 {
				t.Fatalf("stale approval made backup: %v", backups)
			}
		})
	}
}
