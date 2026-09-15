package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The consumer no longer characterizes obsolete shell/wizard source recipes.
// Behavioral evidence: reading legacy L preserves raw settings and never writes
// a second live document into a bundle or the neutral location.
func TestLegacyCanonicalReadPreservesRawBytes(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	legacy := filepath.Join(home, ".claude", "claude-notifications-go", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{\n \"future\": {\"large\": 9007199254740993}, \"notifications\": {}\n}")
	if err := os.WriteFile(legacy, raw, 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	bundle := t.TempDir()
	if _, err := LoadFromPluginRootQuiet(bundle); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(legacy)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(raw) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("legacy read mutated document")
	}
	if _, err := os.Stat(filepath.Join(bundle, "config")); !os.IsNotExist(err) {
		t.Fatal("bundle config created")
	}
}
