package config

import (
	"encoding/json"
	"testing"
)

func TestZellijFocusModesSurviveDocumentResolution(t *testing.T) {
	for _, mode := range []string{"auto", "pane", "tab", "off"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte(`{"notifications":{"desktop":{"zellijFocus":"` + mode + `"}}}`)
			document, err := ParseDocument(raw, "/fixture/config.json", true)
			if err != nil {
				t.Fatal(err)
			}
			effective, err := document.Effective(AssetContext{PluginRoot: "/fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if got := effective.Notifications.Desktop.ZellijFocus; got != mode {
				t.Fatalf("effective mode = %q, want %q", got, mode)
			}
			encoded, err := json.Marshal(effective)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Config
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if got := decoded.Notifications.Desktop.ZellijFocus; got != mode {
				t.Fatalf("persisted mode = %q, want %q", got, mode)
			}
		})
	}
}
