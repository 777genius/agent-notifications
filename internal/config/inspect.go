package config

// Inspection is a deliberately restrictive diagnostic artifact. It never
// includes raw values, URLs, headers, payload fields or unknown property names.
// A failed parse is represented by its stable code, never its underlying text.
type Inspection struct {
	Selection     Selection     `json:"selection"`
	Revision      string        `json:"revision,omitempty"`
	SchemaVersion int           `json:"schemaVersion,omitempty"`
	Valid         bool          `json:"valid"`
	ErrorCode     Code          `json:"errorCode,omitempty"`
	Settings      *SafeSettings `json:"settings,omitempty"`
}

// InspectDocument validates a supplied snapshot without reading or writing files.
// Environment placeholders are treated as nonempty for structural validation;
// this diagnostic does not assert that runtime secrets are configured.
// physicalPath must be the canonical snapshot identity supplied by the FS adapter.
func InspectDocument(selection Selection, physicalPath string, data []byte) Inspection {
	out := Inspection{Selection: selection}
	d, err := ParseDocument(data, physicalPath, selection.Exists)
	if err != nil {
		out.ErrorCode = err.(*Error).Code
		return out
	}
	c, validationErr := d.Effective(AssetContext{LookupEnv: func(string) (string, bool) { return "CONFIG_ENV_PLACEHOLDER", true }})
	if validationErr != nil {
		out.ErrorCode = ConfigInvalid
		return out
	}
	out.Revision = d.Revision()
	out.SchemaVersion = d.SchemaVersion()
	out.Valid = true
	out.Settings = &SafeSettings{DesktopEnabled: c.Notifications.Desktop.Enabled, DesktopSound: c.Notifications.Desktop.Sound, Volume: c.Notifications.Desktop.Volume, Statuses: map[string]SafeStatus{}}
	for name := range buildDefaultConfig(".").Statuses {
		status, ok := c.Statuses[name]
		if !ok {
			continue
		}
		safe := SafeStatus{Enabled: status.Enabled}
		if status.Desktop != nil {
			safe.DesktopEnabled = status.Desktop.Enabled
		}
		if status.Webhook != nil {
			safe.WebhookEnabled = status.Webhook.Enabled
		}
		out.Settings.Statuses[name] = safe
	}
	return out
}

// SafeSettings is a numeric/boolean allowlist for a future wizard. Free-form
// strings and unknown status names are intentionally excluded from diagnostics.
type SafeSettings struct {
	DesktopEnabled bool                  `json:"desktopEnabled"`
	DesktopSound   bool                  `json:"desktopSound"`
	Volume         float64               `json:"volume"`
	Statuses       map[string]SafeStatus `json:"statuses"`
}
type SafeStatus struct {
	Enabled        *bool `json:"enabled"`
	DesktopEnabled *bool `json:"desktopEnabled"`
	WebhookEnabled *bool `json:"webhookEnabled"`
}
