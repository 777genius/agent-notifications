package config

import "testing"

// Red condition: unknown-agent fallback silently ignores Gemini's explicit
// channel overrides and borrows the global profile.
func TestGeminiProfileOverridesGlobalWithoutChangingSiblings(t *testing.T) {
	d, err := ParseDocument([]byte(`{"schemaVersion":2,"notifications":{"webhook":{"enabled":true,"url":"https://example.test/hook"}},"agents":{"gemini":{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}}}}`), "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []AgentID{AgentGemini, AgentClaude, AgentCodex, AgentOpenCode, "unknown"} {
		cfg, err := d.Effective(AssetContext{Agent: agent})
		if err != nil {
			t.Fatal(err)
		}
		want := agent != AgentGemini
		if cfg.IsDesktopEnabled() != want || cfg.IsWebhookEnabled() != want {
			t.Fatalf("%s channels = %v/%v, want %v", agent, cfg.IsDesktopEnabled(), cfg.IsWebhookEnabled(), want)
		}
	}
}
