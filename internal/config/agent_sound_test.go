package config

import "testing"

func TestExplicitAgentDesktopSoundNeverInheritsGlobalDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"legacy_global_true", `{"notifications":{"desktop":{"sound":true}}}`, false},
		{"schema2_global_true", `{"schemaVersion":2,"notifications":{"desktop":{"sound":true}}}`, false},
		{"another_agent_true", `{"schemaVersion":2,"agents":{"codex":{"notifications":{"desktop":{"sound":true}}}}}`, false},
		{"opencode_missing_sound", `{"schemaVersion":2,"agents":{"opencode":{"notifications":{"desktop":{"enabled":true}}}}}`, false},
		{"opencode_explicit_false", `{"schemaVersion":2,"agents":{"opencode":{"notifications":{"desktop":{"sound":false}}}}}`, false},
		{"opencode_explicit_true", `{"schemaVersion":2,"agents":{"opencode":{"notifications":{"desktop":{"sound":true}}}}}`, true},
		{"opencode_explicit_true_global_false", `{"schemaVersion":2,"notifications":{"desktop":{"sound":false}},"agents":{"opencode":{"notifications":{"desktop":{"sound":true}}}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseDocument([]byte(tc.raw), "/test/agent-notifications.json", true)
			if err != nil {
				t.Fatal(err)
			}
			if got := doc.ExplicitAgentDesktopSound(AgentOpenCode); got != tc.want {
				t.Fatalf("sound opt-in = %t; want %t", got, tc.want)
			}
		})
	}
}

func TestExplicitAgentDesktopSoundRejectsAmbiguousSettings(t *testing.T) {
	raw := `{"schemaVersion":2,"agents":{"opencode":{"notifications":{"desktop":{"sound":false,"Sound":true}}}}}`
	if _, err := ParseDocument([]byte(raw), "/test/agent-notifications.json", true); err == nil {
		t.Fatal("ambiguous sound keys accepted")
	}
}
