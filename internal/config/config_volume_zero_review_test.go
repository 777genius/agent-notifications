package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitMutedDesktopVolumeSurvivesLegacyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"schemaVersion":1,"notifications":{"desktop":{"volume":0}}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Notifications.Desktop.Volume != 0 {
		t.Fatalf("legacy file volume should remain muted: %v %+v", err, loaded)
	}
	document, err := ParseDocument(data, path, false)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := document.Effective(AssetContext{})
	if err != nil || effective.Notifications.Desktop.Volume != 0 {
		t.Fatalf("effective schema-1 volume should remain muted: %v %+v", err, effective)
	}
	if DefaultConfig().Notifications.Desktop.Volume != 1 {
		t.Fatal("default full volume changed")
	}
}
