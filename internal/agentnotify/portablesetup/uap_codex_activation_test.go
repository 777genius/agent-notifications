//go:build linux || darwin

package portablesetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	source   string
	version  string
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
		body, err := os.ReadFile(filepath.Join(args[3], ".codex-plugin", "plugin.json"))
		if err != nil {
			return ports.CommandResult{}, err
		}
		var manifest struct {
			Name, Version string
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			return ports.CommandResult{}, err
		}
		if manifest.Name != "agent-notify" || manifest.Version != "1.0.0" {
			return ports.CommandResult{}, fmt.Errorf("unexpected Codex fixture manifest")
		}
		r.source, r.version = args[3], manifest.Version
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
			"installed": true, "enabled": true, "version": r.version,
			"source": map[string]any{"source": "local", "path": r.source},
		}}})
		return ports.CommandResult{Stdout: body}, err
	default:
		return ports.CommandResult{}, fmt.Errorf("unexpected Codex command: %v", args)
	}
}

// This is a scripted SDK contract fixture, not native Codex qualification.
// Check every request against the selected installed fixture; reject unrelated
// RPCs instead of returning a generic successful response.
func (r *codexActivationRunner) RunDuplexToCompletion(ctx context.Context, cmd ports.Command, exchange func(io.Writer, io.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(cmd.Argv) != 2 || cmd.Argv[1] != "app-server" || cmd.Dir != r.source ||
		!containsEnvironment(cmd.Env, "CODEX_HOME="+r.profile) {
		return fmt.Errorf("unexpected Codex discovery command")
	}
	name, marketplace, ok := strings.Cut(r.pluginID, "@")
	if !ok || name != "agent-notify" || r.version != "1.0.0" {
		return fmt.Errorf("unexpected installed Codex fixture identity")
	}
	r.calls = append(r.calls, "app-server")
	skill := "agent-notifications"
	if _, err := os.Stat(filepath.Join(r.source, "skills", skill, "SKILL.md")); err != nil {
		return err
	}
	responses := []map[string]any{
		{"id": 1, "result": map[string]any{"userAgent": "agentplugins/0.162.0", "codexHome": r.profile}},
		{"id": 3, "result": map[string]any{"data": []any{map[string]any{
			"cwd": r.source, "errors": []any{}, "skills": []any{map[string]any{
				"name": name + ":" + skill, "scope": "user", "enabled": true, "pluginId": r.pluginID,
				"path": filepath.Join(r.profile, "plugins", "cache", marketplace, name, r.version, "skills", skill, "SKILL.md"),
			}},
		}}}},
		{"id": 4, "result": map[string]any{"data": []any{map[string]any{
			"name": "agent-notifications", "pluginId": r.pluginID, "authStatus": "unsupported",
			"runtimeStatus": "connected", "serverInfo": map[string]any{"name": "agent-notifications", "version": "1.0.0"},
			"tools": map[string]any{},
		}}, "nextCursor": nil}},
	}
	var replies, requests bytes.Buffer
	encoder := json.NewEncoder(&replies)
	for _, response := range responses {
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	if err := exchange(&requests, &replies); err != nil {
		return err
	}
	decoder := json.NewDecoder(&requests)
	for i, method := range []string{"initialize", "initialized", "skills/list", "mcpServerStatus/list"} {
		var request struct {
			ID     int
			Method string
			Params struct {
				Cwds        []string
				ForceReload bool
				ServerName  string
				Detail      string
			}
		}
		if err := decoder.Decode(&request); err != nil {
			return err
		}
		if request.Method != method || request.ID != []int{1, 0, 3, 4}[i] {
			return fmt.Errorf("unexpected Codex discovery request %d: %s", i, request.Method)
		}
		if method == "skills/list" && (len(request.Params.Cwds) != 1 || request.Params.Cwds[0] != r.source || !request.Params.ForceReload) {
			return fmt.Errorf("Codex skill discovery escaped the fixture package")
		}
		if method == "mcpServerStatus/list" && (request.Params.ServerName != "agent-notifications" || request.Params.Detail != "toolsAndAuthOnly") {
			return fmt.Errorf("Codex discovery did not select the fixture MCP server")
		}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || replies.Len() != 0 {
		return fmt.Errorf("unexpected extra Codex discovery exchange")
	}
	return ctx.Err()
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
	if len(runner.calls) != 5 || !strings.HasPrefix(runner.calls[0], "plugin marketplace add ") ||
		!strings.HasPrefix(runner.calls[1], "plugin add agent-notify@") || runner.calls[2] != "plugin list --json" ||
		runner.calls[3] != "plugin list --json" || runner.calls[4] != "app-server" {
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
