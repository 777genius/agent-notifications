//go:build linux || darwin

package portablesetup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

type listingRunner struct {
	configRoot string
}

func (r listingRunner) Run(_ context.Context, _ ports.Command) (ports.CommandResult, error) {
	root := filepath.Join(r.configRoot, "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return ports.CommandResult{Stdout: []byte("[]")}, nil
	}
	if err != nil {
		return ports.CommandResult{}, err
	}
	listed := make([]map[string]any, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		path := filepath.Join(root, entry.Name())
		body, readErr := os.ReadFile(filepath.Join(path, ".claude-plugin", "plugin.json"))
		if readErr != nil {
			continue
		}
		var manifest map[string]any
		if json.Unmarshal(body, &manifest) != nil {
			continue
		}
		name, _ := manifest["name"].(string)
		listed = append(listed, map[string]any{
			"id": name + "@skills-dir", "version": manifest["version"], "scope": "user",
			"enabled": true, "installPath": path,
		})
	}
	body, err := json.Marshal(listed)
	return ports.CommandResult{Stdout: body}, err
}

func buildProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(src, []byte(`package main
import ("encoding/json"; "os")
func main() {
	json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "PLUGIN_DATA": os.Getenv("PLUGIN_DATA")})
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "probe")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %s %v", body, err)
	}
	return out
}

func writePackage(t *testing.T, root, probe string) {
	t.Helper()
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"plugin.json":                  []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.0"}`),
		"mcp.json":                     []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"agent-notify":{"type":"stdio","command":"./bin/probe","args":[],"env":{}}}}`),
		"skills/agent-notify/SKILL.md": []byte("---\nname: agent-notify\ndescription: Isolated portable setup fixture\n---\nFixture only.\n"),
		"bin/probe":                    body,
	}
	for rel, data := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if rel == "bin/probe" {
			mode = 0700
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUAPMaterializerTwoClientsShareDataIndependentLocators(t *testing.T) {
	codex, ledger := bindingFixture(t)
	probe := buildProbe(t)
	root := filepath.Dir(codex.ControlRoot)
	pkg := filepath.Join(root, "package source with spaces")
	writePackage(t, pkg, probe)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{
		StateFile:        filepath.Join(uapRoot, "state", "state-v2.json"),
		LockFile:         filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir:    filepath.Join(uapRoot, "state", "operations"),
		PluginDataBase:   filepath.Join(uapRoot, "plugin data"),
		ManagedRoot:      filepath.Join(uapRoot, "managed"),
		HelperExecutable: probe,
		ClaudeRunner:     listingRunner{configRoot: filepath.Join(root, "home", "claude config")},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := Identity{
		InstallationID: "00000000-0000-4000-8000-000000000007",
		ComponentID:    codex.ComponentID, Owner: codex.Owner, ScopeRoot: codex.ScopeRoot,
		ControlRoot: codex.ControlRoot, GlobalConfig: codex.GlobalConfig, RuntimeRoot: codex.RuntimeRoot,
		Primary: codex.Primary,
	}
	install := func(integration portable.Integration, gen uint64) portable.Binding {
		t.Helper()
		config := filepath.Join(root, "home", string(integration)+" config")
		if err := os.MkdirAll(config, 0700); err != nil {
			t.Fatal(err)
		}
		got, err := mat.Install(testCtx(t), MaterializeRequest{
			Identity: id, Integration: integration, ExpectedGeneration: gen,
			PackageRoot: pkg, ClientConfigRoot: config, ClientExecutable: probe,
			OperationID: "portable-" + string(integration),
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	codexB := install(portable.Codex, ledger.Generation)
	snap, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	claudeB := install(portable.Claude, snap.Ledger.Generation)
	if codexB.DataRoot != claudeB.DataRoot {
		t.Fatalf("clients did not share PLUGIN_DATA: %s vs %s", codexB.DataRoot, claudeB.DataRoot)
	}
	codexName, err := codexB.Filename()
	if err != nil {
		t.Fatal(err)
	}
	claudeName, err := claudeB.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if codexName == claudeName {
		t.Fatal("shared data used one locator")
	}
	lease, err := portable.Acquire(testCtx(t), codexB.DataRoot, codexName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease, err = portable.Acquire(testCtx(t), claudeB.DataRoot, claudeName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, id.InstallationID)
	if !ok {
		t.Fatal("UAP installation missing")
	}
	var sawLocator bool
	for _, binding := range installation.Clients {
		mcp := filepath.Join(binding.TargetLocator, ".mcp.json")
		body, err := os.ReadFile(mcp)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "portable-launch") || !strings.Contains(string(body), "--locator") {
			t.Fatalf("locator missing from projection %s: %s", mcp, body)
		}
		sawLocator = true
	}
	if !sawLocator {
		t.Fatal("no projected MCP")
	}
	snap, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := mat.Remove(testCtx(t), MaterializeRequest{
		Identity: id, Integration: portable.Claude, ExpectedGeneration: snap.Ledger.Generation,
		ClientConfigRoot: filepath.Join(root, "home", "claude config"), ClientExecutable: probe,
		OperationID: "portable-claude-remove",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.Acquire(testCtx(t), claudeB.DataRoot, claudeName); err == nil {
		t.Fatal("removed locator still acquired")
	}
	if _, err := portable.Acquire(testCtx(t), codexB.DataRoot, codexName); err != nil {
		t.Fatal("sibling locator lost")
	}
	if _, err := os.Stat(filepath.Join(codex.RuntimeRoot, codex.Primary)); err != nil {
		t.Fatal("shared runtime removed")
	}
}
