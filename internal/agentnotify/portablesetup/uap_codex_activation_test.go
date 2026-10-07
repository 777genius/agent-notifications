//go:build linux || darwin

package portablesetup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
)

type codexActivationRunner struct {
	profile  string
	pluginID string
	calls    []string
}

func (r *codexActivationRunner) Run(_ context.Context, cmd ports.Command) (ports.CommandResult, error) {
	if len(cmd.Argv) < 3 || !containsEnvironment(cmd.Env, "CODEX_HOME="+r.profile) {
		return ports.CommandResult{}, fmt.Errorf("Codex command escaped selected profile: %+v", cmd)
	}
	args := cmd.Argv[1:]
	r.calls = append(r.calls, strings.Join(args, " "))
	switch {
	case len(args) == 5 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add":
		return ports.CommandResult{Stdout: []byte(`{}`)}, nil
	case len(args) == 4 && args[0] == "plugin" && args[1] == "add":
		r.pluginID = args[2]
		return ports.CommandResult{Stdout: []byte(`{}`)}, nil
	case len(args) == 3 && args[0] == "plugin" && args[1] == "list":
		name, marketplace, ok := strings.Cut(r.pluginID, "@")
		if !ok {
			return ports.CommandResult{}, fmt.Errorf("plugin list ran before plugin add")
		}
		body, err := json.Marshal(map[string]any{"installed": []map[string]any{{
			"pluginId": r.pluginID, "name": name, "marketplaceName": marketplace,
			"installed": true, "enabled": true,
		}}})
		return ports.CommandResult{Stdout: body}, err
	default:
		return ports.CommandResult{}, fmt.Errorf("unexpected Codex command: %v", args)
	}
}

func containsEnvironment(env []string, entry string) bool {
	for _, item := range env {
		if item == entry {
			return true
		}
	}
	return false
}

func TestUAPMaterializerActivatesCodexInSelectedProfile(t *testing.T) {
	binding, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(binding.ControlRoot)
	pkg := filepath.Join(root, "package")
	writePackage(t, pkg, probe)
	profile := filepath.Join(root, "codex profile")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	runner := &codexActivationRunner{profile: profile}
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin-data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		CodexRunner:      runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mat.Install(testCtx(t), MaterializeRequest{
		Identity: Identity{
			InstallationID: "00000000-0000-4000-8000-000000000097",
			ComponentID:    binding.ComponentID, Owner: binding.Owner,
			ScopeRoot: binding.ScopeRoot, ControlRoot: binding.ControlRoot,
			GlobalConfig: binding.GlobalConfig, RuntimeRoot: binding.RuntimeRoot,
			Primary: binding.Primary,
		},
		Integration: portable.Codex, ExpectedGeneration: ledger.Generation,
		PackageRoot: pkg, ClientConfigRoot: profile, ClientExecutable: probe,
		OperationID: "codex-native-activation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 || !strings.HasPrefix(runner.calls[0], "plugin marketplace add ") ||
		!strings.HasPrefix(runner.calls[1], "plugin add agent-notify@") || runner.calls[2] != "plugin list --json" {
		t.Fatalf("Codex native registration calls: %v", runner.calls)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, "00000000-0000-4000-8000-000000000097")
	if !ok {
		t.Fatal("installed package missing")
	}
	for _, client := range installation.Clients {
		if string(client.Activation) != "active" || string(client.Verification) != "installation_verified" {
			t.Fatalf("Codex native registration was not verified: %+v", client)
		}
		return
	}
	t.Fatal("Codex binding missing")
}
