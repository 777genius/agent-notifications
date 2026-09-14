package setupwizard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseCodexPluginListContract(t *testing.T) {
	status, specs := parseCodexPluginList([]byte(`{"ok":true}`))
	if status != codexListUnknown || len(specs) != 0 {
		t.Fatalf("probe contract: %v %v", status, specs)
	}
	status, specs = parseCodexPluginList([]byte(`{"installed":[]}`))
	if status != codexListAbsent || len(specs) != 0 {
		t.Fatalf("empty list: %v %v", status, specs)
	}
	present := `{"installed":[{"pluginId":"agent-notify@managed","name":"agent-notify","marketplaceName":"managed","installed":true,"enabled":true}]}`
	status, specs = parseCodexPluginList([]byte(present))
	if status != codexListPresent || len(specs) != 1 || specs[0] != "agent-notify@managed" {
		t.Fatalf("present: %v %v", status, specs)
	}
	foreign := `{"installed":[{"pluginId":"other@managed","name":"other","marketplaceName":"managed","installed":true,"enabled":true}]}`
	status, specs = parseCodexPluginList([]byte(foreign))
	if status != codexListAbsent || len(specs) != 0 {
		t.Fatalf("foreign: %v %v", status, specs)
	}
}

func TestRunCodexPluginJSONUsesAllowlistEnv(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "explicit-codex")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(home, "env.txt")
	name := "probe"
	body := "#!/bin/sh\nenv > \"$CODEX_HOME/env.txt\"\nprintf '%s\\n' '{\"installed\":[]}'\n"
	if runtime.GOOS == "windows" {
		name = "probe.bat"
		body = "@echo off\r\nset > \"%CODEX_HOME%\\env.txt\"\r\necho {\"installed\":[]}\r\n"
	}
	probe := filepath.Join(dir, name)
	if err := os.WriteFile(probe, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(dir, "parent-codex"))
	t.Setenv("HOME", filepath.Join(dir, "parent-home"))
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-leak")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, ok := runCodexPluginJSON(ctx, probe, home, "plugin", "list", "--json")
	if !ok {
		t.Fatalf("probe failed: %s", got)
	}
	dumped, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	text := string(dumped)
	if !strings.Contains(text, "CODEX_HOME="+home) && !strings.Contains(text, "CODEX_HOME="+filepath.FromSlash(home)) {
		t.Fatalf("explicit CODEX_HOME missing: %s", text)
	}
	if strings.Contains(text, "AWS_SECRET_ACCESS_KEY") || strings.Contains(text, "should-not-leak") {
		t.Fatalf("secret leaked into child env: %s", text)
	}
	if strings.Contains(text, "parent-codex") || strings.Contains(text, "parent-home") {
		t.Fatalf("parent profile leaked into child env: %s", text)
	}
}
