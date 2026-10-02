//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Before this repair Run refuses the kernel CAS but loses its error identity,
// advertises an unfenced --yes retry, and calls a finalization conflict cleanup
// failure. Use production progress callbacks and actual managed state throughout.
func TestRunPolicyConflictRequiresFreshConsent(t *testing.T) {
	for _, scenario := range []string{"install", "update", "finalization", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := testCtx(t)
			control, runtimeRoot, global, _, gen := managedRuntime(t)
			root := filepath.Dir(control)
			probe := buildProbe(t)
			pkg := filepath.Join(root, "package")
			writePackage(t, pkg, probe)
			req := Request{Action: ActionInstall, Yes: true, Agents: []string{"codex"}, Hooks: boolPtr(false), AgentNotify: boolPtr(true), ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, PackageRoot: pkg, CodexHome: filepath.Join(root, "codex"), ClientExecutable: probe, Helper: probe, ScopeRoot: filepath.Join(root, "scope")}
			for _, dir := range []string{req.CodexHome, req.ScopeRoot} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			on := true
			_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "existing", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &on})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "update" {
				installed, err := Run(ctx, req)
				if err != nil || installed.Outcome != "completed" {
					t.Fatalf("initial install: %+v %v", installed, err)
				}
				req.Action = ActionUpdate
			}
			projection, generation, err := ObserveBootstrapMCP(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			req.BootstrapMCP = &BootstrapMCPSelection{Selected: req.Agents, Projection: projection}
			req.BootstrapExpectedGeneration = &generation
			policyPath := filepath.Join(control, "agent-notifications.json")
			raw, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			edited := bytes.Replace(raw, []byte(`"enabled": true`), []byte(`"enabled": false`), 1)
			if bytes.Equal(edited, raw) {
				t.Fatal("fixture has no enabled field")
			}
			if scenario == "malformed" {
				edited = []byte("invalid TEST policy")
			}
			phase := "preflight"
			if scenario == "finalization" {
				phase = "complete"
			}
			var before map[string][]byte
			changed := false
			req.Progress = func(current string) {
				if current != phase || changed {
					return
				}
				before = policyConflictFiles(t, root)
				if err := os.WriteFile(policyPath, edited, 0600); err != nil {
					t.Fatal(err)
				}
				changed = true
			}
			out, err := Run(ctx, req)
			if !changed {
				t.Fatalf("never reached %s: %+v %v", phase, out, err)
			}
			after := policyConflictFiles(t, root)
			before[policyPath] = edited
			if len(before) != len(after) {
				t.Fatalf("refusal changed files: before=%v after=%v", before, after)
			}
			for path, data := range before {
				if !bytes.Equal(data, after[path]) {
					t.Fatalf("refusal changed %s", path)
				}
			}
			if scenario == "malformed" {
				if err == nil || errors.Is(err, portablesetup.ErrConcurrentChange) || out.Reason == "concurrent_change" || len(out.Command) == 0 {
					t.Fatalf("unrelated failure lost its recovery: %+v %v", out, err)
				}
				return
			}
			if !errors.Is(err, portablesetup.ErrConcurrentChange) || !errors.Is(err, installruntime.ErrPolicyConflict) || out.Outcome != "conflict" || out.Reason != "concurrent_change" {
				t.Fatalf("policy conflict identity lost: %+v %v", out, err)
			}
			if len(out.Command) != 0 || len(out.NextActions) != 0 {
				t.Fatalf("conflict advertised unsafe retry/actions: %+v", out)
			}
			snap, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "finalization" {
				if snap.Ledger.PendingMutation == nil || len(LiveNotifyClients(control, req.Agents)) != 1 {
					t.Fatal("lost committed binding or pending intent")
				}
				completed := false
				for _, target := range out.Targets {
					if target.Unit == "agent-notify" && target.Outcome == "completed" {
						completed = true
					}
				}
				if !completed {
					t.Fatalf("lost completed effects: %+v", out.Targets)
				}
			} else if snap.Ledger.Generation != generation || snap.Ledger.PendingMutation != nil {
				t.Fatalf("refused admission changed generation/reservation: %+v", snap.Ledger)
			}
		})
	}
}

// Capture payloads throughout this freshly made TEST runtime, profile and UAP
// tree; lock files may be provisioned by admission without changing products.
func policyConflictFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) == ".lock" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[path] = data
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
