//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
)

type committedTimeoutRunner struct {
	listingRunner
	fail  bool
	calls int
}

func (r *committedTimeoutRunner) Run(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	r.calls++
	for _, variable := range command.Env {
		if strings.HasPrefix(variable, "CODEX_HOME=") {
			return (processadapter.OS{}).Run(ctx, command)
		}
	}
	listed, err := r.listingRunner.Run(ctx, command)
	if r.fail && err == nil && !bytes.Equal(listed.Stdout, []byte("[]")) {
		return ports.CommandResult{}, context.DeadlineExceeded
	}
	return listed, err
}

// This regression reaches actual managed records, acquired source and CLI
// verification. Before the fix Codex becomes "failed", recovery duplicates the
// staging ZIP, and a completed sibling loses its completed attempt fact.
func TestActivationPartialRecoveryUsesAcquiredSource(t *testing.T) {
	for _, order := range [][]string{{"claude", "codex"}, {"codex", "claude"}} {
		t.Run(order[0]+"-first", func(t *testing.T) {
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
			got, err := Run(testCtx(t), req)
			if !errors.Is(err, context.DeadlineExceeded) || got.Reason != "activation_incomplete" {
				t.Fatalf("partial result: %+v %v", got, err)
			}
			byClient := map[string]TargetResult{}
			for _, target := range got.Targets {
				byClient[target.Client] = target
			}
			if byClient["claude"].Outcome != "activation_timeout" || byClient["claude"].Materialization != "materialized" {
				t.Fatalf("Claude attempt lost: %+v", byClient)
			}
			wantCodex := "activation_not_attempted"
			if order[0] == "codex" {
				wantCodex = "completed"
			}
			if byClient["codex"].Outcome != wantCodex || byClient["codex"].Materialization != "materialized" {
				t.Fatalf("Codex attempt: %+v", byClient)
			}
			if order[0] == "codex" {
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
			if order[0] == "codex" && strings.Count(string(traceBefore), "plugin add ") != 1 {
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

func buildRecoveryCodexCLI(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "TEST-cli.go")
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
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "TEST-codex")
	command := exec.Command("go", "build", "-o", binary, source)
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build finite CLI: %s %v", body, err)
	}
	return binary
}
