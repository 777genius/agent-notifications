package config

import (
	"context"
	"errors"
	configtemplate "github.com/777genius/agent-notifications/config"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRuntimeCanonicalFailureNeverFallsBack(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{true: "codex", false: "claude"}[quiet], func(t *testing.T) {
			home := t.TempDir()
			setTestHome(t, home)
			legacy := filepath.Join(home, ".claude", "claude-notifications-go", "config.json")
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"secret":"CANARY", broken`)
			if err := os.WriteFile(legacy, original, 0600); err != nil {
				t.Fatal(err)
			}
			bundle := t.TempDir()
			if err := os.MkdirAll(filepath.Join(bundle, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bundle, "config", "config.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			load := LoadFromPluginRoot
			if quiet {
				load = LoadFromPluginRootQuiet
			}
			got, err := load(bundle)
			var ce *Error
			if got != nil || !errors.As(err, &ce) || ce.Code != ConfigInvalid {
				t.Fatalf("expected typed canonical error: %v", err)
			}
			after, err := os.ReadFile(legacy)
			if err != nil || string(after) != string(original) {
				t.Fatal("canonical changed")
			}
			if _, err := os.Stat(legacy + ".lock"); !os.IsNotExist(err) {
				t.Fatal("read created lock")
			}
		})
	}
}

func TestRuntimeMissingIsReadOnlyAndUsesShippedDefaults(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	root := t.TempDir()
	selected, err := Resolve(SnapshotEnv())
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadFromPluginRootQuiet(root)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := SeedDocument(selected.Path)
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := ConsumerContext(root)
	expected, err := seed.Effective(assets)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("shipped defaults changed")
	}
	for _, p := range []string{filepath.Dir(selected.Path), filepath.Join(home, ".claude"), filepath.Join(home, ".agent-notifications-config.lock")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("read created %s", p)
		}
	}
}

func TestRuntimeUnknownHistoricalBundleRequiresImport(t *testing.T) {
	setTestHome(t, t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFromPluginRootQuiet(root)
	var ce *Error
	if got != nil || !errors.As(err, &ce) || ce.Code != ConfigLegacyImportRequired {
		t.Fatalf("historical config silently adopted: %v", err)
	}
}

func TestStorageValidationDoesNotNeedRuntimeSecret(t *testing.T) {
	raw := []byte(`{"notifications":{"webhook":{"enabled":true,"url":"${UNAVAILABLE_SECRET}"}},"future":{"large":9007199254740993}}`)
	document, err := ParseDocument(raw, "/fixture/config.json", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Effective(ValidationAssets("/fixture/bundle")); err != nil {
		t.Fatal(err)
	}
	if string(document.Bytes()) != string(raw) {
		t.Fatal("validation mutated raw data")
	}
	if _, err := document.Effective(AssetContext{}); err == nil {
		t.Fatal("runtime accepted missing required secret")
	}
}

func TestRuntimeTemplateRequiresMatchingReleaseProvenance(t *testing.T) {
	for _, version := range []string{"", "0.1.0", ConsumerVersion} {
		t.Run(version, func(t *testing.T) {
			setTestHome(t, t.TempDir())
			root := t.TempDir()
			for _, dir := range []string{"config", ".claude-plugin"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "config", "config.json"), configtemplate.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			manifest := `{"name":"claude-notifications-go","version":"` + version + `"}`
			if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadFromPluginRootQuiet(root)
			if version == ConsumerVersion {
				if err != nil || got == nil {
					t.Fatal(err)
				}
				assets, legacy := ConsumerContext(root)
				result, err := EnsureInitialized(context.Background(), InitRequest{Env: SnapshotEnv(), Assets: assets, Legacy: legacy})
				if err != nil || !result.Changed {
					t.Fatalf("real template init: %v", err)
				}
			} else {
				if err == nil || got != nil {
					t.Fatal("blessed unknown version from matching template bytes")
				}
			}
		})
	}
}

func TestSharedRolloutPolicy(t *testing.T) {
	setTestHome(t, t.TempDir())
	selected, err := Resolve(SnapshotEnv())
	if err != nil {
		t.Fatal(err)
	}
	expected := "legacy"
	if FreshNeutral {
		expected = "universal"
	}
	if selected.Source != expected || selected.Exists {
		t.Fatalf("fresh policy: %+v", selected)
	}
	result, err := EnsureInitialized(context.Background(), InitRequest{Env: SnapshotEnv(), Assets: ValidationAssets("")})
	if err != nil || !result.Changed || result.Selection.Path != selected.Path {
		t.Fatalf("init disagrees: %+v %v", result, err)
	}
	got, err := LoadFromPluginRootQuiet("")
	if err != nil || got == nil {
		t.Fatalf("runtime disagrees: %v", err)
	}
}
