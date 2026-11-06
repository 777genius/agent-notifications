package config

import "encoding/json"

// TerminalClickAuthorized requires fresh, explicit per-agent consent. It never
// proves the originating OS window is local or safely targetable.
func (c *Config) TerminalClickAuthorized() bool {
	return c != nil && c.terminalClickAuthorized && c.Notifications.Desktop.ClickToFocus
}

func (d Document) hasTerminalClickConsent(agent AgentID) bool {
	if d.schema != 2 || (agent != AgentQoder && agent != AgentGemini && agent != AgentCursor) {
		return false
	}
	type desktop struct {
		ClickToFocus                *bool `json:"clickToFocus"`
		TerminalClickConsentVersion *int  `json:"terminalClickConsentVersion"`
	}
	type profile struct {
		Notifications struct {
			Desktop desktop `json:"desktop"`
		} `json:"notifications"`
	}
	var stored struct {
		Notifications struct {
			Desktop desktop `json:"desktop"`
		} `json:"notifications"`
		Agents map[string]profile `json:"agents"`
	}
	if err := json.Unmarshal(d.original, &stored); err != nil {
		return false
	}
	if global := stored.Notifications.Desktop.ClickToFocus; global != nil && !*global {
		return false
	}
	p, ok := stored.Agents[string(agent)]
	if !ok {
		return false
	}
	a := p.Notifications.Desktop
	return a.ClickToFocus != nil && *a.ClickToFocus &&
		a.TerminalClickConsentVersion != nil && *a.TerminalClickConsentVersion == 1
}
