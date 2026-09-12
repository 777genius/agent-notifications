package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/strictjson"
	"github.com/pelletier/go-toml/v2"
)

type notificationInventory struct {
	DisabledPackage bool
	State           string
	Skill           bool
	Revalidate      func(context.Context) error
}
type notificationInventoryEvidence map[string][32]byte

// Inventory reads only exact user registries/config and identified own packages.
// A missing registry is clear only where the corresponding plugin root is absent.
// Unsupported installed Codex inventories remain unknown pending a qualified
// client adapter; authored source.path and plugin/read never authorize writes.
func inspectNotificationInventory(ctx context.Context, provider registration.Provider, home string) (notificationInventory, error) {
	evidence := notificationInventoryEvidence{}
	disabled := false
	read := func(path string) ([]byte, error) { return inventoryRead(ctx, path, evidence) }
	finish := func(skill bool) (notificationInventory, error) {
		return notificationInventory{DisabledPackage: disabled, State: "clear", Skill: skill, Revalidate: func(ctx context.Context) error {
			current := notificationInventoryEvidence{}
			for path := range evidence {
				if _, err := inventoryRead(ctx, path, current); err != nil {
					return err
				}
			}
			if !reflect.DeepEqual(evidence, current) {
				return errors.New("inventory_changed")
			}
			return nil
		}}, nil
	}
	unknown := func() (notificationInventory, error) {
		return notificationInventory{State: "unknown"}, errors.New("inventory_unknown")
	}
	if provider == registration.Codex {
		configPath := filepath.Join(home, "config.toml")
		raw, err := read(configPath)
		delete(evidence, configPath)
		if err != nil {
			return unknown()
		}
		var config map[string]any
		if len(raw) > 0 && toml.Unmarshal(raw, &config) != nil {
			return unknown()
		}
		if plugins, ok := config["plugins"]; ok {
			entries, ok := plugins.(map[string]any)
			if !ok {
				return unknown()
			}
			for key := range entries {
				if ownNotificationPlugin(key) {
					return inspectCodexNotificationPackages(ctx, home, probeNotificationCodex)
				}
			}
		}
		// No directory traversal: an existing registry/cache requires the qualified
		// installed-envelope/cache adapter, not a guessed most-recent package.
		root := filepath.Join(home, "plugins")
		if _, err = os.Lstat(root); err == nil {
			return inspectCodexNotificationPackages(ctx, home, probeNotificationCodex)
		}
		if _, err = read(root); err != nil {
			return unknown()
		}
		result, err := finish(false)
		if err != nil {
			return result, err
		}
		check := result.Revalidate
		oldPlugins := config["plugins"]
		result.Revalidate = func(ctx context.Context) error {
			if err := check(ctx); err != nil {
				return err
			}
			raw, err := inventoryRead(ctx, configPath, notificationInventoryEvidence{})
			if err != nil {
				return err
			}
			var current map[string]any
			if len(raw) > 0 && toml.Unmarshal(raw, &current) != nil {
				return errors.New("inventory_unknown")
			}
			if !reflect.DeepEqual(oldPlugins, current["plugins"]) {
				return errors.New("inventory_changed")
			}
			return nil
		}
		return result, nil
	}
	registry := filepath.Join(home, "plugins", "installed_plugins.json")
	raw, err := read(registry)
	if err != nil {
		return unknown()
	}
	settings, err := read(filepath.Join(home, "settings.json"))
	if err != nil {
		return unknown()
	}
	activation := struct{ EnabledPlugins map[string]bool }{EnabledPlugins: map[string]bool{}}
	if len(settings) > 0 {
		var fields map[string]json.RawMessage
		if strictjson.Validate(settings, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(settings, &fields) != nil || fields == nil {
			return unknown()
		}
		for key := range fields {
			if strings.EqualFold(key, "enabledPlugins") && key != "enabledPlugins" {
				return unknown()
			}
		}
		if value, exists := fields["enabledPlugins"]; exists {
			var entries map[string]json.RawMessage
			if json.Unmarshal(value, &entries) != nil || entries == nil {
				return unknown()
			}
			for key, value := range entries {
				switch strings.TrimSpace(string(value)) {
				case "true":
					activation.EnabledPlugins[key] = true
				case "false":
					activation.EnabledPlugins[key] = false
				default:
					return unknown()
				}
			}
		}
	}
	if len(raw) == 0 {
		for key := range activation.EnabledPlugins {
			if ownNotificationPlugin(key) {
				return unknown()
			}
		}
		if _, err = read(filepath.Join(home, "plugins")); err != nil {
			return unknown()
		}
		return finish(false)
	}
	var installed struct {
		Version int `json:"version"`
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			Version     string `json:"version"`
		} `json:"plugins"`
	}
	if strictjson.Validate(raw, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(raw, &installed) != nil || installed.Version != 2 || installed.Plugins == nil {
		return unknown()
	}
	for key, entries := range installed.Plugins {
		if !ownNotificationPlugin(key) {
			continue
		}
		active, known := activation.EnabledPlugins[key]
		if known && !active {
			disabled = true
		}
		if !known {
			return unknown()
		}
		if len(entries) != 1 || entries[0].Scope != "user" {
			return unknown()
		}
		entry := entries[0]
		if !configurePhysical(entry.InstallPath) || entry.Version == "" {
			return unknown()
		}
		manifest, err := read(filepath.Join(entry.InstallPath, ".claude-plugin", "plugin.json"))
		if err != nil {
			return unknown()
		}
		var m map[string]json.RawMessage
		if strictjson.Validate(manifest, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(manifest, &m) != nil {
			return unknown()
		}
		var name, version string
		if json.Unmarshal(m["name"], &name) != nil || name != "claude-notifications-go" || json.Unmarshal(m["version"], &version) != nil || version != entry.Version {
			return unknown()
		}
		declaration, err := read(filepath.Join(entry.InstallPath, ".mcp.json"))
		if err != nil {
			return unknown()
		}
		if active {
			if _, present := m["mcpServers"]; present {
				return notificationInventory{State: "collision"}, nil
			}
			if len(declaration) > 0 {
				var servers struct {
					MCPServers map[string]json.RawMessage `json:"mcpServers"`
				}
				if strictjson.Validate(declaration, strictjson.Budget{Bytes: 1 << 20, Depth: 24, Entries: 10000}) != nil || json.Unmarshal(declaration, &servers) != nil || servers.MCPServers == nil {
					return unknown()
				}
				if len(servers.MCPServers) > 0 {
					return notificationInventory{State: "collision"}, nil
				}
			}
		}
	}
	return finish(false)
}
func ownNotificationPlugin(key string) bool {
	return key == "claude-notifications-go" || strings.HasPrefix(key, "claude-notifications-go@")
}

func inventoryRead(ctx context.Context, path string, evidence notificationInventoryEvidence) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !configurePhysical(path) {
		return nil, errors.New("inventory_path")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		evidence[path] = sha256.Sum256([]byte("absent"))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("inventory_document")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.New("inventory_changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("inventory_document")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	evidence[path] = sha256.Sum256(append([]byte(info.Mode().String()+"\x00"), raw...))
	return raw, nil
}
