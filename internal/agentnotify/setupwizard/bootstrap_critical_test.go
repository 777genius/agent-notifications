//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Old code counts the surviving ledger receipt after an owned entry is deleted,
// so the confirmed projection still passes. Real config removal must revoke it
// without a generation increment, before reservation or portable writes.
func TestBootstrapOwnedDirectDeletionSameGeneration(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			ctx := testCtx(t)
			control, runtimeRoot, _, command, gen := managedRuntime(t)
			root := filepath.Dir(control)
			profile := filepath.Join(root, agent)
			path := filepath.Join(root, "direct.json")
			if agent == "codex" {
				path = filepath.Join(profile, "config.toml")
			}
			if err := os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			provider := registration.Provider(agent)
			applied, err := clientsetup.Apply(ctx, clientsetup.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, ConfigPath: path, Command: command, Provider: provider, Mode: clientsetup.Managed, ExpectedGeneration: gen})
			if err != nil {
				t.Fatal(err)
			}
			r := Request{Agents: []string{agent}, ControlRoot: control, RuntimeRoot: runtimeRoot, ClaudeConfig: profile, CodexHome: profile, MCPConfig: map[string]string{agent: path}}
			before, generation, err := ObserveBootstrapMCP(ctx, r)
			if err != nil || len(before.Direct) != 1 {
				t.Fatalf("owned baseline: %+v %v", before, err)
			}
			if generation != applied.Ledger.Generation {
				t.Fatal("baseline generation mismatch")
			}
			// Independent user edit: retain unrelated config and remove only the server.
			data := []byte(`{"mcpServers":{},"TEST":"retained"}`)
			if agent == "codex" {
				data = []byte("TEST = 'retained'\n[mcp_servers]\n")
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			ownership, err := os.ReadFile(filepath.Join(control, "ownership.json"))
			if err != nil {
				t.Fatal(err)
			}
			expected := BootstrapMCPSelection{Selected: []string{agent}, Projection: before}
			current, currentGen, err := ObserveBootstrapMCP(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if currentGen != generation {
				t.Fatal("manual edit advanced ledger")
			}
			if err := CheckBootstrapMCP(expected, current); err == nil {
				t.Fatal("deleted actual owned entry accepted with unchanged ledger receipt")
			}
			r.Action, r.Yes, r.Hooks, r.AgentNotify = ActionInstall, true, boolPtr(false), boolPtr(true)
			r.BootstrapMCP, r.BootstrapExpectedGeneration = &expected, &generation
			result, err := Run(ctx, r)
			if err == nil || result.Reason != "concurrent_change" {
				t.Fatalf("portable admission: %+v %v", result, err)
			}
			after, err := os.ReadFile(filepath.Join(control, "ownership.json"))
			if err != nil || !bytes.Equal(after, ownership) {
				t.Fatalf("refusal changed ledger: %v", err)
			}
			after, err = os.ReadFile(path)
			if err != nil || !bytes.Equal(after, data) {
				t.Fatalf("refusal recreated registration: %v", err)
			}
			for _, absent := range []string{filepath.Join(control, "portable-handoff.json"), filepath.Join(root, "uap")} {
				if _, err := os.Lstat(absent); !os.IsNotExist(err) {
					t.Fatalf("portable effect at %s: %v", absent, err)
				}
			}
		})
	}
}

// Old evaluation discards the policy identity, so a same-generation edit after
// its final guard still publishes the wizard reservation. Capture today's
// preimage after legitimate own policy/ledger changes, then verify real refusal.
func TestBootstrapFinalGuardPolicyHandoff(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, _, _, gen := managedRuntime(t)
	root := filepath.Dir(control)
	r := Request{Action: ActionInstall, Yes: true, Agents: []string{"claude"}, Hooks: boolPtr(false), AgentNotify: boolPtr(true), ControlRoot: control, RuntimeRoot: runtimeRoot, ClaudeConfig: filepath.Join(root, "claude")}
	on := true
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "existing", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := ObserveBootstrapMCP(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	confirmedPolicy, err := installruntime.ReadPolicySnapshot(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	expected := BootstrapMCPSelection{Selected: r.Agents, Projection: before}
	// A real own unrelated policy generation must not be fenced by the original
	// confirmation bytes. This field has no MCP enabled/keep-off meaning.
	gen = ledger.Generation
	ledger, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "existing", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"localRouting":false,"allowUnknownCaller":false,"allowCallerAsserted":false}`)}})
	if err != nil {
		t.Fatal(err)
	}
	r.BootstrapMCP = &expected
	r.BootstrapExpectedGeneration = &ledger.Generation
	ev := evaluate(ctx, &r, true)
	if ev.stop || ev.err != nil {
		t.Fatalf("own generation refused: %+v %v", ev.out, ev.err)
	}
	verified, err := installruntime.ReadPolicySnapshot(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	if r.BootstrapExpectedPolicy == nil || *r.BootstrapExpectedPolicy != verified.Preimage || *r.BootstrapExpectedPolicy == confirmedPolicy.Preimage {
		t.Fatal("final guard did not carry current verified policy")
	}
	mat, err := materializer(r, ev.snap, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if mat.Kernel.ExpectedPolicy == nil || *mat.Kernel.ExpectedPolicy != verified.Preimage {
		t.Fatal("coordinated portable commits lost admission preimage")
	}
	raw, err := os.ReadFile(filepath.Join(control, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(raw, []byte(`"enabled": true`), []byte(`"enabled": false`), 1)
	if bytes.Equal(edited, raw) {
		t.Fatal("fixture has no enabled field")
	}
	if err := os.WriteFile(filepath.Join(control, "agent-notifications.json"), edited, 0600); err != nil {
		t.Fatal(err)
	}
	ownership, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = publishWizardIntent(ctx, r, ev.snap, runtimeRoot, ev.hookAgents, ev.notifyAgents, true)
	if !errors.Is(err, installruntime.ErrPolicyConflict) {
		t.Fatalf("post-guard edit admitted: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil || !bytes.Equal(after, ownership) {
		t.Fatalf("refusal changed ownership: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(control, "portable-handoff.json")); !os.IsNotExist(err) {
		t.Fatalf("refused reservation persisted: %v", err)
	}
}

// A fence against the original confirmation policy would reject unrelated own
// policy generations. Verify actual portable materialization and finalization
// with the newly verified policy, rather than only snapshot comparison.
func TestBootstrapOwnGenerationGuardedInstall(t *testing.T) {
	ctx := testCtx(t)
	control, runtimeRoot, global, _, gen := managedRuntime(t)
	root := filepath.Dir(control)
	probe := buildProbe(t)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	r := Request{Action: ActionInstall, Yes: true, Agents: []string{"codex"}, Hooks: boolPtr(false), AgentNotify: boolPtr(true), ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, PackageRoot: pkg, CodexHome: filepath.Join(root, "codex"), ClientExecutable: probe, Helper: probe, ScopeRoot: filepath.Join(root, "scope")}
	for _, dir := range []string{r.CodexHome, r.ScopeRoot} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	before, _, err := ObserveBootstrapMCP(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.BootstrapMCP = &BootstrapMCPSelection{Selected: r.Agents, Projection: before, AllowPolicySeed: true, SeedEnabled: true}
	on := true
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "existing", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &on, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"localRouting":false,"allowUnknownCaller":false,"allowCallerAsserted":false}`)}})
	if err != nil {
		t.Fatal(err)
	}
	r.BootstrapExpectedGeneration = &ledger.Generation
	result, err := Run(ctx, r)
	if err != nil || result.Outcome != "completed" {
		t.Fatalf("guarded install after own seed/generation: %+v %v", result, err)
	}
	current, _, err := ObserveBootstrapMCP(ctx, r)
	if err != nil || len(current.Bindings) != 1 || current.Bindings[0].Client != "codex" {
		t.Fatalf("missing actual portable binding: %+v %v", current, err)
	}
	installed, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || installed.Ledger.PendingMutation != nil {
		t.Fatalf("intent did not finalize: %+v %v", installed, err)
	}
	consumers := 0
	for id := range installed.Ledger.Consumers {
		if strings.HasPrefix(id, "portable:") {
			consumers++
		}
	}
	if consumers != 1 {
		t.Fatalf("owned portable consumers=%d", consumers)
	}
}
