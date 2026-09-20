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
