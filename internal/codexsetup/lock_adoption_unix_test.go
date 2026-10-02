//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package codexsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fails when setup cannot coexist with a read-only public lock left by another
// tool, or drops that tool's hooks while installing Notifications.
func TestRunAdoptsExistingHooksLock(t *testing.T) {
	bundle := fakeBundle(t)
	home := t.TempDir()
	hooks := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo foreign-hook"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	lock := hooks + ".lock"
	if err := os.WriteFile(lock, []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lock, 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(lock)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(Options{CodexHome: home, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("registration with legacy lock failed: %v", err)
	}
	data, err := os.ReadFile(hooks)
	if err != nil || !strings.Contains(string(data), "echo foreign-hook") || !strings.Contains(string(data), "codex-hook-wrapper") {
		t.Fatalf("foreign/installed hooks missing: %s %v", data, err)
	}
	if result.ForeignKept != 1 {
		t.Fatalf("foreign hook count: %d", result.ForeignKept)
	}
	after, err := os.Stat(lock)
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm() != 0600 {
		t.Fatalf("legacy lock not safely adopted: %v %v", after, err)
	}
}
