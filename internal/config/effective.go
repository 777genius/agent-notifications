package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	configtemplate "github.com/777genius/agent-notifications/config"
)

// AssetContext is separate from resolver inputs. LookupEnv must be injected;
// no ambient environment is read when constructing an effective Config.
type AssetContext struct {
	Agent      AgentID
	PluginRoot string
	LookupEnv  func(string) (string, bool)
}

// Effective constructs an independent, expanded runtime value using historical
// defaults and tri-state semantics. Never marshal its result back to storage.
func (d Document) Effective(assets AssetContext) (*Config, error) {
	if d.schema == 1 {
		return d.effectiveProfile(assets, d.original)
	}
	profiles := d.prepareProfiles()
	for _, agent := range profiles.validationAgents() {
		data, err := profiles.mergedProfile(agent)
		if err != nil {
			return nil, err
		}
		if _, err := d.effectiveProfile(AssetContext{Agent: agent, LookupEnv: func(string) (string, bool) { return "CONFIG_ENV_PLACEHOLDER", true }}, data); err != nil {
			return nil, err
		}
	}
	if assets.Agent != AgentClaude && assets.Agent != AgentCodex {
		assets.Agent = ""
	}
	data, err := profiles.mergedProfile(assets.Agent)
	if err != nil {
		return nil, err
	}
	return d.effectiveProfile(assets, data)
}

func (d Document) effectiveProfile(assets AssetContext, data []byte) (*Config, error) {
	c := buildDefaultConfig("${AGENT_NOTIFICATIONS_ROOT}")
	defaults := make(map[string]StatusInfo, len(c.Statuses))
	for k, v := range c.Statuses {
		defaults[k] = v
	}
	if err := decodeTyped(data, c); err != nil {
		return nil, &Error{Code: ConfigInvalid}
	}
	if err := mergeStatusOverrides(data, c, defaults); err != nil {
		return nil, &Error{Code: ConfigInvalid}
	}
	if c.Statuses == nil {
		c.Statuses = defaults
	}
	expand := func(s string) string {
		return os.Expand(s, func(k string) string {
			if k == AssetRootPlaceholder || k == LegacyAssetRootPlaceholder {
				return assetRoot(assets)
			}
			if assets.LookupEnv != nil {
				if v, ok := assets.LookupEnv(k); ok {
					return v
				}
			}
			return ""
		})
	}
	expandPathValue := func(s string) string {
		v := expand(s)
		if v == "" {
			return ""
		}
		return filepath.Clean(v)
	}
	c.Notifications.Desktop.AppIcon = expandPathValue(c.Notifications.Desktop.AppIcon)
	c.Notifications.Webhook.URL = expand(c.Notifications.Webhook.URL)
	for k, v := range c.Statuses {
		v.Sound = expandPathValue(v.Sound)
		c.Statuses[k] = v
	}
	// A nonempty literal root prevents applyDefaults consulting the environment.
	// Missing statuses were already present before decode and merged above.
	if d.schema == 1 {
		c.applyDefaults(".")
	}
	if err := c.Validate(); err != nil {
		return nil, &Error{Code: ConfigInvalid}
	}
	return c, nil
}

// SeedDocument adds schema metadata to an independent copy of the exact shipped
// template. It is an internal capability only, not a runtime fallback cutover.
func SeedDocument(physicalPath string) (Document, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(configtemplate.Bytes(), &raw); err != nil {
		return Document{}, &Error{Code: ConfigInvalid}
	}
	raw["schemaVersion"] = json.RawMessage("1")
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Document{}, &Error{Code: ConfigInvalid}
	}
	return ParseDocument(append(data, '\n'), physicalPath, false)
}
