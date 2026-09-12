//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
)

func TestNotificationInventoryClaudeExactPackage(t *testing.T) {
	home := setupCommandRoot(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	packageRoot := filepath.Join(home, "package")
	for _, p := range []string{filepath.Join(home, "plugins"), filepath.Join(packageRoot, ".claude-plugin")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	key := "claude-notifications-go@claude-notifications-go"
	registry, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{key: []any{map[string]any{"scope": "user", "installPath": packageRoot, "version": "1.41.0"}}}})
	write(filepath.Join(home, "plugins", "installed_plugins.json"), string(registry))
	write(filepath.Join(packageRoot, ".claude-plugin", "plugin.json"), `{"name":"claude-notifications-go","version":"1.41.0"}`)
	write(filepath.Join(home, "settings.json"), `{"enabledPlugins":{"`+key+`":true}}`)
	inv, err := inspectNotificationInventory(context.Background(), registration.Claude, home)
	if err != nil || inv.State != "clear" {
		t.Fatal(inv, err)
	}
	write(filepath.Join(packageRoot, ".mcp.json"), `{"mcpServers":{"agent_notifications":{"command":"/usr/bin/true"}}}`)
	if err = inv.Revalidate(context.Background()); err == nil {
		t.Fatal("package drift missed")
	}
	inv, err = inspectNotificationInventory(context.Background(), registration.Claude, home)
	if err != nil || inv.State != "collision" {
		t.Fatal(inv, err)
	}
	write(filepath.Join(home, "settings.json"), `{"enabledPlugins":{"`+key+`":false}}`)
	inv, err = inspectNotificationInventory(context.Background(), registration.Claude, home)
	if err != nil || inv.State != "clear" {
		t.Fatal("disabled declaration", inv, err)
	}
	for _, malformed := range []string{
		`{"enabledPlugins":{"` + key + `":null}}`,
		`{"enabledPlugins":{"` + key + `":true},"EnabledPlugins":{"` + key + `":false}}`,
		`{"EnabledPlugins":{"` + key + `":false}}`,
		`{"enabledPlugins":null}`,
	} {
		write(filepath.Join(home, "settings.json"), malformed)
		if _, err := inspectNotificationInventory(context.Background(), registration.Claude, home); err == nil {
			t.Fatal("ambiguous activation accepted", malformed)
		}
	}
	write(filepath.Join(home, "settings.json"), `{}`)
	if _, err = inspectNotificationInventory(context.Background(), registration.Claude, home); err == nil {
		t.Fatal("ambiguous activation accepted")
	}
}

func TestNotificationCodexQualifiedCacheSeam(t *testing.T) {
	for _, version := range []string{"codex-cli 0.152.0", "codex-cli 0.153.4"} {
		t.Run(version, func(t *testing.T) {
			home := setupCommandRoot(t)
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			id := "claude-notifications-go@test-marketplace"
			cache := filepath.Join(home, "plugins", "cache", "test-marketplace", "claude-notifications-go", "1.41.0")
			if err := os.MkdirAll(filepath.Join(cache, ".codex-plugin"), 0700); err != nil {
				t.Fatal(err)
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, "config.toml"), "[plugins.\""+id+"\"]\nenabled = true\n")
			write(filepath.Join(cache, ".codex-plugin", "plugin.json"), `{"name":"claude-notifications-go","version":"1.41.0","hooks":"./hooks/hooks-codex.json"}`)
			// Synthetic schema fixture for the qualification seam, not actual-client proof.
			observed := notificationCodexProbeResult{ClientVersion: version, Installed: []byte(`{"installed":[{"id":"` + id + `","name":"claude-notifications-go","marketplaceName":"test-marketplace","version":"1.41.0","enabled":true,"source":{"path":"/authored/source-not-cache"}}]}`)}
			probe := func(context.Context, string) (notificationCodexProbeResult, error) { return observed, nil }
			if _, err := inspectCodexNotificationPackages(context.Background(), home, probe); err == nil {
				t.Fatal("unqualified no-skill layout accepted")
			}
			observed.CacheLayoutQualified = true
			inv, err := inspectCodexNotificationPackages(context.Background(), home, probe)
			if err != nil || inv.State != "clear" || inv.Skill {
				t.Fatal(inv, err)
			}
			if err = os.MkdirAll(filepath.Join(cache, "skills", "agent-notify"), 0700); err != nil {
				t.Fatal(err)
			}
			skillPath := filepath.Join(cache, "skills", "agent-notify", "SKILL.md")
			write(skillPath, "canonical skill")
			if inv.Revalidate(context.Background()) == nil {
				t.Fatal("skill drift missed")
			}
			if _, err = inspectCodexNotificationPackages(context.Background(), home, probe); err == nil {
				t.Fatal("skill without discovery anchor accepted")
			}
			observed.Skills = []notificationCodexSkill{{Name: "claude-notifications-go:agent-notify", PluginID: id, Path: skillPath, Enabled: true}}
			inv, err = inspectCodexNotificationPackages(context.Background(), home, probe)
			if err != nil || !inv.Skill {
				t.Fatal(inv, err)
			}
			observed.Skills[0].Path = "/authored/source-not-cache/skills/agent-notify/SKILL.md"
			if _, err = inspectCodexNotificationPackages(context.Background(), home, probe); err == nil {
				t.Fatal("authored source accepted")
			}
			observed.Skills[0].Path = skillPath
			write(filepath.Join(cache, ".mcp.json"), `{"mcpServers":{"agent_notifications":{"command":"/usr/bin/true"}}}`)
			inv, err = inspectCodexNotificationPackages(context.Background(), home, probe)
			if err != nil || inv.State != "collision" {
				t.Fatal(inv, err)
			}
			// Exact installed version wins even with old cache versions alongside it.
			old := filepath.Join(home, "plugins", "cache", "test-marketplace", "claude-notifications-go", "0.1.0")
			if err := os.MkdirAll(old, 0700); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(old, ".mcp.json"), "malformed old version must not be read")
			observed.Installed = []byte(strings.Replace(string(observed.Installed), `"enabled":true`, `"enabled":false`, 1))
			write(filepath.Join(home, "config.toml"), "[plugins.\""+id+"\"]\nenabled = false\n[plugins.\"unrelated@market\"]\nenabled = true\n")
			observed.Installed = []byte(strings.Replace(string(observed.Installed), `]}`, `,{"id":"unrelated@market","name":"unrelated","enabled":true}]}`, 1))
			observed.Skills = nil
			inv, err = inspectCodexNotificationPackages(context.Background(), home, probe)
			if err != nil || inv.State != "clear" || !inv.DisabledPackage || inv.Skill {
				t.Fatal("disabled/unrelated packages", inv, err)
			}
			observed.ClientVersion = "codex-cli 0.153.5"
			if _, err = inspectCodexNotificationPackages(context.Background(), home, probe); err == nil {
				t.Fatal("unsupported version accepted")
			}
		})
	}
}

func TestNotificationCodexSkillsInventoryExchange(t *testing.T) {
	home := setupCommandRoot(t)
	path := filepath.Join(home, "plugins", "cache", "market", "claude-notifications-go", "1.41.0", "skills", "agent-notify", "SKILL.md")
	result, _ := json.Marshal(map[string]any{"id": 2, "result": map[string]any{"data": []any{map[string]any{"cwd": home, "errors": []any{}, "skills": []any{map[string]any{"name": "claude-notifications-go:agent-notify", "pluginId": "claude-notifications-go@market", "enabled": true, "path": path}}}}}})
	var sent strings.Builder
	skills, err := notificationCodexSkillsExchange(&sent, strings.NewReader("{\"id\":1,\"result\":{}}\n"+string(result)+"\n"), home)
	if err != nil || len(skills) != 1 || skills[0].Path != path {
		t.Fatal(skills, err)
	}
	if strings.Contains(sent.String(), "thread") || strings.Contains(sent.String(), "mcp") || !strings.Contains(sent.String(), "skills/list") {
		t.Fatal(sent.String())
	}
	for _, raw := range []string{"", `{"id":1,"error":{"message":"fixture"}}` + "\n", `{"id":1,"result":{}}` + "\n" + `{"id":2,"result":{"data":[]}}` + "\n"} {
		if _, err := notificationCodexSkillsExchange(&sent, strings.NewReader(raw), home); err == nil {
			t.Fatal("unknown accepted")
		}
	}
}

// Neither a client nor a subprocess is needed to exercise a held-open pipe.
func TestNotificationCodexSkillsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	defer outputReader.Close()
	defer outputWriter.Close()
	defer cancel()
	started := make(chan struct{})
	go func() {
		var b [1]byte
		if _, err := inputReader.Read(b[:]); err == nil {
			close(started)
		}
		io.Copy(io.Discard, inputReader)
	}()
	finished := make(chan error, 1)
	home := t.TempDir()
	go func() {
		_, err := notificationCodexSkillsExchangeContext(ctx, inputWriter, outputReader, home)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("exchange did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation left the inventory reader blocked")
	}
}
func TestNotificationCodexSkillsDuplicateFields(t *testing.T) {
	var sent strings.Builder
	response := `{"id":1,"id":1,"result":{}}` + "\n"
	if _, err := notificationCodexSkillsExchange(&sent, strings.NewReader(response), t.TempDir()); err == nil {
		t.Fatal("ambiguous response accepted")
	}
}

func TestNotificationCodexProductionQualificationFlag(t *testing.T) {
	for _, version := range []string{"codex-cli 0.152.0", "codex-cli 0.153.4"} {
		t.Run(version, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			// Inert CLI transport: no client, network or model starts.
			path := filepath.Join(home, "codex")
			if err := os.WriteFile(path, []byte("#!/bin/sh\ncase \"$*\" in --version) echo '"+version+"';; 'plugin list --json') echo '{\"installed\":[]}';; *) exit 1;; esac\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))
			if _, err := exec.LookPath("codex"); err != nil {
				t.Fatal(err)
			}
			observed, err := probeNotificationCodex(context.Background(), home)
			if err != nil || !observed.CacheLayoutQualified {
				t.Fatal(observed, err)
			}
		})
	}
}

func TestNotificationCodexUnqualifiedVersions(t *testing.T) {
	for _, version := range []string{"codex-cli 0.151.0", "codex-cli 0.153.5", "", "codex-cli 0.154.0"} {
		t.Run(version, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			t.Setenv("PATH", home)
			script := "#!/bin/sh\ncase \"$*\" in --version) echo '" + version + "';; *) exit 97;; esac\n"
			if err := os.WriteFile(filepath.Join(home, "codex"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			observed, err := probeNotificationCodex(context.Background(), home)
			if err == nil || observed.CacheLayoutQualified || observed.Installed != nil {
				t.Fatal("unqualified probe accepted", observed, err)
			}
			inv, err := inspectCodexNotificationPackages(context.Background(), home, func(context.Context, string) (notificationCodexProbeResult, error) {
				return notificationCodexProbeResult{ClientVersion: version, CacheLayoutQualified: true, Installed: []byte(`{"installed":[]}`)}, nil
			})
			if err == nil || inv.State != "unknown" {
				t.Fatal("unqualified inspector accepted", inv, err)
			}
		})
	}
}
