package config

import "testing"

func TestPiProfileOverridesOnlyPi(t *testing.T) {
	d, err := ParseDocument([]byte(`{"schemaVersion":2,"agents":{"pi":{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}},"statuses":{"task_complete":{"sound":"pi-cue.mp3"}}}}}`), "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []AgentID{AgentPi, AgentClaude, AgentCodex, AgentGemini, AgentOpenCode, AgentCursor} {
		cfg, err := d.Effective(AssetContext{Agent: agent})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.IsDesktopEnabled() != (agent != AgentPi) || cfg.Notifications.Desktop.Sound != (agent != AgentPi) || cfg.Notifications.Desktop.ClickToFocus != (agent != AgentPi) {
			t.Fatalf("%s did not retain its own desktop profile: %+v", agent, cfg.Notifications.Desktop)
		}
		status, _ := cfg.GetStatusInfo("task_complete")
		if (status.Sound == "pi-cue.mp3") != (agent == AgentPi) {
			t.Fatalf("%s sound=%q", agent, status.Sound)
		}
	}
}
