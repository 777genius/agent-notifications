package config

import (
	"encoding/json"
	"testing"
)

func TestSystemSoundRequiresExplicitConsent(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IsSystemSoundEnabled() {
		t.Fatal("default or inherited sound enabled the observer's system cue")
	}
	on, off := true, false
	cfg.Notifications.Desktop.SystemSound = &on
	if !cfg.IsSystemSoundEnabled() {
		t.Fatal("explicit system sound not honored")
	}
	cfg.Notifications.Desktop.Sound = false
	if cfg.IsSystemSoundEnabled() {
		t.Fatal("disabled master sound still played system cue")
	}
	cfg.Notifications.Desktop.Sound = true
	cfg.Notifications.Desktop.SystemSound = &off
	if cfg.IsSystemSoundEnabled() {
		t.Fatal("explicit opt-out ignored")
	}
	if (*Config)(nil).IsSystemSoundEnabled() {
		t.Fatal("nil policy must remain silent")
	}
}

func TestSystemSoundCanBeEditedThroughExistingManagedLeaf(t *testing.T) {
	d, err := ParseDocument([]byte(`{"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":false}}}`), "/test/config.json", true)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{
		"/notifications/desktop/systemSound": json.RawMessage("true"),
	}}, AssetContext{Agent: AgentOpenCode, PluginRoot: "/test"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := next.Effective(AssetContext{Agent: AgentOpenCode, PluginRoot: "/test"})
	if err != nil || !cfg.IsSystemSoundEnabled() {
		t.Fatalf("sound opt-in lost through config edit: cfg=%+v err=%v", cfg, err)
	}
	if d.SchemaVersion() != 1 || next.SchemaVersion() != 1 {
		t.Fatal("setting native sound must not force schema migration")
	}
	if !json.Valid(next.Bytes()) {
		t.Fatal("edited config is malformed")
	}
}

func TestSystemSoundSchema2AgentProfilesStayIndependent(t *testing.T) {
	raw := []byte(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":true,"sound":true}},"agents":{"gemini":{"notifications":{"desktop":{"systemSound":false}}},"cursor":{"notifications":{"desktop":{"systemSound":true}}},"copilot-vscode":{"notifications":{"desktop":{"systemSound":true,"sound":false}}}}}`)
	d, err := ParseDocument(raw, "/TEST/agent-notifications.json", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		agent AgentID
		want  bool
	}{
		{AgentClaude, false},
		{AgentGemini, false},
		{AgentCursor, true},
		{AgentCopilotVSCode, false},
	} {
		view, err := d.Effective(AssetContext{Agent: tc.agent, PluginRoot: "/TEST"})
		if err != nil {
			t.Fatal(tc.agent, err)
		}
		if got := view.IsSystemSoundEnabled(); got != tc.want {
			t.Fatalf("%s system sound = %t; want %t (profile leak)", tc.agent, got, tc.want)
		}
	}
}
