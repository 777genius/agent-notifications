package config

import "testing"

func TestFreshTerminalClickConsent(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		agent AgentID
		want  bool
	}{
		{"schema1", `{"notifications":{"desktop":{"clickToFocus":true}}}`, AgentQoder, false},
		{"inherited_default", `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{}}}}}`, AgentQoder, false},
		{"old_true", `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true}}}}}`, AgentQoder, false},
		{"version_only", `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"terminalClickConsentVersion":1}}}}}`, AgentQoder, false},
		{"qoder", `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`, AgentQoder, true},
		{"gemini", `{"schemaVersion":2,"agents":{"gemini":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`, AgentGemini, true},
		{"cursor", `{"schemaVersion":2,"agents":{"cursor":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`, AgentCursor, true},
		{"global_veto", `{"schemaVersion":2,"notifications":{"desktop":{"clickToFocus":false}},"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`, AgentQoder, false},
		{"agent_veto", `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":false,"terminalClickConsentVersion":1}}}}}`, AgentQoder, false},
		{"wrong_agent", `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`, AgentGemini, false},
		{"opencode_excluded", `{"schemaVersion":2,"agents":{"opencode":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`, AgentOpenCode, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseDocument([]byte(tc.raw), "/TEST-config.json", true)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := d.Effective(AssetContext{Agent: tc.agent})
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.TerminalClickAuthorized(); got != tc.want {
				t.Fatalf("TerminalClickAuthorized=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestInvalidTerminalClickConsentVersion(t *testing.T) {
	for _, version := range []string{"0", "2", "-1"} {
		raw := `{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":` + version + `}}}}}`
		d, err := ParseDocument([]byte(raw), "/TEST-config.json", true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Effective(AssetContext{Agent: AgentQoder}); err == nil {
			t.Fatalf("accepted invalid consent marker %s", version)
		}
	}
}

func TestTerminalClickDeniesChangedSelection(t *testing.T) {
	raw := []byte(`{"schemaVersion":2,"agents":{"qoder":{"notifications":{"desktop":{"clickToFocus":true,"terminalClickConsentVersion":1}}}}}`)
	d, err := ParseDocument(raw, "/TEST/config.json", true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := d.Effective(AssetContext{Agent: AgentQoder})
	if err != nil || !cfg.TerminalClickAuthorized() {
		t.Fatal("fresh exact config should authorize opt-in", err)
	}
	cfg.BindTerminalClickSelection("/TEST/current-config-link")
	if cfg.TerminalClickAuthorized() {
		t.Fatal("mutable symlink/selection must not authorize focus")
	}
	if _, _, ok := cfg.TerminalClickSelection(); ok {
		t.Fatal("revoked selection still yielded a focus target")
	}
	ordinary, err := d.Effective(AssetContext{Agent: AgentQoder})
	if err != nil {
		t.Fatal(err)
	}
	ordinary.BindTerminalClickSelection("/TEST/config.json")
	if !ordinary.TerminalClickAuthorized() {
		t.Fatal("unchanged regular file selection lost consent")
	}
}
