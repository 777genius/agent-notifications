//go:build linux || darwin

package setupwizard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

// A retry with an explicit Local selection must retain the package's exact
// component names. Canonical and older packages can declare different MCP keys;
// a unit boolean cannot authorize silently substituting one for the other.
func TestLocalIntentRetryPreservesPackageSelection(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		t.Skip("Local selected delivery has no qualified Darwin Intel tuple")
	}
	for _, server := range []string{"agent-notifications", "agent-notify"} {
		t.Run(server, func(t *testing.T) {
			ctx := testCtx(t)
			control, runtimeRoot, global, primary, generation := managedRuntime(t)
			root := filepath.Dir(control)
			profile, pkg := filepath.Join(root, "TEST-profile"), filepath.Join(root, "TEST-package")
			if err := os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			writePackage(t, pkg, primary)
			mcp, err := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{server: map[string]any{"type": "stdio", "command": "./bin/probe", "args": []string{}, "env": map[string]string{}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkg, "mcp.json"), mcp, 0600); err != nil {
				t.Fatal(err)
			}
			// Also retain a supplied skill name rather than replacing it with
			// the product default while restoring the selected skill unit.
			const skill = "selected-retry-skill"
			if err := os.Rename(filepath.Join(pkg, "skills", "agent-notifications"), filepath.Join(pkg, "skills", skill)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkg, "skills", skill, "SKILL.md"), []byte("---\nname: "+skill+"\ndescription: Explicit retry selection\n---\n"), 0600); err != nil {
				t.Fatal(err)
			}
			tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
			if runtime.GOOS == "darwin" {
				tuple = vscode.QualifiedDarwinTESTTuple()
			}
			cfg := &vscode.LocalConfig{ProfileSettingsPath: filepath.Join(profile, "settings.json"), QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, MCPServers: []string{server}, Skills: []string{skill}}
			req := Request{Action: ActionInstall, Agents: []string{"copilot-vscode"}, LocalConfig: cfg, ScopeRoot: profile, ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, Primary: "bin/claude-notifications", PackageRoot: pkg, Helper: primary, InstallationID: "00000000-0000-4000-8000-000000000239"}
			agents := []portable.Integration{portable.CopilotVSCode}
			hooks, notify := selectedUnits(req, agents)
			intent := portablesetup.Intent{Version: 1, SetupIntentID: "TEST-local-package-retry", Action: "install", Stage: "confirmed", ExpectedGeneration: generation, Targets: wizardIntentTargets(req, hooks, notify), Primary: req.Primary}
			body, err := json.Marshal(intent)
			if err != nil {
				t.Fatal(err)
			}
			var durable portablesetup.Intent
			if err := json.Unmarshal(body, &durable); err != nil {
				t.Fatal(err)
			}
			retry := retryRequestFromIntent(req, durable)
			if retry.LocalConfig == nil || !slices.Equal(retry.LocalConfig.MCPServers, cfg.MCPServers) || !slices.Equal(retry.LocalConfig.Skills, cfg.Skills) {
				t.Fatalf("retry changed explicit package selection: %+v", retry.LocalConfig)
			}
			restored, _, err := restoreOmittedFromIntent(retry, agents, durable)
			if err != nil {
				t.Fatal("restore selected pending intent", err)
			}
			adapter, err := vscode.NewLocal(*restored.LocalConfig)
			if err != nil {
				t.Fatal(err)
			}
			uapRoot := filepath.Join(root, "TEST-uap")
			mat, err := portablesetup.NewMaterializer(portablesetup.UAPRoots{StateFile: filepath.Join(uapRoot, "state", "state-v2.json"), LockFile: filepath.Join(uapRoot, "state", "mutation.lock"), OperationsDir: filepath.Join(uapRoot, "state", "operations"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed"), HelperExecutable: primary})
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(adapter)
			if err != nil {
				t.Fatal(err)
			}
			snap, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil {
				t.Fatal(err)
			}
			install := portablesetup.MaterializeRequest{Identity: portablesetup.Identity{InstallationID: req.InstallationID, ComponentID: snap.Ledger.ID, Owner: "existing-installer", ScopeRoot: profile, ControlRoot: control, GlobalConfig: global, RuntimeRoot: runtimeRoot, Primary: req.Primary}, LocalConfig: restored.LocalConfig, Integration: portable.CopilotVSCode, ExpectedGeneration: generation, PackageRoot: pkg, ClientConfigRoot: profile, ClientExecutable: primary, OperationID: "TEST-local-package-install"}
			plan, err := mat.PreviewPlan(ctx, install)
			if err != nil {
				t.Fatal("public Prepare with exact retry selection", err)
			}
			facts, ok := plan.SelectedDelivery.LocalFacts()
			if !ok || !slices.Equal(facts.MCPServers, cfg.MCPServers) || !slices.Equal(facts.Skills, cfg.Skills) {
				t.Fatalf("Prepare changed exact selection: %+v", facts)
			}
			binding, err := mat.Install(ctx, install)
			if err != nil {
				t.Fatal("public Install with exact retry selection", err)
			}
			state, err := mat.Store.Load()
			if err != nil || len(state.Installations) != 1 {
				t.Fatalf("installed selected state unavailable: %+v %v", state, err)
			}
			facts, ok = state.Installations[0].Clients[binding.BindingID].SelectedDelivery.LocalFacts()
			if !ok || !slices.Equal(facts.MCPServers, cfg.MCPServers) || !slices.Equal(facts.Skills, cfg.Skills) {
				t.Fatalf("installed selection changed: %+v", facts)
			}
			// Retry data must not share writable selection slices with its caller.
			retry.LocalConfig.MCPServers[0], retry.LocalConfig.Skills[0] = "changed", "changed"
			if cfg.MCPServers[0] != server || cfg.Skills[0] != skill {
				t.Fatal("retry mutated caller's exact component selection")
			}
			cleared := applyIntentUnits(req, map[string]unitSelection{"copilot-vscode": {local: true}})
			if len(cleared.LocalConfig.MCPServers) != 0 || len(cleared.LocalConfig.Skills) != 0 || cfg.MCPServers[0] != server || cfg.Skills[0] != skill {
				t.Fatal("disabled intent units did not clear only the retry selection")
			}
		})
	}
}
