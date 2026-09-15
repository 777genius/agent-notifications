package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEffectiveDefaultsGolden(t *testing.T) {
	seed, err := SeedDocument("/fixture")
	if err != nil {
		t.Fatal(err)
	}
	c, err := seed.Effective(AssetContext{PluginRoot: "ASSET_ROOT"})
	if err != nil {
		t.Fatal(err)
	}
	c.Notifications.Desktop.AppIcon = filepath.ToSlash(c.Notifications.Desktop.AppIcon)
	for k, v := range c.Statuses {
		v.Sound = filepath.ToSlash(v.Sound)
		c.Statuses[k] = v
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	want, err := os.ReadFile("testdata/effective-defaults.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatal("effective shipped defaults differ from frozen golden")
	}
}
