package main

import (
	"encoding/json"
	"github.com/777genius/agent-notifications/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultVersionSources(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{".claude-plugin/plugin.json", ".codex-plugin/plugin.json"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct{ Version string }
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Version != config.ConsumerVersion || version != config.ConsumerVersion {
			t.Fatal("version sources disagree")
		}
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "var ConsumerVersion =") || !strings.Contains(string(workflow), "internal/config/runtime.go)") {
		t.Fatal("release gate must extract shared build version")
	}
}
