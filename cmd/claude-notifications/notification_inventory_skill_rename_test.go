//go:build linux || darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// This fails if renamed discovery is rejected, historical installed discovery
// stops working, or two/mismatched skill discoveries bypass collision fencing.
func TestNotificationCodexSkillRenameInventory(t *testing.T) {
	for _, layout := range []string{"agent-notifications", "agent-notify", "both", "mismatched-name", "stale-legacy-discovery"} {
		t.Run(layout, func(t *testing.T) {
			home := setupCommandRoot(t)
			id := "claude-notifications-go@test-marketplace"
			cache := filepath.Join(home, "plugins", "cache", "test-marketplace", "claude-notifications-go", "1.46.0")
			write := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, "config.toml"), "[plugins.\""+id+"\"]\nenabled = true\n")
			write(filepath.Join(cache, ".codex-plugin", "plugin.json"), `{"name":"claude-notifications-go","version":"1.46.0"}`)
			name := "agent-notifications"
			if layout == "agent-notify" {
				name = layout
			}
			path := filepath.Join(cache, "skills", name, "SKILL.md")
			write(path, "skill fixture")
			observed := notificationCodexProbeResult{ClientVersion: "codex-cli 0.153.4", CacheLayoutQualified: true, Installed: []byte(`{"installed":[{"id":"` + id + `","name":"claude-notifications-go","marketplaceName":"test-marketplace","version":"1.46.0","enabled":true,"source":{"type":"local"}}]}`), Skills: []notificationCodexSkill{{Name: "claude-notifications-go:" + name, PluginID: id, Path: path, Enabled: true}}}
			if layout == "both" {
				write(filepath.Join(cache, "skills", "agent-notify", "SKILL.md"), "old skill fixture")
			}
			if layout == "mismatched-name" {
				observed.Skills[0].Name = "claude-notifications-go:agent-notify"
			}
			if layout == "stale-legacy-discovery" {
				observed.Skills = append(observed.Skills, notificationCodexSkill{Name: "claude-notifications-go:agent-notify", PluginID: id, Path: filepath.Join(cache, "skills", "agent-notify", "SKILL.md"), Enabled: true})
			}
			inv, err := inspectCodexNotificationPackages(context.Background(), home, func(context.Context, string) (notificationCodexProbeResult, error) { return observed, nil })
			if layout == "agent-notifications" || layout == "agent-notify" {
				if err != nil || inv.State != "clear" || !inv.Skill {
					t.Fatal("valid discovery rejected", inv, err)
				}
			} else if err == nil {
				t.Fatal("ambiguous discovery accepted", inv)
			}
		})
	}
}
