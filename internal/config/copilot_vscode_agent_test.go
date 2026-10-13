package config

import "testing"

// Red: the allowlist discards Local overrides or the new default implies success.
func TestCopilotVSCodeEffectiveIsolation(t *testing.T) {
	d, err := ParseDocument([]byte(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":false}},"agents":{"copilot-vscode":{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":true,"url":"http://127.0.0.1:1"}}}}}`), "/TEST/config.json", false)
	if err != nil {
		t.Fatal(err)
	}
	local, err := d.Effective(AssetContext{Agent: AgentCopilotVSCode, PluginRoot: "/TEST"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.Effective(AssetContext{Agent: AgentGemini, PluginRoot: "/TEST"})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := d.Effective(AssetContext{Agent: "unknown", PluginRoot: "/TEST"})
	if err != nil {
		t.Fatal(err)
	}
	if local.IsDesktopEnabled() || !local.IsWebhookEnabled() || !other.IsDesktopEnabled() || other.IsWebhookEnabled() || !unknown.IsDesktopEnabled() {
		t.Fatal("profile isolation failed")
	}
	for _, c := range []*Config{local, other, unknown} {
		s, ok := c.GetStatusInfo("agent_stopping")
		if !ok || s.Title != "Copilot in VS Code" || s.Sound != "" {
			t.Fatal("stopping default missing or unsafe")
		}
	}
}
