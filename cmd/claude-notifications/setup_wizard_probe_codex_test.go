//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/shared"
)

func TestWizardProbeCodexRemovalContract(t *testing.T) {
	root := t.TempDir()
	image, err := os.ReadFile(buildWizardProbe(t))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "TEST-wizard-codex")
	if err := os.WriteFile(binary, image, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	state := filepath.Join(root, "TEST-wizard-codex-installed.json")
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "CODEX_HOME="+root)
		return cmd.CombinedOutput()
	}
	must := func(args ...string) []byte {
		body, err := run(args...)
		if err != nil {
			t.Fatalf("probe %v: %v: %s", args, err, body)
		}
		return body
	}
	name, marketplace := "agent-notify", shared.ManagedMarketplaceName("TEST-codex-removal")
	spec := name + "@" + marketplace
	managed := filepath.Join(root, "managed")
	for rel, body := range map[string]string{
		".agents/plugins/marketplace.json": `{"name":"` + marketplace + `"}`,
		".codex-plugin/plugin.json":        `{"name":"agent-notify","version":"1.0.0"}`,
	} {
		path := filepath.Join(managed, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	must("plugin", "marketplace", "add", managed, "--json")
	must("plugin", "add", spec, "--json")
	if codex.PluginStatusFromList(must("plugin", "list", "--json"), name, marketplace) != codex.StatusInstalled {
		t.Fatal("qualified installed status missing")
	}
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"plugin", "remove", "foreign@" + marketplace, "--json"}, {"plugin", "marketplace", "remove", "foreign", "--json"}} {
		if body, err := run(args...); err == nil {
			t.Fatalf("foreign removal admitted: %v: %s", args, body)
		}
		after, err := os.ReadFile(state)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("foreign removal changed TEST state: %v", err)
		}
	}
	must("plugin", "remove", spec, "--json")
	if codex.PluginStatusFromList(must("plugin", "list", "--json"), name, marketplace) != codex.StatusAbsent {
		t.Fatal("removed plugin did not have qualified absence")
	}
	must("plugin", "marketplace", "remove", marketplace, "--json")
	after, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Marketplaces map[string]string
		Installed    map[string]map[string]any
	}
	if err := json.Unmarshal(after, &registry); err != nil || registry.Marketplaces == nil || registry.Installed == nil ||
		len(registry.Marketplaces) != 0 || len(registry.Installed) != 0 {
		t.Fatalf("matching marketplace cleanup did not empty TEST registry: %v", err)
	}
	if codex.PluginStatusFromList(must("plugin", "list", "--json"), name, marketplace) != codex.StatusAbsent {
		t.Fatal("marketplace cleanup changed qualified absence")
	}
	_, err = run("plugin", "unknown", "--json")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 99 {
		t.Fatalf("unknown command refusal changed: %v", err)
	}
}
