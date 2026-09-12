package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/pelletier/go-toml/v2"
)

// These are inventory-only RPC facts, never plugin/read authored package data.
// The controller may supply qualified skills/list observations through this seam;
// configure never opens a model session or an MCP connection to obtain them.
type notificationCodexSkill struct {
	Name, PluginID, Path string
	Enabled              bool
}
type notificationCodexProbeResult struct {
	// Exact 0.152.0 and 0.153.4 add/list cache layout was qualified by the controller.
	CacheLayoutQualified bool
	ClientVersion        string
	Installed            []byte
	Skills               []notificationCodexSkill
}
type notificationCodexProbe func(context.Context, string) (notificationCodexProbeResult, error)

// CLI inventory is bounded by the configuration deadline and shorter subprocess
// budgets. No plugin installation, thread, model or MCP connection is requested.
func probeNotificationCodex(ctx context.Context, home string) (notificationCodexProbeResult, error) {
	var result notificationCodexProbeResult
	run := func(args ...string) ([]byte, error) {
		child, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(child, "codex", args...)
		cmd.Dir = home
		cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
		cmd.WaitDelay = time.Second
		output := &notificationInventoryBuffer{}
		cmd.Stdout = output
		cmd.Stderr = &notificationInventoryBuffer{}
		err := cmd.Run()
		return output.Bytes(), err
	}
	version, err := run("--version")
	if err != nil {
		return result, err
	}
	result.ClientVersion = strings.TrimSpace(string(version))
	if !qualifiedNotificationCodexVersion(result.ClientVersion) {
		return result, errors.New("unsupported_codex_version")
	}
	result.CacheLayoutQualified = true
	result.Installed, err = run("plugin", "list", "--json")
	if err != nil {
		return result, err
	}
	// Only own installed identities need the additional skill discovery probe.
	var envelope struct {
		Installed []struct {
			Name     string `json:"name"`
			ID       string `json:"id"`
			PluginID string `json:"pluginId"`
		} `json:"installed"`
	}
	if json.Unmarshal(result.Installed, &envelope) != nil {
		return result, errors.New("inventory_unknown")
	}
	for _, item := range envelope.Installed {
		if ownNotificationPlugin(item.ID) || ownNotificationPlugin(item.PluginID) || item.Name == "claude-notifications-go" {
			result.Skills, err = probeNotificationCodexSkills(ctx, home)
			break
		}
	}
	return result, err
}

type notificationInventoryBuffer struct{ bytes.Buffer }

func (b *notificationInventoryBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("inventory_limit")
	}
	return b.Buffer.Write(p)
}

// The cache adapter is intentionally pinned. The controller supplied evidence; the
// exact layout was qualified against actual 0.152.0 and 0.153.4 add/list observations.
// Missing, ambiguous or unsupported identities fail closed, without cache scans.
func inspectCodexNotificationPackages(ctx context.Context, home string, probe notificationCodexProbe) (notificationInventory, error) {
	unknown := func() (notificationInventory, error) {
		return notificationInventory{State: "unknown"}, errors.New("inventory_unknown")
	}
	observed, err := probe(ctx, home)
	if err != nil || !qualifiedNotificationCodexVersion(observed.ClientVersion) {
		return unknown()
	}
	if strictjson.Validate(observed.Installed, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil {
		return unknown()
	}
	var envelope struct {
		Installed []json.RawMessage `json:"installed"`
	}
	if json.Unmarshal(observed.Installed, &envelope) != nil || envelope.Installed == nil {
		return unknown()
	}
	evidence := notificationInventoryEvidence{}
	read := func(p string) ([]byte, error) { return inventoryRead(ctx, p, evidence) }
	configPath := filepath.Join(home, "config.toml")
	configRaw, err := read(configPath)
	delete(evidence, configPath)
	if err != nil {
		return unknown()
	}
	var config struct {
		Plugins map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if len(configRaw) > 0 && toml.Unmarshal(configRaw, &config) != nil {
		return unknown()
	}
	selected := map[string]bool{}
	skill := false
	disabled := false
	for _, raw := range envelope.Installed {
		var item struct {
			MarketplaceName string          `json:"marketplaceName"`
			ID              string          `json:"id"`
			PluginID        string          `json:"pluginId"`
			Name            string          `json:"name"`
			Version         string          `json:"version"`
			Enabled         *bool           `json:"enabled"`
			Source          json.RawMessage `json:"source"`
		}
		if json.Unmarshal(raw, &item) != nil {
			return unknown()
		}
		id := item.ID
		if id == "" {
			id = item.PluginID
		} else if item.PluginID != "" && item.PluginID != id {
			return unknown()
		}
		if !ownNotificationPlugin(id) && item.Name != "claude-notifications-go" {
			continue
		}
		parts := strings.Split(id, "@")
		if len(parts) != 2 || item.Name != "claude-notifications-go" || (item.MarketplaceName != "" && item.MarketplaceName != parts[1]) || parts[0] != "claude-notifications-go" || !notificationCacheToken(parts[1]) || !notificationCacheToken(item.Version) || item.Enabled == nil || selected[id] {
			return unknown()
		}
		selected[id] = true
		configured, ok := config.Plugins[id]
		if !ok || configured.Enabled == nil || *configured.Enabled != *item.Enabled {
			return unknown()
		}
		// Authored source must be present but is never followed or fingerprinted.
		var source map[string]json.RawMessage
		if json.Unmarshal(item.Source, &source) != nil || source == nil {
			return unknown()
		}
		cache := filepath.Join(home, "plugins", "cache", parts[1], parts[0], item.Version)
		if !configurePhysical(cache) {
			return unknown()
		}
		manifest, err := read(filepath.Join(cache, ".codex-plugin", "plugin.json"))
		if err != nil {
			return unknown()
		}
		var m map[string]json.RawMessage
		if strictjson.Validate(manifest, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(manifest, &m) != nil {
			return unknown()
		}
		var name, version string
		if json.Unmarshal(m["name"], &name) != nil || name != parts[0] || json.Unmarshal(m["version"], &version) != nil || version != item.Version {
			return unknown()
		}
		canonicalSkill := filepath.Join(cache, "skills", "agent-notify", "SKILL.md")
		skillRaw, err := read(canonicalSkill)
		if err != nil {
			return unknown()
		}
		declaration, err := read(filepath.Join(cache, ".mcp.json"))
		if err != nil {
			return unknown()
		}
		if !*item.Enabled {
			disabled = true
			continue
		}
		if _, ok := m["mcpServers"]; ok {
			return notificationInventory{State: "collision"}, nil
		}
		if len(declaration) > 0 {
			var mcp struct {
				Servers map[string]json.RawMessage `json:"mcpServers"`
			}
			if strictjson.Validate(declaration, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(declaration, &mcp) != nil || mcp.Servers == nil {
				return unknown()
			}
			if len(mcp.Servers) > 0 {
				return notificationInventory{State: "collision"}, nil
			}
		}
		for _, discovered := range observed.Skills {
			if discovered.Enabled && discovered.PluginID == id && discovered.Name == parts[0]+":agent-notify" && (len(skillRaw) == 0 || discovered.Path != canonicalSkill) {
				return unknown()
			}
		}
		if len(skillRaw) > 0 {
			anchors := 0
			for _, s := range observed.Skills {
				if s.Enabled && s.PluginID == id && s.Name == parts[0]+":agent-notify" && s.Path == canonicalSkill {
					anchors++
				}
			}
			if anchors != 1 || skill {
				return unknown()
			}
			skill = true
		} else if !observed.CacheLayoutQualified {
			return unknown()
		} else if _, ok := m["skills"]; ok {
			return unknown()
		}
	}
	for id := range config.Plugins {
		if ownNotificationPlugin(id) && !selected[id] {
			return unknown()
		}
	}
	return notificationInventory{DisabledPackage: disabled, State: "clear", Skill: skill, Revalidate: func(ctx context.Context) error {
		next, err := probe(ctx, home)
		if err != nil {
			return err
		}
		a, _ := json.Marshal(observed)
		b, _ := json.Marshal(next)
		if !bytes.Equal(a, b) {
			return errors.New("inventory_changed")
		}
		for path, want := range evidence {
			current := notificationInventoryEvidence{}
			if _, err := inventoryRead(ctx, path, current); err != nil {
				return err
			}
			if current[path] != want {
				return errors.New("inventory_changed")
			}
		}
		raw, err := inventoryRead(ctx, configPath, notificationInventoryEvidence{})
		if err != nil {
			return err
		}
		var current struct {
			Plugins map[string]struct {
				Enabled *bool `toml:"enabled"`
			} `toml:"plugins"`
		}
		if len(raw) > 0 && toml.Unmarshal(raw, &current) != nil {
			return errors.New("inventory_unknown")
		}
		a, _ = json.Marshal(config)
		b, _ = json.Marshal(current)
		if !bytes.Equal(a, b) {
			return errors.New("inventory_changed")
		}
		return nil
	}}, nil
}
func notificationCacheToken(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Only these exact CLI versions have controller-qualified inventory evidence.
func qualifiedNotificationCodexVersion(version string) bool {
	return version == "codex-cli 0.152.0" || version == "codex-cli 0.153.4"
}
