package config

import "encoding/json"

// ExplicitAgentDesktopSound reports an affirmative, per-agent sound choice.
// A global/default sound setting does not grant a newly added observer the
// right to start playing audio. Call only on a validated Document.
func (d Document) ExplicitAgentDesktopSound(agent AgentID) bool {
	if d.SchemaVersion() != 2 {
		return false
	}
	var agents map[string]json.RawMessage
	if json.Unmarshal(d.Raw()["agents"], &agents) != nil {
		return false
	}
	raw, ok := agents[string(agent)]
	if !ok {
		return false
	}
	var profile struct {
		Notifications struct {
			Desktop struct {
				Sound *bool `json:"sound"`
			} `json:"desktop"`
		} `json:"notifications"`
	}
	return json.Unmarshal(raw, &profile) == nil &&
		profile.Notifications.Desktop.Sound != nil &&
		*profile.Notifications.Desktop.Sound
}
