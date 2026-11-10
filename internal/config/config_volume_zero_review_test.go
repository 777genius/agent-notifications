package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVolumeZeroPreservesLegacyAndSchemaTwoContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := []byte(`{"schemaVersion":1,"notifications":{"desktop":{"volume":0}}}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Notifications.Desktop.Volume != 1 {
		t.Fatalf("legacy file zero must preserve historical default: %v %+v", err, loaded)
	}
	original, err := ParseDocument(legacy, path, false)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := original.Effective(AssetContext{})
	if err != nil || effective.Notifications.Desktop.Volume != 1 {
		t.Fatalf("legacy effective zero contract changed: %v %+v", err, effective)
	}
	modern, err := ParseDocument([]byte(`{"schemaVersion":2,"notifications":{"desktop":{"volume":0}}}`), path, false)
	if err != nil {
		t.Fatal(err)
	}
	muted, err := modern.Effective(AssetContext{})
	if err != nil || muted.Notifications.Desktop.Volume != 0 {
		t.Fatalf("explicit schema-2 mute must remain zero: %v %+v", err, muted)
	}
	if DefaultConfig().Notifications.Desktop.Volume != 1 {
		t.Fatal("default full volume changed")
	}
}
