//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestUAPProjectedCodexLaunchRunsProductionMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "package source with spaces")
	probe := filepath.Join(pkg, "bin", "probe")
	for _, rel := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"plugin.json", []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.0"}`), 0600},
		{"mcp.json", []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"agent-notify":{"type":"stdio","command":"./bin/probe","args":[],"env":{}}}}`), 0600},
		{"skills/agent-notify/SKILL.md", []byte("---\nname: agent-notify\ndescription: Isolated UAP projection fixture\n---\nFixture only.\n"), 0600},
		{"bin/probe", binary, 0700},
	} {
		path := filepath.Join(pkg, rel.name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, rel.data, rel.mode); err != nil {
			t.Fatal(err)
		}
	}
	b := portable.Binding{
		Version: 1, Integration: portable.Codex, InstallationID: "00000000-0000-4000-8000-000000000008",
		BindingID: "codex-binding", ScopeID: "user", Owner: "existing-installer",
		ScopeRoot: filepath.Join(root, "scope"), DataRoot: filepath.Join(root, "shared data"),
		ControlRoot: filepath.Join(root, "control"), GlobalConfig: filepath.Join(root, "global", "config.json"),
		RuntimeRoot: filepath.Join(root, "permanent runtime"), Primary: "primary",
	}
	for _, p := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: "existing",
		Files: []installruntime.File{{Path: filepath.Join(b.RuntimeRoot, b.Primary), Data: binary, Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	uapRoot := filepath.Join(root, "uap")
	mat, err := portablesetup.NewMaterializer(portablesetup.UAPRoots{
		StateFile: filepath.Join(uapRoot, "state", "state-v2.json"), LockFile: filepath.Join(uapRoot, "state", "mutation.lock"),
		OperationsDir: filepath.Join(uapRoot, "state", "operations"), PluginDataBase: filepath.Join(uapRoot, "plugin data"),
		ManagedRoot: filepath.Join(uapRoot, "managed"), HelperExecutable: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "home", "codex config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := mat.Install(ctx, portablesetup.MaterializeRequest{
		Identity: portablesetup.Identity{
			InstallationID: b.InstallationID, ComponentID: ledger.ID, Owner: b.Owner, ScopeRoot: b.ScopeRoot,
			ControlRoot: b.ControlRoot, GlobalConfig: b.GlobalConfig, RuntimeRoot: b.RuntimeRoot, Primary: b.Primary,
		},
		Integration: portable.Codex, ExpectedGeneration: ledger.Generation, PackageRoot: pkg,
		ClientConfigRoot: config, ClientExecutable: probe, OperationID: "portable-codex-projection",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var mcpPath string
	for _, installation := range state.Installations {
		if installation.InstallationID != b.InstallationID {
			continue
		}
		for _, client := range installation.Clients {
			mcpPath = filepath.Join(client.TargetLocator, ".mcp.json")
		}
	}
	body, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var projected struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if json.Unmarshal(body, &projected) != nil {
		t.Fatalf("projected MCP is not JSON: %s", body)
	}
	command, args, env := projected.Command, projected.Args, projected.Env
	if command == "" {
		for _, server := range projected.MCPServers {
			command, args, env = server.Command, server.Args, server.Env
		}
	}
	if command == "" || !containsAll(args, "portable-launch", "--locator") {
		t.Fatalf("UAP projection missing portable-launch: %s", body)
	}
	if env["PLUGIN_DATA"] == "" || env["PLUGIN_ROOT"] == "" {
		t.Fatalf("UAP projection missing explicit plugin env: %s", body)
	}
	if _, ok := env["HOME"]; ok || env["PATH"] != "" {
		t.Fatalf("UAP projection inherited ambient env: %s", body)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = []string{"PLUGIN_DATA=" + env["PLUGIN_DATA"], "PLUGIN_ROOT=" + env["PLUGIN_ROOT"]}
	cmd.Dir = filepath.Dir(mcpPath)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := false
	defer func() {
		if !done {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(out)
	if _, err := fmt.Fprintln(in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"foreign-client-label","version":"1"}}}`); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("projected launch produced no protocol: %v stderr=%s mcp=%s", err, diagnostics.String(), body)
	}
	if !bytes.Contains(line, []byte(`"id":1`)) || bytes.Contains(line, []byte(`"error"`)) {
		t.Fatalf("projected stdout is not MCP initialize: %s", line)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = in.Close()
	_ = cmd.Wait()
	done = true
	if _, err := portable.Acquire(ctx, got.DataRoot, mustLocator(t, got)); err != nil {
		t.Fatal("locator not acquirable after projected launch")
	}
}

func mustLocator(t *testing.T, b portable.Binding) string {
	t.Helper()
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func containsAll(args []string, want ...string) bool {
	joined := strings.Join(args, "\x00")
	for _, item := range want {
		if !strings.Contains(joined, item) {
			return false
		}
	}
	return true
}
