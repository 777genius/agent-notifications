package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	configtemplate "github.com/777genius/agent-notifications/config"
	"github.com/777genius/agent-notifications/internal/config"
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

func TestInstallerBootstrapRetryTrustsOnlyCurrentUnmodifiedCacheTemplate(t *testing.T) {
	box := t.TempDir()
	testenv.Set(t, box)
	root := filepath.Join(box, "cache", config.ConsumerVersion)
	stage := filepath.Join(box, "stage")
	for _, path := range []string{filepath.Join(root, "config"), filepath.Join(root, ".claude-plugin"), stage} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "config", "config.json")
	manifestPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	if err := os.WriteFile(configPath, configtemplate.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	writeManifest := func(version string) {
		t.Helper()
		manifest := `{"name":"claude-notifications-go","version":"` + version + `"}`
		if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest(config.ConsumerVersion)
	registry := filepath.Join(box, "installed-before.json")
	data, err := json.Marshal(map[string]any{"plugins": map[string]any{"test": []installerEntry{{InstallPath: root, Version: config.ConsumerVersion}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, data, 0600); err != nil {
		t.Fatal(err)
	}
	request, err := installerBootstrapRequest([]string{registry, "test", filepath.Join(box, "claude"), filepath.Join(box, "cache"), filepath.Join(box, "market"), filepath.Join(box, "codex"), "both", stage, registry, ""})
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	check := func(data []byte, wantSafe bool) {
		t.Helper()
		var out, stderr bytes.Buffer
		code := configCommand([]string{"preflight-update", "--stdin", "--json"}, bytes.NewReader(data), &out, &stderr)
		if wantSafe && code != 0 {
			t.Fatalf("unmodified current template blocked: %s %s", out.String(), stderr.String())
		}
		if !wantSafe && (code == 0 || !strings.Contains(stderr.String(), string(config.ConfigLegacyImportRequired))) {
			t.Fatalf("unsafe historical config accepted: %d %s %s", code, out.String(), stderr.String())
		}
	}
	check(input, true)
	history := request["historicalCandidates"].([]config.HistoricalCandidate)
	request["historicalCandidates"] = history[1:]
	withoutActiveCandidate, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	check(withoutActiveCandidate, true)
	if err := os.WriteFile(configPath, []byte(`{"custom":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	check(input, false)
	check(withoutActiveCandidate, false)
	if err := os.WriteFile(configPath, configtemplate.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	writeManifest("0.0.0")
	check(input, false)
	check(withoutActiveCandidate, false)
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
