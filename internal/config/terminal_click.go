package config

import (
	"encoding/json"
	"path/filepath"
)

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

// TerminalClickSelection is the immutable selected-config proof for a single
// agent notification, not a permission to focus by itself. It is absent for
// seeded defaults, legacy schema or inherited permissions.
func (c *Config) TerminalClickSelection() (path, revision string, ok bool) {
	if c == nil || !c.TerminalClickAuthorized() || c.terminalClickPath == "" || c.terminalClickRevision == "" {
		return "", "", false
	}
	return c.terminalClickPath, c.terminalClickRevision, true
}

// BindTerminalClickSelection denies click routing when the effective profile
// came through a mutable symlink/alias. The callback only has the physical
// inode; without this check it could miss a selection switched after notify.
// Ordinary desktop notifications remain unaffected.
func (c *Config) BindTerminalClickSelection(selected string) {
	if c == nil {
		return
	}
	if selected != c.terminalClickPath || selected == "" {
		c.terminalClickAuthorized = false
		c.terminalClickPath = ""
		c.terminalClickRevision = ""
	}
}

// RecheckTerminalClickConsent refuses changed/revoked config at click time.
// The raw global veto is checked after parsing; nothing is enabled by defaults.
func RecheckTerminalClickConsent(path, revision string, agent AgentID) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || revision == "" {
		return false
	}
	snap, err := ReadFileSnapshot(path, MaxDocumentBytes)
	if err != nil || snap.PhysicalPath != path {
		return false
	}
	doc, err := ParseDocument(snap.Bytes, snap.PhysicalPath, true)
	return err == nil && doc.Revision() == revision && doc.hasTerminalClickConsent(agent)
}
