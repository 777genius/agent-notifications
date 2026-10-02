package config

import "testing"

func TestOpenCodeProfileIsSelectedOnlyForOpenCode(t *testing.T) {
	d, err := ParseDocument([]byte(`{"schemaVersion":2,"agents":{"opencode":{"notifications":{"desktop":{"enabled":false}}}}}`), "", false)
	if err != nil {
		t.Fatal(err)
	}
	openCode, err := d.Effective(AssetContext{Agent: AgentOpenCode})
	if err != nil {
		t.Fatal(err)
	}
	claude, err := d.Effective(AssetContext{Agent: AgentClaude})
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Notifications.Desktop.Enabled || !claude.Notifications.Desktop.Enabled {
		t.Fatalf("agent isolation: opencode=%v claude=%v", openCode.Notifications.Desktop.Enabled, claude.Notifications.Desktop.Enabled)
	}
}
