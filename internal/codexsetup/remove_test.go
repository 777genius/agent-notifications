package codexsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveOnlyRegisteredConsumer(t *testing.T) {
	home := t.TempDir()
	opts := Options{ControlRoot: filepath.Join(home, "control"), CodexHome: home, PluginRoot: fakeBundle(t)}
	hooks := filepath.Join(home, "hooks.json")
	foreign := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"foreign-command"}]}]},"foreign":true}`
	if err := os.WriteFile(hooks, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	opts.Remove = true
	opts.PluginRoot = ""
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "foreign-command") || strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("wrong removed handlers: %s", data)
	}
	if _, err := Run(opts); err != nil {
		t.Fatalf("repeat uninstall: %v", err)
	}
}
