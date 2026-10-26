//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
)

type committedTimeoutRunner struct {
	listingRunner
	fail         bool
	calls        atomic.Int64
	onFailure    func()
	failureReady func() bool
}

func (r *committedTimeoutRunner) Run(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	r.calls.Add(1)
	for _, variable := range command.Env {
		if strings.HasPrefix(variable, "CODEX_HOME=") {
			return (processadapter.OS{}).Run(ctx, command)
		}
	}
	listed, err := r.listingRunner.Run(ctx, command)
	if r.fail && err == nil && !bytes.Equal(listed.Stdout, []byte("[]")) && (r.failureReady == nil || r.failureReady()) {
		if r.onFailure != nil {
			r.onFailure()
		}
		// This branch performs only the inert filesystem listing above. Model
		// a private expired probe with positive no-child evidence; an empty result
		// means unknown process cleanup and must still cancel an in-flight sibling.
		started := time.Now()
		probe, cancel := context.WithTimeout(ctx, time.Nanosecond)
		defer cancel()
		<-probe.Done()
		return ports.CommandResult{NoChildStarted: true}, &domain.IsolatedProbeTimeout{
			Operation: "TEST closed Claude listing", Budget: time.Nanosecond, Elapsed: time.Since(started), Err: probe.Err(),
		}
	}
	return listed, err
}

// This regression reaches actual managed records, acquired source and CLI
// verification. Before the fix Codex becomes "failed", recovery duplicates the
// staging ZIP, and a completed sibling loses its completed attempt fact.
func TestActivationPartialRecoveryUsesAcquiredSource(t *testing.T) {
	for _, row := range []struct {
		name       string
		order      []string
		sequential bool
	}{{"claude-first", []string{"claude", "codex"}, false}, {"codex-first", []string{"codex", "claude"}, false}, {"sequential-codex-first", []string{"codex", "claude"}, true}} {
		t.Run(row.name, func(t *testing.T) {
			order := row.order
			control, runtimeRoot, global, _, _ := managedRuntime(t)
			root := filepath.Dir(control)
			probe := buildProbe(t)
			nativeCLI := buildRecoveryCodexCLI(t)
			archive := filepath.Join(root, "disposable.zip")
			built, err := portableasset.Build(portableasset.BuildRequest{Version: "1.43.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: probe, OutputRoot: filepath.Join(root, "source"), Archive: archive})
			if err != nil {
				t.Fatal(err)
			}
			claudeRoot, codexRoot := filepath.Join(root, "claude"), filepath.Join(root, "codex")
			for _, path := range []string{claudeRoot, codexRoot, filepath.Join(root, "scope")} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			runner := &committedTimeoutRunner{listingRunner: listingRunner{configRoot: claudeRoot}, fail: true}
			req := Request{Action: ActionInstall, Agents: order, Yes: true, Hooks: boolPtr(false), AgentNotify: boolPtr(true), PackageRoot: archive, PackageSHA256: built.ArchiveSHA256, ReleaseVersion: "1.43.0", ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, ClaudeConfig: claudeRoot, CodexHome: codexRoot, ClientExecutable: nativeCLI, Helper: probe, ScopeRoot: filepath.Join(root, "scope"), ClaudeRunner: runner}
			if row.sequential {
				// A live Codex plus an unbound Claude takes the actual per-client
				// dispatcher, whose failure display selects only Claude.
				initial := req
				initial.Agents = []string{"codex"}
				if installed, err := Run(testCtx(t), initial); err != nil || installed.Outcome != "completed" {
					t.Fatalf("initial Codex: %+v %v", installed, err)
				}
			}
			got, err := Run(testCtx(t), req)
			if !errors.Is(err, context.DeadlineExceeded) || got.Reason != "activation_incomplete" {
				t.Fatalf("partial result: %+v %v", got, err)
			}
			byClient := map[string]TargetResult{}
			for _, target := range got.Targets {
				byClient[target.Client] = target
			}
			wantClaude := "activation_timeout"
			if row.sequential {
				// The SDK's single-client path retains lifecycle facts without a
				// typed current group attempt; do not infer timeout from its error.
				wantClaude = "incomplete"
			}
			if byClient["claude"].Outcome != wantClaude || byClient["claude"].Materialization != "materialized" {
				t.Fatalf("Claude attempt lost: %+v", byClient)
			}
			if byClient["codex"].Outcome != "completed" || (!row.sequential && byClient["codex"].Materialization != "materialized") {
				t.Fatalf("Codex attempt: %+v", byClient)
			}
			if order[0] == "codex" && !row.sequential {
				var saved portablesetup.ResultError
				if !errors.As(err, &saved) {
					t.Fatal("typed result missing")
				}
				for i := range saved.Result.Targets {
					if saved.Result.Targets[i].ClientID == "codex" {
						saved.Result.Targets[i].Materialization = ""
					}
				}
				unknown := groupNotifyFailed([]portable.Integration{portable.Codex, portable.Claude}, req, Result{Action: "install"}, saved)
				if unknown.Targets[0].Outcome != "incomplete" || unknown.Targets[0].Reason != "state_requires_inspection" || unknown.Targets[0].GroupPhase != "external_completed" || unknown.Targets[1].Outcome != "activation_timeout" {
					t.Fatalf("unknown saved state fabricated success or lost attempt: %+v", unknown.Targets)
				}
			}
			if len(got.Command) != 0 || len(got.NextActions) != 1 || len(got.NextActions[0].Command) == 0 {
				t.Fatalf("single validated recovery missing: %+v", got)
			}
			if row.sequential && strings.Join(got.NextActions[0].Agents, ",") != "claude" {
				t.Fatalf("sequential failure display selection: %+v", got.NextActions)
			}
			statePath := filepath.Join(root, "uap", "state", "state-v2.json")
			body, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			var before domain.StateFileV2
			if err := json.Unmarshal(body, &before); err != nil {
				t.Fatal(err)
			}
			if len(before.Installations) != 1 || len(before.Installations[0].Clients) != 2 {
				t.Fatalf("committed registrations missing: %+v", before)
			}
			traceBefore, _ := os.ReadFile(filepath.Join(codexRoot, "TEST-trace"))
			if err := os.Remove(archive); err != nil {
				t.Fatal(err)
			}
			command := got.NextActions[0].Command
			if command[0] != "setup-notifications" || command[1] != "wizard" {
				t.Fatalf("unsupported recovery prefix %v", command)
			}
			expected := map[string]string{
				"--action": "install", "--agents": strings.Join(order, ","), "--hooks": "false", "--agent-notify": "true",
				"--package": before.Installations[0].Source.CanonicalSource, "--control-root": control, "--runtime-root": runtimeRoot,
				"--global-config": global, "--codex-home": codexRoot, "--claude-config": claudeRoot, "--client-executable": nativeCLI,
				"--helper": probe, "--scope-root": filepath.Join(root, "scope"), "--installation-id": before.Installations[0].InstallationID,
			}
			flags := map[string]string{}
			confirmed := false
			for i := 2; i < len(command); i++ {
				if command[i] == "--yes" {
					if confirmed {
						t.Fatal("duplicate confirmation")
					}
					confirmed = true
					continue
				}
				if i+1 >= len(command) {
					t.Fatalf("truncated recovery %v", command)
				}
				key, value := command[i], command[i+1]
				i++
				if want, ok := expected[key]; !ok || want != value || flags[key] != "" {
					t.Fatalf("recovery changed %s=%q (want %q)", key, value, want)
				}
				flags[key] = value
			}
			if !confirmed || len(flags) != len(expected) || !packageStillUsable(flags["--package"]) {
				t.Fatalf("unusable recovery %v", command)
			}
			// Execute exactly the request represented by every returned flag. Hidden
			// fields from the original request cannot make an unusable argv pass.
			req = Request{Action: Action(flags["--action"]), Agents: strings.Split(flags["--agents"], ","), Yes: confirmed, Hooks: boolPtr(false), AgentNotify: boolPtr(true),
				PackageRoot: flags["--package"], ControlRoot: flags["--control-root"], RuntimeRoot: flags["--runtime-root"], GlobalConfig: flags["--global-config"],
				CodexHome: flags["--codex-home"], ClaudeConfig: flags["--claude-config"], ClientExecutable: flags["--client-executable"], Helper: flags["--helper"],
				ScopeRoot: flags["--scope-root"], InstallationID: flags["--installation-id"], ClaudeRunner: runner}
			// Changed source identity cannot produce any new managed effect.
			bad := req
			bad.TreeDigest = "changed-TEST-identity"
			files := policyConflictFiles(t, root)
			if rejected, e := Run(testCtx(t), bad); e == nil || rejected.Outcome == "completed" {
				t.Fatalf("changed source admitted: %+v %v", rejected, e)
			}
			for path, want := range files {
				if actual, e := os.ReadFile(path); e != nil || !bytes.Equal(actual, want) {
					t.Fatalf("identity conflict changed %s: %v", path, e)
				}
			}
			runner.fail = false
			resumed, err := Run(testCtx(t), req)
			if err != nil || resumed.Outcome != "completed" {
				t.Fatalf("acquired resume: %+v %v", resumed, err)
			}
			traceAfter, err := os.ReadFile(filepath.Join(codexRoot, "TEST-trace"))
			if err != nil || strings.Count(string(traceAfter), "plugin add ") != 1 {
				t.Fatalf("activation replayed: %s; %v", traceAfter, err)
			}
			if strings.Count(string(traceBefore), "plugin add ") != 1 {
				t.Fatalf("completed sibling absent from trace: %s", traceBefore)
			}
			body, err = os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			var after domain.StateFileV2
			if err := json.Unmarshal(body, &after); err != nil {
				t.Fatal(err)
			}
			if len(after.Installations) != 1 || len(after.Installations[0].Clients) != 2 {
				t.Fatalf("duplicate registration: %+v", after)
			}
			for _, old := range before.Installations[0].Clients {
				found := false
				for _, binding := range after.Installations[0].Clients {
					if old.ClientBindingID == binding.ClientBindingID && old.ClientID == binding.ClientID {
						found = true
					}
				}
				if !found {
					t.Fatalf("resume rebound %s", old.ClientID)
				}
			}
			if snap, e := installruntime.ReadInstalledSnapshot(control); e != nil || snap.Ledger.PendingMutation != nil {
				t.Fatalf("resume left pending: %+v %v", snap, e)
			}
			if _, e := portablesetup.ReadIntent(control); !os.IsNotExist(e) {
				t.Fatalf("completed intent retained: %v", e)
			}
		})
	}
}

func TestMigrationActivationRecoveryPreservesSourceValidation(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "acquired-source"
		if missing {
			name = "missing-source"
		}
		t.Run(name, func(t *testing.T) {
			control, runtimeRoot, _, _, _ := managedRuntime(t)
			root := filepath.Dir(control)
			probe := buildProbe(t)
			pkg := filepath.Join(root, "package")
			writePackage(t, pkg, probe)
			legacy := filepath.Join(runtimeRoot, "global", "config.json")
			canonical := filepath.Join(root, "canonical", "config.json")
			profile, scope := filepath.Join(root, "claude"), filepath.Join(root, "scope")
			for _, path := range []string{profile, scope} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			runner := &committedTimeoutRunner{listingRunner: listingRunner{configRoot: profile}}
			req := Request{Action: ActionInstall, Agents: []string{"claude"}, Yes: true, Hooks: boolPtr(false), AgentNotify: boolPtr(true),
				PackageRoot: pkg, ReleaseVersion: "1.0.0", ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: legacy, ClaudeConfig: profile,
				ClientExecutable: probe, Helper: probe, ScopeRoot: scope, ClaudeRunner: runner}
			installed, err := Run(testCtx(t), req)
			if err != nil || installed.Outcome != "completed" {
				t.Fatalf("legacy install: %+v %v", installed, err)
			}
			t.Setenv(config.OverrideEnv, canonical)
			req.Action, req.GlobalConfig, req.InstallationID = ActionRepair, "", installed.InstallationID
			req.TreeDigest = inspectedWizardBinding(t, testCtx(t), control, "claude").TreeDigest
			plan, err := Plan(testCtx(t), req)
			if err != nil || !plan.Ready || len(plan.Request.MigrationBindings) != 1 {
				t.Fatalf("migration plan: %+v %v", plan, err)
			}
			runner.fail = true
			runner.failureReady = func() bool {
				snap, err := installruntime.ReadInstalledSnapshot(control)
				if err != nil {
					t.Fatal(err)
				}
				return portable.ExactCommittedBinding(snap.Ledger, plan.Request.MigrationBindings["claude"].New)
			}
			if missing {
				// Lose the source only after external verification reaches the
				// committed new registration. Presentation must refuse recovery.
				runner.onFailure = func() {
					if err := os.Rename(pkg, pkg+"-removed"); err != nil {
						t.Fatal(err)
					}
					runner.onFailure = nil
				}
			}
			failed, err := Run(testCtx(t), plan.Request)
			if !errors.Is(err, context.DeadlineExceeded) || failed.Reason != "migration_activation_pending" || len(failed.NextActions) != 1 {
				t.Fatalf("migration failure: %+v %v", failed, err)
			}
			if len(failed.Targets) != 1 || failed.Targets[0].Materialization != "materialized" || failed.Targets[0].Activation != "failed" {
				t.Fatalf("committed migration facts lost: %+v", failed.Targets)
			}
			next := failed.NextActions[0]
			if missing {
				if len(next.Command) != 0 || next.Reason != "recovery_unavailable" {
					t.Fatalf("unsafe migration recovery advertised: %+v", next)
				}
			} else {
				if len(next.Command) == 0 || next.Reason != "migration_activation_pending" {
					t.Fatalf("validated migration recovery lost: %+v", next)
				}
				found := false
				for i := 0; i+1 < len(next.Command); i++ {
					if next.Command[i] == "--package" && next.Command[i+1] == pkg {
						found = true
					}
				}
				if !found {
					t.Fatalf("migration recovery lost acquired source: %v", next.Command)
				}
			}
			intent, err := portablesetup.ReadIntent(control)
			snap, snapErr := installruntime.ReadInstalledSnapshot(control)
			if err != nil || snapErr != nil || snap.Ledger.PendingMutation == nil || snap.Ledger.PendingMutation.ID != intent.SetupIntentID || snap.Ledger.PendingMutation.Owner != "existing-installer" {
				t.Fatalf("migration recovery lost owned intent: %+v %v %v", snap, err, snapErr)
			}
		})
	}
}

func buildRecoveryCodexCLI(t *testing.T) string {
	t.Helper()
	code := `package main
import ("encoding/json"; "os"; "path/filepath"; "strings")
func main() {
 root:=os.Getenv("CODEX_HOME"); args:=os.Args[1:]
 f,_:=os.OpenFile(filepath.Join(root,"TEST-trace"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600); if f!=nil { f.WriteString(strings.Join(args," ")+"\n"); f.Close() }
 if len(args)>=3 && args[0]=="plugin" && args[1]=="add" { os.WriteFile(filepath.Join(root,"TEST-plugin"),[]byte(args[2]),0600) }
 if len(args)>=2 && args[0]=="plugin" && args[1]=="list" {
  id,_:=os.ReadFile(filepath.Join(root,"TEST-plugin")); installed:=[]map[string]any{}
  if len(id)>0 { name,market,_:=strings.Cut(string(id),"@"); installed=append(installed,map[string]any{"pluginId":string(id),"name":name,"marketplaceName":market,"installed":true,"enabled":true}) }
  json.NewEncoder(os.Stdout).Encode(map[string]any{"installed":installed}); return
 }
 json.NewEncoder(os.Stdout).Encode(map[string]any{})
}`
	return testenv.GoFixture(t, code)
}
