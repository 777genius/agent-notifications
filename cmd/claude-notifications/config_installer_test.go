package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/testenv"
)

func TestInstallerPreflightPathsWithoutInterpreter(t *testing.T) {
	box := t.TempDir()
	testenv.Set(t, box)
	target := filepath.Join(box, "space quote\" backslash\\ newline\n")
	if runtime.GOOS == "windows" {
		target = filepath.Join(box, "space unicode-é")
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", filepath.Join(target, "config.json"))
	var out, stderr bytes.Buffer
	if code := configCommand([]string{"installer", "preflight", target}, strings.NewReader(""), &out, &stderr); code == 0 {
		t.Fatal("accepted overlapping config")
	}
	if !strings.Contains(stderr.String(), "ConfigUnsafeTarget") {
		t.Fatalf("unexpected diagnostic %q", stderr.String())
	}
	if out.Len() != 0 {
		t.Fatal("installer leaked preflight document")
	}
}

func TestInstallerBootstrapProtectsStageAndRegistry(t *testing.T) {
	box := t.TempDir()
	testenv.Set(t, box)
	stage := filepath.Join(box, "stage")
	root := filepath.Join(box, "bundle")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(box, "registry.json")
	data, _ := json.Marshal(map[string]any{"plugins": map[string]any{"test": []installerEntry{{InstallPath: root, Version: "1.2.3"}}}})
	if err := os.WriteFile(registry, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{registry, "test", filepath.Join(box, "claude"), root, filepath.Join(box, "market"), filepath.Join(box, "codex"), "both", stage, registry, ""}
	request, err := installerBootstrapRequest(args)
	if err != nil {
		t.Fatal(err)
	}
	protected := request["protectedPaths"].([]string)
	if protected[0] != registry {
		t.Fatal("registry protection lost")
	}
	args[3] = box
	if _, err := installerBootstrapRequest(args); err == nil {
		t.Fatal("stage nested under refresh root accepted")
	}
	args[3] = root
	if err := os.WriteFile(registry, []byte(`{"plugins":{"test":[{"installPath":"relative"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installerBootstrapRequest(args); err == nil {
		t.Fatal("relative registry path accepted")
	}
}

func TestInstallerRegistryRejectsDuplicateKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"plugins":{},"plugins":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installerRegistry(path, "test"); err == nil {
		t.Fatal("duplicate registry key accepted")
	}
}

func TestInstallerRootPrefersValidVersionsOverPlaceholders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	root := t.TempDir()
	placeholderFirst := filepath.Join(root, "placeholder-first")
	validLow := filepath.Join(root, "valid-low")
	missingVersion := filepath.Join(root, "missing-version")
	validHigh := filepath.Join(root, "valid-high")
	entries := []map[string]any{
		{"installPath": placeholderFirst, "version": "unknown"},
		{"installPath": validLow, "version": "1.2.3"},
		{"installPath": missingVersion},
		{"installPath": validHigh, "version": "2.0.0"},
	}
	data, err := json.Marshal(map[string]any{"plugins": map[string]any{"test": entries}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := installerConfigCommand([]string{"root", path, "test"}, &out, &stderr); code != 0 {
		t.Fatalf("root: %d %s", code, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != validHigh {
		t.Fatalf("root = %q", got)
	}
}

func TestInstallerRootPlaceholderOnlyUsesRegistryOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	data, err := json.Marshal(map[string]any{"plugins": map[string]any{"test": []map[string]any{
		{"installPath": first, "version": "unknown"},
		{"installPath": second},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := installerConfigCommand([]string{"root", path, "test"}, &out, &stderr); code != 0 {
		t.Fatalf("root: %d %s", code, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != first {
		t.Fatalf("root = %q", got)
	}
}
