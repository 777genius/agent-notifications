//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Enter must select Desktop, and explicit choices must still replace that
// default. An accidental Webhook default or a config write makes this red.
func TestSetupProductsChannelsDefaults(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_CONFIG_DIR", "GEMINI_CLI_HOME", "AGENT_NOTIFICATIONS_CONTROL_ROOT"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	for _, product := range []struct{ ids, title string }{{"claude,codex,opencode,gemini", "Notification channels for OpenCode, Gemini CLI"}, {"cursor", "Notification channels for Cursor CLI (explicit profile)"}} {
		for _, tc := range []struct{ name, input, want string }{
			{"enter", "\n", "desktop\n"},
			{"both", "desktop,webhook\n", "desktop,webhook\n"},
			{"webhook-only", "webhook\n", "webhook\n"},
			{"cancel", "cancel\n", ""},
		} {
			t.Run(product.ids+"/"+tc.name, func(t *testing.T) {
				var out, prompt bytes.Buffer
				code := runSetupProducts([]string{"channels", "--products", product.ids}, strings.NewReader(tc.input), &out, &prompt)
				if code != 0 || out.String() != tc.want {
					t.Fatalf("code=%d output=%q prompt=%s", code, out.String(), prompt.String())
				}
				for _, want := range []string{product.title, "Desktop notifications (recommended)", "requires a configured destination"} {
					if !strings.Contains(prompt.String(), want) {
						t.Fatalf("missing %q: %s", want, prompt.String())
					}
				}
				if tc.want == "" && !strings.Contains(prompt.String(), "Installation cancelled. No changes were applied.") {
					t.Fatal("cancellation was silent")
				}
			})
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("channel selection changed config roots: %v %v", entries, err)
	}
}

// A readable summary must still disclose every exact authority, preserve off
// choices, distinguish portable channels, and never render terminal controls.
func TestBootstrapReadableSummary(t *testing.T) {
	i, _ := bootstrapCodecFixture(t)
	a, err := parseSetupProducts([]string{"confirm", "--products", "claude,codex,opencode,gemini", "--desktop"})
	if err != nil {
		t.Fatal(err)
	}
	i.Request = a
	i.Units = []bootstrapProductUnits{
		{Product: "claude", Hooks: true, MCP: true, Skill: true},
		{Product: "codex", Hooks: true, PreservedOff: true},
		{Product: "opencode", Native: true, Desktop: true},
		{Product: "gemini", Native: true, Desktop: true},
		{Product: "cursor", MCP: true, Skill: true, Webhook: true},
	}
	for _, key := range intentScalarKeys {
		i.Scopes[key] = []byte("/TEST " + key + "/exact-end")
	}
	i.Scopes["runtime-root"] = []byte("/TEST raw-\xff-\x1b[31m/\u202e/end")
	i.Scopes["codex-home"] = []byte("/TEST trailing ")
	policy := map[string]json.RawMessage{"enabled": json.RawMessage("false"), "route": json.RawMessage(`{"localRouting":true,"allowUnknownCaller":false,"allowCallerAsserted":false,"applicationPath":"/TEST saved ","teamID":"TEST"}`)}
	rows, err := bootstrapIntentSummary(i, policy)
	if err != nil {
		t.Fatal(err)
	}
	shown := strings.Join(rows, "\n")
	for _, want := range []string{
		"Installation summary", "Claude: automatic notification hooks, agent-notify MCP, agent-notify skill", "Codex: automatic notification hooks, agent-notify stays off",
		"Cursor CLI (explicit profile): agent-notify MCP, agent-notify skill\n    Channels: Desktop off, Webhook on", "Cursor workspace: /TEST scope-root/exact-end", "Cursor agent executable: /TEST client-executable/exact-end",
		"Channels: Desktop on, Webhook off", "channels use your existing notification settings", "Notification service: off (kept)",
		`raw-\xff-\x1b[31m/\u202e/end`, `Codex home: \"/TEST trailing \"`, `Desktop application: \"/TEST saved \" (kept)`,
		i.Provenance.SourceCommit, i.Provenance.SHA256, "partial installation", "enabled destination",
	} {
		if !strings.Contains(shown, want) {
			t.Fatalf("missing %q in %s", want, shown)
		}
	}
	for key, value := range i.Scopes {
		if key != "runtime-root" && key != "codex-home" && !strings.Contains(shown, string(value)) {
			t.Fatalf("authority %s omitted", key)
		}
	}
	if strings.ContainsAny(shown, "\x1b\u202e") || strings.Contains(shown, "hooks=true") || rows[0] != "Installation summary" {
		t.Fatalf("unsafe or raw summary: %q", shown)
	}
}

// Help is part of the same consent display budget, even for the line API.
func TestSetupProductsConfirmationDisplayBudget(t *testing.T) {
	for _, rich := range []bool{false, true} {
		if _, err := setupProductsConfirmationRows(make([]string, 126), rich); err != nil {
			t.Fatalf("126 rows refused: %v", err)
		}
		if _, err := setupProductsConfirmationRows(make([]string, 127), rich); err == nil {
			t.Fatal("help overflowed 128-row limit")
		}
		rows := make([]string, 126)
		for n := range rows {
			rows[n] = strings.Repeat("x", 518)
		}
		if _, err := setupProductsConfirmationRows(rows, rich); err != nil {
			t.Fatalf("bounded display refused: %v", err)
		}
		rows[0] += strings.Repeat("x", 500)
		if _, err := setupProductsConfirmationRows(rows, rich); err == nil {
			t.Fatal("help overflowed 64 KiB limit")
		}
	}
}

// Cursor channel consent updates the shared route leaf. Its summary must not
// promise that all existing shared preferences stay unchanged.
func TestBootstrapCursorChannelPreferencesSummary(t *testing.T) {
	i, _ := bootstrapCodecFixture(t)
	a, err := parseSetupProducts([]string{"confirm", "--products", "cursor", "--webhook"})
	if err != nil {
		t.Fatal(err)
	}
	i.Request = a
	i.Units = []bootstrapProductUnits{{Product: "cursor", MCP: true, Skill: true, Webhook: true}}
	rows, err := bootstrapIntentSummary(i, nil)
	if err != nil {
		t.Fatal(err)
	}
	shown := strings.Join(rows, "\n")
	if !strings.Contains(shown, "Update shared Cursor Desktop/Webhook preferences to the channels shown above.") || !strings.Contains(shown, "Channels: Desktop off, Webhook on") || strings.Contains(shown, "Keep existing shared notification preferences.") {
		t.Fatalf("Cursor channel update is not disclosed: %s", shown)
	}
}
