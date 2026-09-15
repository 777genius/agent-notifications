package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..")
}

type codexManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Hooks   string `json:"hooks"`
}

type claudeManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type marketplaceManifest struct {
	Name     string `json:"name"`
	Metadata struct {
		Version string `json:"version"`
	} `json:"metadata"`
	Plugins []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"plugins"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// TestCodexManifestContract parses the real Codex plugin manifest, resolves
// the custom hooks path, and asserts version equality across every shipped
// manifest and the Go binary. A drift in any of the five occurrences is a
// release blocker.
func TestCodexManifestContract(t *testing.T) {
	root := repoRoot(t)

	var codexM codexManifest
	readJSON(t, filepath.Join(root, ".codex-plugin", "plugin.json"), &codexM)

	if codexM.Name != "claude-notifications-go" {
		t.Errorf("codex manifest name = %q; frozen compatibility field", codexM.Name)
	}
	if codexM.Hooks != "./hooks/hooks-codex.json" {
		t.Errorf("codex manifest hooks = %q; frozen compatibility field", codexM.Hooks)
	}
	hooksPath := filepath.Join(root, filepath.FromSlash(codexM.Hooks))
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("declared hooks file missing: %v", err)
	}

	var claudeM claudeManifest
	readJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), &claudeM)

	var marketM marketplaceManifest
	readJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), &marketM)

	if marketM.Name != "claude-notifications-go" {
		t.Errorf("marketplace name = %q; frozen compatibility field", marketM.Name)
	}

	pluginEntryVersion := ""
	for _, p := range marketM.Plugins {
		if p.Name == codexM.Name {
			pluginEntryVersion = p.Version
		}
	}
	if pluginEntryVersion == "" {
		t.Fatalf("marketplace has no plugin entry named %q", codexM.Name)
	}

	versions := map[string]string{
		"binary const":               version,
		".codex-plugin/plugin.json":  codexM.Version,
		".claude-plugin/plugin.json": claudeM.Version,
		"marketplace metadata":       marketM.Metadata.Version,
		"marketplace plugin entry":   pluginEntryVersion,
	}
	for name, v := range versions {
		if v != version {
			t.Errorf("version mismatch: %s = %q, binary = %q", name, v, version)
		}
	}
}

type codexHooksFile struct {
	Description string `json:"description"`
	Hooks       map[string][]struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []struct {
			Type                   string `json:"type"`
			Command                string `json:"command"`
			CommandWindows         string `json:"commandWindows"`
			Timeout                int    `json:"timeout"`
			Async                  bool   `json:"async"`
			StatusMessage          string `json:"statusMessage,omitempty"`
			AdditionalContextLimit int    `json:"additionalContextLimit,omitempty"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// TestCodexHookIdentityGolden freezes the normalized Codex hook identity.
// Codex trust-hashes the platform-effective command with timeout/async, so
// after the first public Codex release any change here forces an explicit
// re-trust migration. Unix and Windows effective commands are asserted
// separately because Codex hashes only the selected variant.
func TestCodexHookIdentityGolden(t *testing.T) {
	root := repoRoot(t)

	var hf codexHooksFile
	readJSON(t, filepath.Join(root, "hooks", "hooks-codex.json"), &hf)

	golden := map[string]struct {
		command        string
		commandWindows string
		matcher        string
	}{
		"Stop": {
			command:        `sh "${PLUGIN_ROOT}/bin/codex-hook-wrapper.sh" handle-hook Stop --product codex`,
			commandWindows: `cmd.exe /d /v:off /s /c ""${PLUGIN_ROOT}\bin\codex-hook-wrapper.cmd" handle-hook Stop --product codex"`,
		},
		"PreToolUse": {
			command:        `sh "${PLUGIN_ROOT}/bin/codex-hook-wrapper.sh" handle-hook PreToolUse --product codex`,
			commandWindows: `cmd.exe /d /v:off /s /c ""${PLUGIN_ROOT}\bin\codex-hook-wrapper.cmd" handle-hook PreToolUse --product codex"`,
			matcher:        `^request_user_input$`,
		},
		"SubagentStop": {
			command:        `sh "${PLUGIN_ROOT}/bin/codex-hook-wrapper.sh" handle-hook SubagentStop --product codex`,
			commandWindows: `cmd.exe /d /v:off /s /c ""${PLUGIN_ROOT}\bin\codex-hook-wrapper.cmd" handle-hook SubagentStop --product codex"`,
		},
		"PermissionRequest": {
			command:        `sh "${PLUGIN_ROOT}/bin/codex-hook-wrapper.sh" handle-hook PermissionRequest --product codex`,
			commandWindows: `cmd.exe /d /v:off /s /c ""${PLUGIN_ROOT}\bin\codex-hook-wrapper.cmd" handle-hook PermissionRequest --product codex"`,
		},
	}

	if len(hf.Hooks) != len(golden) {
		t.Fatalf("hooks-codex.json declares %d events, want %d", len(hf.Hooks), len(golden))
	}

	for event, want := range golden {
		groups, ok := hf.Hooks[event]
		if !ok {
			t.Errorf("event %s missing", event)
			continue
		}
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Errorf("event %s must have exactly one group with one handler", event)
			continue
		}
		g := groups[0]
		hk := g.Hooks[0]
		if g.Matcher != want.matcher {
			t.Errorf("%s: matcher = %q, want %q", event, g.Matcher, want.matcher)
		}
		if hk.Type != "command" {
			t.Errorf("%s: type = %q", event, hk.Type)
		}
		if hk.Command != want.command {
			t.Errorf("%s: unix command drift:\n got %q\nwant %q", event, hk.Command, want.command)
		}
		if hk.CommandWindows != want.commandWindows {
			t.Errorf("%s: windows command drift:\n got %q\nwant %q", event, hk.CommandWindows, want.commandWindows)
		}
		if hk.Timeout != 30 {
			t.Errorf("%s: timeout = %d, want 30", event, hk.Timeout)
		}
		// Synchronous by measurement: an async handler never runs under
		// `codex exec`, which exits as soon as the turn ends.
		if hk.Async {
			t.Errorf("%s: async must stay false", event)
		}
		if hk.StatusMessage != "" || hk.AdditionalContextLimit != 0 {
			t.Errorf("%s: statusMessage/additionalContextLimit must stay omitted", event)
		}
	}

	// The configured launchers must exist at their frozen paths.
	for _, launcher := range []string{"bin/codex-hook-wrapper.sh", "bin/codex-hook-wrapper.cmd"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(launcher))); err != nil {
			t.Errorf("frozen launcher path missing: %v", err)
		}
	}
}
