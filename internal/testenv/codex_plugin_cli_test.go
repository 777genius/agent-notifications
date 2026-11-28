package testenv

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCodexPluginCLI(t *testing.T) {
	first := filepath.Join(t.TempDir(), "codex")
	second := filepath.Join(t.TempDir(), "codex")
	call := func(home string, args ...string) ([]byte, error) {
		t.Helper()
		body, handled, err := CodexPluginCLI([]string{"CODEX_HOME=" + home}, append([]string{"/tmp/TEST-codex"}, args...))
		if !handled {
			t.Fatal("Codex CLI fixture did not recognize request")
		}
		return body, err
	}
	entries := func(home string) []map[string]any {
		t.Helper()
		body, err := call(home, "plugin", "list", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Installed []map[string]any
		}
		if err := json.Unmarshal(body, &doc); err != nil || doc.Installed == nil {
			t.Fatalf("malformed TEST Codex registry: %v", err)
		}
		return doc.Installed
	}
	if len(entries(first)) != 0 {
		t.Fatal("fresh Codex profile unexpectedly installed")
	}
	if _, err := call(first, "plugin", "marketplace", "add", filepath.Join(first, "source"), "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(first, "plugin", "add", "agent-notify@marketplace", "--json"); err != nil {
		t.Fatal(err)
	}
	if len(entries(first)) != 1 || len(entries(second)) != 0 {
		t.Fatal("fixture profile isolation failed")
	}
	if _, err := call(first, "plugin", "remove", "foreign@marketplace", "--json"); err != nil {
		t.Fatal(err)
	}
	if len(entries(first)) != 1 {
		t.Fatal("foreign removal affected managed identity")
	}
	if _, err := call(first, "plugin", "marketplace", "remove", "marketplace", "--json"); err == nil {
		t.Fatal("removed a marketplace that still holds a plugin")
	}
	if _, err := call(first, "plugin", "remove", "agent-notify@marketplace", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(first, "plugin", "marketplace", "remove", "marketplace", "--json"); err != nil {
		t.Fatal(err)
	}
	if len(entries(first)) != 0 {
		t.Fatal("exact Codex removal was not reflected in listing")
	}
	if _, err := call(first, "plugin", "unknown", "--json"); err == nil {
		t.Fatal("unknown native protocol was accepted")
	}
}
